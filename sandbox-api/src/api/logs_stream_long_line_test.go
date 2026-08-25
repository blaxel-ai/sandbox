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

// A log line longer than the read buffer must reach the client intact.
//
// The stream used to tag each 4096-byte read rather than each line, so a long
// line came out with "stdout:" spliced into the middle of it. Any consumer
// parsing the line then failed on perfectly valid application output, and the
// bigger the output the more certain the failure.
func TestLogStreamKeepsLongLinesIntact(t *testing.T) {
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	gin.SetMode(gin.TestMode)
	server := httptest.NewServer(SetupRouter(true, false))
	defer server.Close()

	// One JSON line well past the 4096-byte read buffer, like a large tool result.
	const payloadSize = 9000
	command := `sh -c 'payload=$(head -c ` + itoa(payloadSize) + ` /dev/zero | tr "\0" "x"); ` +
		`echo "{\"type\":\"tool\",\"output\":\"$payload\"}"; sleep 10'`
	body, err := json.Marshal(map[string]any{"command": command, "name": "longline"})
	if err != nil {
		t.Fatalf("marshalling request: %v", err)
	}
	resp, err := http.Post(server.URL+"/process", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	resp.Body.Close()

	streamResp, err := http.Get(server.URL + "/process/longline/logs/stream")
	if err != nil {
		t.Fatalf("opening stream: %v", err)
	}
	defer streamResp.Body.Close()

	lines := make(chan string, 16)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(streamResp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()

	deadline := time.After(30 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			if !ok {
				t.Fatal("stream ended before the long line arrived")
			}
			if !strings.Contains(line, `"type":"tool"`) {
				continue
			}
			payload := strings.TrimPrefix(line, "stdout:")

			// The tag belongs at the start, and nowhere else.
			if idx := strings.Index(payload, "stdout:"); idx != -1 {
				t.Fatalf("stream tag spliced into the line at byte %d", idx)
			}
			var parsed struct {
				Type   string `json:"type"`
				Output string `json:"output"`
			}
			if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
				t.Fatalf("line no longer parses as JSON: %v", err)
			}
			if len(parsed.Output) != payloadSize {
				t.Fatalf("payload truncated: got %d bytes, want %d", len(parsed.Output), payloadSize)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for the long line")
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
