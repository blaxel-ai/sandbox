package process

import (
	"io"
	"os"
	"strings"
	"sync"
	"testing"
)

// captureWriter stands in for an attached log stream.
type captureWriter struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (c *captureWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureWriter) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// newTestProcess builds the minimum ProcessInfo readAndBroadcast touches.
func newTestProcess(w *captureWriter) *ProcessInfo {
	return &ProcessInfo{
		PID:        "1",
		Name:       "test",
		stdout:     newLogBuffer(),
		stderr:     newLogBuffer(),
		logs:       newLogBuffer(),
		logWriters: []io.Writer{w},
	}
}

// readAll feeds content through readAndBroadcast in bufSize-byte reads, the way
// the tailer does, and returns what the attached stream saw.
func readAll(t *testing.T, content string, bufSize int) (string, string) {
	t.Helper()
	dir := t.TempDir()
	src := dir + "/out.log"
	if err := os.WriteFile(src, []byte(content), 0o644); err != nil {
		t.Fatalf("writing source: %v", err)
	}
	file, err := os.Open(src)
	if err != nil {
		t.Fatalf("opening source: %v", err)
	}
	defer file.Close()

	combinedPath := dir + "/combined.log"
	combined, err := os.Create(combinedPath)
	if err != nil {
		t.Fatalf("creating combined log: %v", err)
	}
	defer combined.Close()

	w := &captureWriter{}
	proc := newTestProcess(w)
	pm := NewProcessManager()
	buf := make([]byte, bufSize)
	for {
		if n := pm.readAndBroadcast(file, buf, proc, "stdout", combined); n == 0 {
			break
		}
	}
	onDisk, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatalf("reading combined log: %v", err)
	}
	return w.String(), string(onDisk)
}

// A read is a fixed-size window, not a line. Tagging the chunk rather than its
// line starts spliced "stdout:" into the middle of any line longer than the
// buffer, which corrupted it for every consumer and for the log file the
// reconnect backfill is replayed from.
func TestLongLineIsTaggedOnlyAtItsStart(t *testing.T) {
	line := `{"type":"tool","output":"` + strings.Repeat("x", 9000) + `"}` + "\n"

	streamed, onDisk := readAll(t, line, 4096)

	for name, got := range map[string]string{"stream": streamed, "combined log": onDisk} {
		if n := strings.Count(got, "stdout:"); n != 1 {
			t.Errorf("%s: expected exactly one tag, got %d", name, n)
		}
		if body := strings.TrimPrefix(got, "stdout:"); body != line {
			t.Errorf("%s: line altered in transit (len %d, want %d)", name, len(body), len(line))
		}
	}
}

func TestEveryWholeLineIsTagged(t *testing.T) {
	content := "one\ntwo\nthree\n"

	streamed, onDisk := readAll(t, content, 4096)

	want := "stdout:one\nstdout:two\nstdout:three\n"
	if streamed != want {
		t.Errorf("stream: got %q, want %q", streamed, want)
	}
	if onDisk != want {
		t.Errorf("combined log: got %q, want %q", onDisk, want)
	}
}

// A read boundary landing exactly on a newline must start the next line fresh.
func TestBoundaryOnNewlineStartsNextLineTagged(t *testing.T) {
	// "abc\n" is exactly 4 bytes, so with a 4-byte buffer each read ends on \n.
	streamed, _ := readAll(t, "abc\ndef\n", 4)

	want := "stdout:abc\nstdout:def\n"
	if streamed != want {
		t.Errorf("got %q, want %q", streamed, want)
	}
}

// Output with no trailing newline still arrives, tagged once.
func TestTrailingPartialLineIsTaggedOnce(t *testing.T) {
	streamed, _ := readAll(t, "complete\nno-newline-here", 4)

	want := "stdout:complete\nstdout:no-newline-here"
	if streamed != want {
		t.Errorf("got %q, want %q", streamed, want)
	}
}
