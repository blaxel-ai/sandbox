package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// uniqueProcessName keeps repeated runs against a long-lived sandbox-api from
// colliding on a name that is still registered.
func uniqueProcessName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// payloadSize is comfortably past the 4096-byte read buffer, so the line spans
// several reads however the tailer happens to slice it.
const payloadSize = 9000

// startLongLineProcess runs a command emitting one JSON line larger than the
// read buffer, and returns the process name.
func startLongLineProcess(t *testing.T, name string, settleBeforeStream time.Duration) string {
	t.Helper()
	command := fmt.Sprintf(
		`sh -c 'payload=$(head -c %d /dev/zero | tr "\0" "x"); echo "{\"type\":\"tool\",\"output\":\"$payload\"}"; sleep 10'`,
		payloadSize,
	)
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command": command,
		"name":    name,
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Controls whether the line is replayed from the log file or arrives live.
	time.Sleep(settleBeforeStream)
	return name
}

// readTaggedLines collects stream lines for a while, dropping keepalives.
func readTaggedLines(t *testing.T, processName string, window time.Duration) []string {
	t.Helper()
	streamResp, err := common.MakeRequestWithTimeout(
		http.MethodGet, "/process/"+processName+"/logs/stream", nil, window+5*time.Second)
	require.NoError(t, err)
	defer streamResp.Body.Close()
	require.Equal(t, http.StatusOK, streamResp.StatusCode)

	linesCh := make(chan string, 32)
	go func() {
		reader := bufio.NewReader(streamResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				linesCh <- line
			}
			if err != nil {
				close(linesCh)
				return
			}
		}
	}()

	var received []string
	deadline := time.After(window)
	for {
		select {
		case line, ok := <-linesCh:
			if !ok {
				return received
			}
			if strings.HasPrefix(line, "[keepalive]") {
				continue
			}
			received = append(received, line)
		case <-deadline:
			return received
		}
	}
}

// assertLineSurvived checks the payload reached the client whole: tagged once,
// at the start, and still valid JSON.
func assertLineSurvived(t *testing.T, lines []string) {
	t.Helper()
	var payload string
	for _, line := range lines {
		if strings.Contains(line, `"type":"tool"`) {
			payload = line
			break
		}
	}
	require.NotEmpty(t, payload, "the long line never arrived; got %d lines", len(lines))

	body := strings.TrimPrefix(payload, "stdout:")
	// Reported by offset rather than by dumping the payload: a failure prints a
	// 9KB line otherwise, and the offset is the diagnosis (it lands on a
	// multiple of the read buffer).
	if idx := strings.Index(body, "stdout:"); idx != -1 {
		t.Fatalf("stream tag spliced into the line at byte %d of %d: "+
			"a read boundary was treated as a line boundary", idx, len(body))
	}

	var parsed struct {
		Type   string `json:"type"`
		Output string `json:"output"`
	}
	require.NoError(t, json.Unmarshal([]byte(strings.TrimRight(body, "\n")), &parsed),
		"the line no longer parses as JSON")
	assert.Equal(t, payloadSize, len(parsed.Output), "payload truncated in transit")
}

// A log line longer than the read buffer must reach a client intact.
//
// The stream tags output with "stdout:" / "stderr:". Tagging each read rather
// than each line spliced the tag into the middle of any line past the buffer,
// so a consumer parsing its own output failed on valid data, and the bigger the
// output the more certain the failure.
func TestLongLineArrivesIntactLive(t *testing.T) {
	// Stream attached first: the line arrives through the live path.
	name := startLongLineProcess(t, uniqueProcessName("longline-live"), 0)
	assertLineSurvived(t, readTaggedLines(t, name, 3*time.Second))
}

func TestLongLineArrivesIntactFromBacklog(t *testing.T) {
	// Stream attached after the line was written: it is replayed from the
	// combined log file, which was corrupted the same way.
	name := startLongLineProcess(t, uniqueProcessName("longline-backlog"), 2*time.Second)
	assertLineSurvived(t, readTaggedLines(t, name, 3*time.Second))
}
