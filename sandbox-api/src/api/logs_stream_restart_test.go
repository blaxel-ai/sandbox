package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/gin-gonic/gin"
)

// A log stream must survive a restart of the process it follows.
//
// The handler used to wait on the per-run Done channel, which a restart closes
// before installing a fresh one. So the stream ended with 200 and no error the
// moment the process bounced, while the process came back up and kept producing
// output nobody received: from the caller's side the process looks alive and
// silent, with nothing in the logs explaining why.
func TestLogStreamSurvivesProcessRestart(t *testing.T) {
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(SetupRouter(true, false))
	defer server.Close()

	// Fails once, restarts, then prints run2 and stays up.
	marker := process.ProcessLogDir + "/once"
	command := "sh -c 'if [ ! -f " + marker + " ]; then touch " + marker +
		"; echo run1; exit 1; fi; echo run2; sleep 30'"
	body, err := json.Marshal(map[string]any{
		"command":          command,
		"name":             "restarter",
		"restartOnFailure": true,
		"maxRestarts":      3,
	})
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	resp, err := http.Post(server.URL+"/process", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("starting process: status %d", resp.StatusCode)
	}

	streamResp, err := http.Get(server.URL + "/process/restarter/logs/stream")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer streamResp.Body.Close()

	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(streamResp.Body)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	// "run2" only exists after the restart, so receiving it proves the stream
	// outlived the restart rather than ending with the first run.
	var seen []string
	deadline := time.After(30 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatalf("stream closed before the restarted process produced output; saw: %v", seen)
			}
			if strings.Contains(line, "[keepalive]") {
				continue
			}
			seen = append(seen, line)
			if strings.Contains(line, "run2") {
				return // the stream carried across the restart
			}
		case <-deadline:
			t.Fatalf("timed out waiting for post-restart output; saw: %v", seen)
		}
	}
}
