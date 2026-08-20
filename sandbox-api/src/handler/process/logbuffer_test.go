package process

import (
	"strings"
	"testing"
)

func TestLogBufferKeepsOnlyTheTail(t *testing.T) {
	b := &logBuffer{max: 8}

	b.WriteString("0123456789")
	b.WriteString("abc")

	if got, want := b.Len(), 13; got != want {
		t.Errorf("Len() = %d, want %d (every byte written)", got, want)
	}
	if got := b.String(); !strings.HasSuffix(got, "6789abc") {
		t.Errorf("String() = %q, want it to end with the last bytes written", got)
	}
	if !strings.HasPrefix(b.String(), "[... truncated") {
		t.Errorf("String() = %q, want a truncation marker", b.String())
	}
	if got := len(b.buf); got > 8 {
		t.Errorf("buffered %d bytes, want at most 8", got)
	}
}

func TestLogBufferShortOutputIsVerbatim(t *testing.T) {
	b := &logBuffer{max: 8}
	b.WriteString("abc")

	if got, want := b.String(), "abc"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestLogBufferRestoreKeepsTheFileOffset(t *testing.T) {
	b := newLogBuffer()
	b.restore("tail", 5000)

	if got, want := b.Len(), 5000; got != want {
		t.Errorf("Len() = %d, want %d (the offset the log file resumes at)", got, want)
	}

	b.WriteString("more")
	if got, want := b.Len(), 5004; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
}

func TestStreamOffsetFallsBackToSavedOutput(t *testing.T) {
	if got, want := streamOffset(0, "saved output"), len("saved output"); got != want {
		t.Errorf("streamOffset without a count = %d, want %d", got, want)
	}
	if got, want := streamOffset(42, "saved output"), 42; got != want {
		t.Errorf("streamOffset with a count = %d, want %d", got, want)
	}
}
