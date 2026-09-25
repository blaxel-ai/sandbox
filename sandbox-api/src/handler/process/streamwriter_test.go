package process

import (
	"strings"
	"sync"
	"testing"
)

type recordingWriter struct {
	mu   sync.Mutex
	data strings.Builder
}

func (r *recordingWriter) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.Write(p)
}

func (r *recordingWriter) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data.String()
}

func TestPendingWriterHoldsOutputUntilReleased(t *testing.T) {
	target := &recordingWriter{}
	pending := newPendingWriter(target)

	writeToLogWriter(pending, "stdout", []byte("live 1\n"))
	writeToLogWriter(pending, "stderr", []byte("live 2\n"))

	// The backlog is replayed straight to the target while the live output waits.
	writeToLogWriter(target, "stdout", []byte("backlog\n"))
	if got := target.String(); got != "stdout:backlog\n" {
		t.Fatalf("target has %q before release, want only the backlog", got)
	}

	pending.release()

	want := "stdout:backlog\nstdout:live 1\nstderr:live 2\n"
	if got := target.String(); got != want {
		t.Errorf("target has %q, want %q", got, want)
	}

	// Released, it writes straight through.
	writeToLogWriter(pending, "stdout", []byte("after\n"))
	if got := target.String(); got != want+"stdout:after\n" {
		t.Errorf("target has %q after release, want the passed-through write", got)
	}
}

func TestPendingWriterDropsAnOversizedQueue(t *testing.T) {
	target := &recordingWriter{}
	pending := newPendingWriter(target)

	chunk := strings.Repeat("x", 64*1024)
	for written := 0; written <= maxPendingStreamBytes; written += len(chunk) {
		writeToLogWriter(pending, "stdout", []byte(chunk))
	}
	pending.release()

	got := target.String()
	// Written well past the cap, the queue still flushes about a cap's worth
	// (plus a per-event "stdout:" prefix and the marker).
	if len(got) > 2*maxPendingStreamBytes {
		t.Errorf("queue flushed %d bytes, want around the %d byte cap", len(got), maxPendingStreamBytes)
	}
	if !strings.Contains(got, streamGapMarker) {
		t.Error("a dropped queue must say the stream is not contiguous")
	}
}

func TestUnwrapWriterFindsTheCallersWriter(t *testing.T) {
	target := &recordingWriter{}
	if got := unwrapWriter(newPendingWriter(target)); got != target {
		t.Errorf("unwrapWriter returned %v, want the wrapped writer", got)
	}
	if got := unwrapWriter(target); got != target {
		t.Errorf("unwrapWriter returned %v, want the writer itself", got)
	}
}
