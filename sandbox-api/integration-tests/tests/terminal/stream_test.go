package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

type terminalMessage struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

// openStream follows GET /terminal/stream and yields its decoded events.
func openStream(t *testing.T, sessionID string) <-chan terminalMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, common.BaseURL+"/terminal/stream?sessionId="+sessionID, nil)
	require.NoError(t, err)
	// The shared client has a 10s timeout, too short for a stream.
	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	events := make(chan terminalMessage, 256)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			payload, ok := strings.CutPrefix(scanner.Text(), "data: ")
			if !ok {
				continue
			}
			var msg terminalMessage
			if json.Unmarshal([]byte(payload), &msg) == nil {
				events <- msg
			}
		}
	}()
	return events
}

func sendInput(t *testing.T, sessionID string, msg map[string]any) int {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPost, "/terminal/input?sessionId="+sessionID, msg)
	require.NoError(t, err)
	resp.Body.Close()
	return resp.StatusCode
}

func waitFor(t *testing.T, events <-chan terminalMessage, match func(terminalMessage, string) bool) {
	t.Helper()
	var seen strings.Builder
	deadline := time.After(15 * time.Second)
	for {
		select {
		case msg, ok := <-events:
			require.True(t, ok, "stream closed early, output so far: %q", seen.String())
			seen.WriteString(msg.Data)
			if match(msg, seen.String()) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out, output so far: %q", seen.String())
		}
	}
}

func outputContains(want string) func(terminalMessage, string) bool {
	return func(_ terminalMessage, seen string) bool { return strings.Contains(seen, want) }
}

func TestTerminalHTTPStream(t *testing.T) {
	sessionID := "it-http-stream-" + time.Now().Format("150405.000")
	events := openStream(t, sessionID)
	t.Cleanup(func() { sendInput(t, sessionID, map[string]any{"type": "input", "data": "exit\r"}) })

	require.Equal(t, http.StatusNoContent, sendInput(t, sessionID, map[string]any{"type": "input", "data": "echo http-$((20+22))\r"}))
	waitFor(t, events, outputContains("http-42"))

	require.Equal(t, http.StatusNoContent, sendInput(t, sessionID, map[string]any{"type": "resize", "cols": 132, "rows": 43}))
	sendInput(t, sessionID, map[string]any{"type": "input", "data": "stty size\r"})
	waitFor(t, events, outputContains("43 132"))

	// A second stream on the same session replays the history.
	waitFor(t, openStream(t, sessionID), outputContains("http-42"))

	sendInput(t, sessionID, map[string]any{"type": "input", "data": "exit\r"})
	waitFor(t, events, func(msg terminalMessage, _ string) bool { return msg.Type == "exit" })
}

func TestTerminalHTTPInputErrors(t *testing.T) {
	require.Equal(t, http.StatusNotFound, sendInput(t, "it-http-no-such-session", map[string]any{"type": "input", "data": "ls\r"}))

	sessionID := "it-http-errors-" + time.Now().Format("150405.000")
	openStream(t, sessionID)
	t.Cleanup(func() { sendInput(t, sessionID, map[string]any{"type": "input", "data": "exit\r"}) })
	require.Equal(t, http.StatusBadRequest, sendInput(t, sessionID, map[string]any{"type": "paste", "data": "x"}))
	require.Equal(t, http.StatusBadRequest, sendInput(t, sessionID, map[string]any{"type": "resize"}))
}
