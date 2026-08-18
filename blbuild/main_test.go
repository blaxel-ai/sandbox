package main

import "testing"

// os.Exit runs no deferred function, and the tracer is flushed from one. Before
// this, every failed build lost the spans that explained the failure: they were
// still in the batch queue when the process died.
func TestFlushBeforeExit(t *testing.T) {
	t.Cleanup(func() { flushTraces = nil })

	flushed := 0
	flushTraces = func() { flushed++ }
	flushBeforeExit()
	if flushed != 1 {
		t.Fatalf("the failure path flushed %d times, want 1", flushed)
	}

	// A build that never got tracing up must still be able to fail.
	flushTraces = nil
	flushBeforeExit()
}
