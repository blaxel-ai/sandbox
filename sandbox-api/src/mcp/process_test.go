package mcp

import (
	"context"
	stdjson "encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProcessTerminationToolsObserveActualStatus(t *testing.T) {
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := &Server{mcpServer: protocol.NewServer(&protocol.Implementation{Name: "test", Version: "1"}, nil), handlers: &Handlers{Process: handler.NewProcessHandler()}}
	if err := s.registerProcessTools(); err != nil {
		t.Fatal(err)
	}
	ct, st := protocol.NewInMemoryTransports()
	ss, err := s.mcpServer.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	call := func(t *testing.T, tool string, input any) ProcessExecuteOutput {
		t.Helper()
		result, err := cs.CallTool(ctx, &protocol.CallToolParams{Name: tool, Arguments: input})
		if err != nil {
			t.Fatal(err)
		}
		if result.IsError {
			t.Fatalf("%s: %+v", tool, result.Content)
		}
		data, err := stdjson.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var output ProcessExecuteOutput
		if err := stdjson.Unmarshal(data, &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	for _, tool := range []string{"processStop", "processKill"} {
		t.Run(tool, func(t *testing.T) {
			name := fmt.Sprintf("mcp-%s-%d", tool, time.Now().UnixNano())
			command := `exec sh -c 'trap "sleep 1; echo FINAL; exit 7" TERM; echo READY; while :; do sleep 0.05; done'`
			call(t, "processExecute", map[string]any{"name": name, "command": command, "keepAlive": false})
			defer s.handlers.Process.KillProcess(name)
			input := ProcessIdentifierInput{Identifier: name}
			deadline := time.Now().Add(5 * time.Second)
			for {
				logs, err := s.handlers.Process.GetProcessOutput(name)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(logs.Logs, "READY") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("process never ready")
				}
				time.Sleep(50 * time.Millisecond)
			}
			result := call(t, tool, input)
			if tool == "processStop" && result.Status != "running" {
				t.Fatalf("premature stop confirmation: %+v", result)
			}
			if tool == "processKill" && result.Status != "running" && result.Status != "killed" {
				t.Fatalf("unexpected kill status: %+v", result)
			}
			for {
				result = call(t, "processGet", input)
				if result.Status != "running" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("process never finished")
				}
				time.Sleep(50 * time.Millisecond)
			}
			expected := "killed"
			if tool == "processStop" {
				expected = "stopped"
				if result.ExitCode != 7 || !strings.Contains(result.Logs, "FINAL") {
					t.Fatalf("final output missing: %+v", result)
				}
			}
			if result.Status != expected {
				t.Fatalf("status=%s want=%s", result.Status, expected)
			}
		})
	}
}

func TestProcessExecuteWaitCapPreservesExecutionDeadline(t *testing.T) {
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	s := &Server{mcpServer: protocol.NewServer(&protocol.Implementation{Name: "test", Version: "1"}, nil), handlers: &Handlers{Process: handler.NewProcessHandler()}}
	if err := s.registerProcessTools(); err != nil {
		t.Fatal(err)
	}
	ct, st := protocol.NewInMemoryTransports()
	ss, err := s.mcpServer.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ss.Close() })
	cs, err := protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	for _, timeout := range []int{0, 120} {
		t.Run(fmt.Sprintf("timeout-%d", timeout), func(t *testing.T) {
			t.Parallel()
			name := fmt.Sprintf("mcp-wait-cap-%d-%d", timeout, time.Now().UnixNano())
			t.Cleanup(func() { s.handlers.Process.KillProcess(name) })
			result, err := cs.CallTool(ctx, &protocol.CallToolParams{Name: "processExecute", Arguments: map[string]any{
				"name": name, "command": "true", "timeout": timeout,
				"waitForCompletion": true, "keepAlive": false,
			}})
			if err != nil {
				t.Fatal(err)
			}
			if result.IsError {
				t.Fatalf("processExecute failed: %+v", result.Content)
			}
			data, err := stdjson.Marshal(result.StructuredContent)
			if err != nil {
				t.Fatal(err)
			}
			var output ProcessExecuteOutput
			if err := stdjson.Unmarshal(data, &output); err != nil {
				t.Fatal(err)
			}
			if output.PollRequired || output.Status != "completed" || output.PID == "" {
				t.Fatalf("short command should complete normally: %+v", output)
			}
			proc, exists := process.GetProcessManager().GetProcessByIdentifier(name)
			if !exists {
				t.Fatal("completed process disappeared")
			}
			if proc.Timeout != timeout {
				t.Fatalf("execution timeout=%d, want=%d", proc.Timeout, timeout)
			}
		})
	}
}

func TestProcessWaitTimeout(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout int
		wait    bool
		want    int
		capped  bool
	}{
		{"unlimited synchronous", 0, true, MaxWaitForCompletionTimeout, true},
		{"negative synchronous", -1, true, MaxWaitForCompletionTimeout, true},
		{"long synchronous", 120, true, MaxWaitForCompletionTimeout, true},
		{"at cap", MaxWaitForCompletionTimeout, true, MaxWaitForCompletionTimeout, false},
		{"short synchronous", 1, true, 1, false},
		{"unlimited background", 0, false, 0, false},
		{"long background", 120, false, 120, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, capped := processWaitTimeout(tc.timeout, tc.wait)
			if got != tc.want || capped != tc.capped {
				t.Fatalf("got (%d, %v), want (%d, %v)", got, capped, tc.want, tc.capped)
			}
		})
	}
}
