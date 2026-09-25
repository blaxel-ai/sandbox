package process

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newIngestProcess is a process whose log files exist but which nothing is
// running, so a chunk can be pushed through the ingestion path directly.
func newIngestProcess(t *testing.T) (*ProcessInfo, string) {
	t.Helper()
	dir := t.TempDir()
	combinedPath := filepath.Join(dir, "combined.log")

	return &ProcessInfo{
		PID:    "ingest",
		Name:   "ingest",
		stdout: newLogBuffer(),
		stderr: newLogBuffer(),
		logs:   newLogBuffer(),
	}, combinedPath
}

// The tailer runs on every chunk of every process' output, so it must not hold
// copies of what it reads: it used to convert each chunk to a string and split
// it into a slice of lines twice over, which is what grew the API's heap until
// the kernel OOM-killed it.
func TestIngestingOutputDoesNotCopyTheChunk(t *testing.T) {
	// The telemetry entries themselves are not what this measures, and 20 runs of
	// them would drown the test output.
	disableProcessLogging = true
	defer func() { disableProcessLogging = false }()

	proc, combinedPath := newIngestProcess(t)
	combined, err := os.OpenFile(combinedPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer combined.Close()

	pm := NewProcessManager()
	source := filepath.Join(t.TempDir(), "stdout.log")
	// One megabyte with no newline in it: the shape of output that used to be
	// held whole, several times over, as one line.
	chunk := strings.Repeat("a", 1024*1024)
	if err := os.WriteFile(source, []byte(chunk), 0644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	buf := make([]byte, len(chunk))
	allocated := testing.AllocsPerRun(20, func() {
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			t.Fatal(err)
		}
		pm.readAndBroadcast(file, buf, proc, "stdout", combined)
	})

	// A handful of allocations for the log entry is fine; a copy of the chunk
	// per pass is not, and would show as megabytes of garbage per chunk.
	if allocated > 64 {
		t.Errorf("%v allocations per chunk, want a bounded few", allocated)
	}

	// The in-memory buffers keep only their tail, whatever the chunk size.
	if got, max := len(proc.stdout.buf), maxInMemoryLog(); got > max {
		t.Errorf("stdout buffer holds %d bytes, want at most %d", got, max)
	}
	if got, max := len(proc.logs.buf), maxInMemoryLog(); got > max {
		t.Errorf("combined buffer holds %d bytes, want at most %d", got, max)
	}
}

func TestIngestingOutputKeepsTheCombinedLogPrefixed(t *testing.T) {
	proc, combinedPath := newIngestProcess(t)
	combined, err := os.OpenFile(combinedPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		t.Fatal(err)
	}
	defer combined.Close()

	pm := NewProcessManager()
	source := filepath.Join(t.TempDir(), "stderr.log")
	if err := os.WriteFile(source, []byte("first\nsecond\nno newline"), 0644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	pm.readAndBroadcast(file, make([]byte, 4096), proc, "stderr", combined)

	content, err := os.ReadFile(combinedPath)
	if err != nil {
		t.Fatal(err)
	}
	want := "stderr:first\nstderr:second\nstderr:no newline"
	if got := string(content); got != want {
		t.Errorf("combined log = %q, want %q", got, want)
	}
}
