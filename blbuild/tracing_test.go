package main

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
)

// The lambda builds the traceparent by hashing the execution ARN, so its
// parent-span-id names a span nothing ever emits. Adopting it as a parent is
// what put "this trace has missing spans" on every build.
func TestTraceIDFromTraceparent(t *testing.T) {
	const tp = "00-b3f1c2d4e5a60718293a4b5c6d7e8f90-1122334455667788-01"

	id, ok := traceIDFrom(tp)
	if !ok {
		t.Fatal("the trace id was not extracted")
	}
	if id.String() != "b3f1c2d4e5a60718293a4b5c6d7e8f90" {
		t.Fatalf("trace id = %s", id)
	}
}

func TestTraceIDFromRejectsGarbage(t *testing.T) {
	for _, bad := range []string{
		"",
		"not-a-traceparent",
		"00-nothex-1122334455667788-01",
		"00-00000000000000000000000000000000-1122334455667788-01", // all-zero is invalid
	} {
		if _, ok := traceIDFrom(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

// The root span keeps the execution's trace id, so a build stays findable from
// its execution without being parented to a span that does not exist.
func TestExecutionIDGeneratorKeepsTheTraceID(t *testing.T) {
	want, _ := trace.TraceIDFromHex("b3f1c2d4e5a60718293a4b5c6d7e8f90")
	g := &executionIDGenerator{traceID: want}

	gotTrace, first := g.NewIDs(context.Background())
	if gotTrace != want {
		t.Errorf("trace id = %s, want %s", gotTrace, want)
	}
	if !first.IsValid() {
		t.Error("the root span got an invalid span id")
	}

	// Span ids must not repeat, or the waterfall collapses onto itself.
	_, second := g.NewIDs(context.Background())
	child := g.NewSpanID(context.Background(), want)
	if first == second || first == child || second == child {
		t.Errorf("span ids repeat: %s %s %s", first, second, child)
	}
	if !child.IsValid() {
		t.Error("a child got an invalid span id")
	}
}
