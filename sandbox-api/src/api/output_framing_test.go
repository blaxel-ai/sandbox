package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/gin-gonic/gin"
)

func newFramingTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(SetupRouter(true, false))
	t.Cleanup(server.Close)
	return server
}

func postProcess(t *testing.T, server *httptest.Server, request map[string]any, accept string) *http.Response {
	t.Helper()
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	httpRequest, err := http.NewRequest(http.MethodPost, server.URL+"/process", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if accept != "" {
		httpRequest.Header.Set("Accept", accept)
	}
	resp, err := http.DefaultClient.Do(httpRequest)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("POST /process: HTTP %d", resp.StatusCode)
	}
	return resp
}

// The exec stream forwards output as the process writes it: a prompt with no
// newline arrives on its own, before the process writes anything else, and
// newlines reach the client untouched.
func TestExecStreamSendsRawChunksWithoutHoldingPartialLines(t *testing.T) {
	server := newFramingTestServer(t)
	resp := postProcess(t, server, map[string]any{
		"command": "printf 'prompt> '; sleep 2; printf 'a\\nb\\n'",
	}, "application/x-ndjson")
	defer resp.Body.Close()

	type event struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	events := make(chan event, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			var e event
			if json.Unmarshal(scanner.Bytes(), &e) == nil {
				events <- e
			}
		}
	}()

	start := time.Now()
	var stdout strings.Builder
	for e := range events {
		if e.Type != "stdout" {
			if e.Type == "result" {
				break
			}
			continue
		}
		if stdout.Len() == 0 {
			if e.Data != "prompt> " {
				t.Fatalf("first chunk = %q, want the bare prompt", e.Data)
			}
			if elapsed := time.Since(start); elapsed > time.Second {
				t.Fatalf("prompt took %s to arrive: it was held back until more output came", elapsed)
			}
		}
		stdout.WriteString(e.Data)
	}
	if got := stdout.String(); got != "prompt> a\nb\n" {
		t.Fatalf("stdout = %q, want %q", got, "prompt> a\nb\n")
	}
}

// The text log stream replays the backlog and then follows live output, with
// newlines kept and a partial line sent as soon as it is written.
func TestLogStreamKeepsNewlinesAndSendsPartialLines(t *testing.T) {
	server := newFramingTestServer(t)
	resp := postProcess(t, server, map[string]any{
		"command": "printf 'line1\\n'; sleep 1; printf 'prompt> '; sleep 2; printf 'line2\\n'",
		"name":    "framing",
	}, "")
	resp.Body.Close()
	// Let line1 land in the backlog before the stream attaches.
	time.Sleep(500 * time.Millisecond)

	streamResp, err := http.Get(server.URL + "/process/framing/logs/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer streamResp.Body.Close()

	chunks := make(chan string, 64)
	go func() {
		defer close(chunks)
		buf := make([]byte, 4096)
		for {
			n, err := streamResp.Body.Read(buf)
			if n > 0 {
				chunks <- string(buf[:n])
			}
			if err != nil {
				if err != io.EOF {
					t.Errorf("reading stream: %v", err)
				}
				return
			}
		}
	}()

	var received strings.Builder
	promptDeadline := time.After(2500 * time.Millisecond)
	for !strings.HasSuffix(received.String(), "stdout:prompt> ") {
		select {
		case chunk, ok := <-chunks:
			if !ok {
				t.Fatalf("stream ended before the prompt arrived; got %q", received.String())
			}
			received.WriteString(chunk)
		case <-promptDeadline:
			t.Fatalf("prompt not sent before the process wrote more; got %q", received.String())
		}
	}
	for chunk := range chunks {
		received.WriteString(chunk)
	}
	if got, want := received.String(), "stdout:line1\nstdout:prompt> line2\n"; got != want {
		t.Fatalf("stream = %q, want %q", got, want)
	}
}
