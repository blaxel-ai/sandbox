package handler

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func terminalTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := NewTerminalHandler()
	r.GET("/terminal/stream", h.HandleTerminalStream)
	r.POST("/terminal/input", h.HandleTerminalInput)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

// openTerminalStream opens the SSE stream and returns a channel of decoded events.
func openTerminalStream(t *testing.T, srv *httptest.Server, sessionID string) <-chan TerminalMessage {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/terminal/stream?sessionId="+sessionID+"&cols=100&rows=30", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stream status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	events := make(chan TerminalMessage, 256)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			var msg TerminalMessage
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &msg); err == nil {
				events <- msg
			}
		}
	}()
	return events
}

func postTerminalInput(t *testing.T, srv *httptest.Server, sessionID string, body string) int {
	t.Helper()
	resp, err := http.Post(srv.URL+"/terminal/input?sessionId="+sessionID, "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func waitForOutput(t *testing.T, events <-chan TerminalMessage, want string) {
	t.Helper()
	var seen strings.Builder
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-events:
			if !ok {
				t.Fatalf("stream closed before %q, got %q", want, seen.String())
			}
			seen.WriteString(msg.Data)
			if strings.Contains(seen.String(), want) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %q, got %q", want, seen.String())
		}
	}
}

func TestTerminalStream_InputReachesShellAndOutputStreams(t *testing.T) {
	srv := terminalTestServer(t)
	sessionID := "sse-roundtrip-" + t.Name()
	events := openTerminalStream(t, srv, sessionID)

	if code := postTerminalInput(t, srv, sessionID, `{"type":"input","data":"echo sse-$((40+2))\r"}`); code != http.StatusNoContent {
		t.Fatalf("input status %d", code)
	}
	waitForOutput(t, events, "sse-42")

	if code := postTerminalInput(t, srv, sessionID, `{"type":"resize","cols":120,"rows":40}`); code != http.StatusNoContent {
		t.Fatalf("resize status %d", code)
	}
	postTerminalInput(t, srv, sessionID, `{"type":"input","data":"stty size\r"}`)
	waitForOutput(t, events, "40 120")
}

func TestTerminalStream_ReconnectReplaysHistory(t *testing.T) {
	srv := terminalTestServer(t)
	sessionID := "sse-replay-" + t.Name()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/terminal/stream?sessionId="+sessionID, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	postTerminalInput(t, srv, sessionID, `{"type":"input","data":"echo replay-$((1+1))\r"}`)
	time.Sleep(500 * time.Millisecond)
	cancel()
	resp.Body.Close()

	waitForOutput(t, openTerminalStream(t, srv, sessionID), "replay-2")
}

func TestTerminalStream_ExitEventWhenShellEnds(t *testing.T) {
	srv := terminalTestServer(t)
	sessionID := "sse-exit-" + t.Name()
	events := openTerminalStream(t, srv, sessionID)
	postTerminalInput(t, srv, sessionID, `{"type":"input","data":"exit\r"}`)

	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-events:
			if !ok {
				t.Fatal("stream closed without an exit event")
			}
			if msg.Type == "exit" {
				return
			}
		case <-deadline:
			t.Fatal("no exit event")
		}
	}
}

func TestTerminalInput_Errors(t *testing.T) {
	srv := terminalTestServer(t)
	if code := postTerminalInput(t, srv, "sse-missing-session", `{"type":"input","data":"ls\r"}`); code != http.StatusNotFound {
		t.Fatalf("unknown session: want 404, got %d", code)
	}

	sessionID := "sse-errors-" + t.Name()
	openTerminalStream(t, srv, sessionID)
	cases := map[string]string{
		"invalid json":   `{`,
		"unknown type":   `{"type":"paste","data":"x"}`,
		"resize no size": `{"type":"resize"}`,
	}
	for name, body := range cases {
		if code := postTerminalInput(t, srv, sessionID, body); code != http.StatusBadRequest {
			t.Errorf("%s: want 400, got %d", name, code)
		}
	}
	big := `{"type":"input","data":"` + strings.Repeat("a", terminalInputMaxBytes) + `"}`
	if code := postTerminalInput(t, srv, sessionID, big); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: want 413, got %d", code)
	}
	padded := `{"type":"input","data":"x"}` + strings.Repeat(" ", terminalInputMaxBytes)
	if code := postTerminalInput(t, srv, sessionID, padded); code != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body after a valid message: want 413, got %d", code)
	}
}

func TestSplitIncompleteUTF8(t *testing.T) {
	euro := []byte("€") // 3 bytes
	cases := []struct {
		in, complete, rest []byte
	}{
		{[]byte("abc"), []byte("abc"), nil},
		{append([]byte("a"), euro...), append([]byte("a"), euro...), nil},
		{append([]byte("a"), euro[:1]...), []byte("a"), euro[:1]},
		{append([]byte("a"), euro[:2]...), []byte("a"), euro[:2]},
		{euro[1:], euro[1:], nil}, // stray continuation bytes are not held back
		{nil, nil, nil},
	}
	for _, tc := range cases {
		complete, rest := splitIncompleteUTF8(tc.in)
		if !bytes.Equal(complete, tc.complete) || !bytes.Equal(rest, tc.rest) {
			t.Errorf("splitIncompleteUTF8(%q) = %q, %q; want %q, %q", tc.in, complete, rest, tc.complete, tc.rest)
		}
	}
}
