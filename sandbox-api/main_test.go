package main

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The API serves several endpoints that stay open for the whole lifetime of a
// process or a watch. A non-zero ReadTimeout/WriteTimeout on the server caps
// that lifetime, so guard the values explicitly.
func TestNewHTTPServerHasNoStreamKillingTimeouts(t *testing.T) {
	server := newHTTPServer(":8080", http.NewServeMux())

	if server.WriteTimeout != 0 {
		t.Errorf("WriteTimeout must stay 0, got %v: it is an absolute deadline armed at request start, "+
			"so it truncates /process/:identifier/logs/stream and every other long-lived endpoint", server.WriteTimeout)
	}
	if server.ReadTimeout != 0 {
		t.Errorf("ReadTimeout must stay 0, got %v: it kills the read side of hijacked WebSocket "+
			"connections (/ws/terminal/:name)", server.ReadTimeout)
	}
	if server.ReadHeaderTimeout == 0 {
		t.Error("ReadHeaderTimeout must stay set: it bounds header slowloris without touching streams")
	}
}

// Documents the failure mode the constructor guards against: WriteTimeout fires
// on a stream that never stops producing data, so keepalives cannot save it.
func TestWriteTimeoutTruncatesLongLivedStream(t *testing.T) {
	const lines = 10
	const interval = 100 * time.Millisecond

	// A handler that behaves like the log stream: one line every interval.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		for i := 0; i < lines; i++ {
			if _, err := w.Write([]byte("line\n")); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(interval)
		}
	})

	countLines := func(t *testing.T, writeTimeout time.Duration) int {
		t.Helper()
		server := httptest.NewUnstartedServer(handler)
		server.Config.WriteTimeout = writeTimeout
		server.Start()
		defer server.Close()

		resp, err := server.Client().Get(server.URL)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		got := 0
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			got++
		}
		return got
	}

	// Half the stream duration: the connection dies mid-stream.
	if got := countLines(t, lines*interval/2); got >= lines {
		t.Errorf("expected the stream to be truncated by WriteTimeout, got all %d lines", got)
	}

	// What newHTTPServer does: the stream runs to completion.
	if got := countLines(t, 0); got != lines {
		t.Errorf("expected %d lines without WriteTimeout, got %d", lines, got)
	}
}
