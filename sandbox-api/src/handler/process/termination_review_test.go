package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestUnstartedProcessClosesStdin(t *testing.T) {
	for _, scenario := range []string{"canceled-restart", "failed-restart", "failed-start"} {
		t.Run(scenario, func(t *testing.T) {
			pm := newStdinTestManager(t)
			if scenario != "canceled-restart" {
				t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
			}
			before, err := openDescriptorNames()
			if err != nil {
				t.Fatal(err)
			}
			for range 16 {
				if scenario == "failed-start" {
					if _, err := pm.StartProcess("true", "", nil, false, 0, false, 0, true, nil); err == nil {
						t.Fatal("expected start failure")
					}
					continue
				}
				p := &ProcessInfo{Command: "true", Stdin: true, StdoutFile: filepath.Join(ProcessLogDir, "stdout"), StderrFile: filepath.Join(ProcessLogDir, "stderr")}
				if scenario == "canceled-restart" {
					p.terminationRequested = StatusKilled
				}
				if _, err := pm.restartProcess(p, nil); err == nil {
					t.Fatal("expected restart failure")
				}
				if p.stdin.w != nil {
					t.Error("failed setup retained stdin writer")
				}
			}
			after, err := openDescriptorNames()
			if err != nil {
				t.Fatal(err)
			}
			if len(after) > len(before) {
				t.Fatalf("stdin descriptors leaked: before=%d after=%d", len(before), len(after))
			}
		})
	}
}

func TestRestoreDeadProcessPreservesTermination(t *testing.T) {
	for _, intent := range []string{"", string(StatusStopped), string(StatusKilled)} {
		for _, mismatch := range []bool{false, true} {
			name := intent + "-dead"
			if mismatch {
				name = intent + "-reused-pid"
			}
			t.Run(name, func(t *testing.T) {
				if mismatch && runtime.GOOS != "linux" {
					t.Skip("PID command verification uses /proc")
				}
				pm := newStdinTestManager(t)
				statePath := filepath.Join(t.TempDir(), "state.json")
				t.Setenv("SANDBOX_STATE_FILE", statePath)
				pid := 2147483647
				if mismatch {
					pid = os.Getpid()
				}
				proc := ProcessState{PID: "saved", Name: "saved", Command: "definitely-not-the-current-process", ProcessPid: pid, Status: StatusRunning}
				switch intent {
				case string(StatusStopped):
					proc.TerminationRequested = StatusStopped
				case string(StatusKilled):
					proc.TerminationRequested = StatusKilled
				}
				data, err := json.Marshal(ManagerState{Version: 1, Processes: map[string]ProcessState{"saved": proc}})
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statePath, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := pm.LoadState(); err != nil {
					t.Fatal(err)
				}
				p, ok := pm.GetProcessSnapshot("saved")
				if !ok {
					t.Fatal("missing restored process")
				}
				want := StatusFailed
				if proc.TerminationRequested != "" {
					want = proc.TerminationRequested
				}
				if p.Status != want || p.ExitCode != -1 || p.CompletedAt == nil {
					t.Fatalf("wrong restored outcome: %+v", p)
				}
			})
		}
	}

}

func openDescriptorNames() ([]string, error) {
	dir, err := os.Open("/dev/fd")
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return dir.Readdirnames(-1)
}
