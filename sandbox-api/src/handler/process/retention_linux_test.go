//go:build linux

package process

import (
	"bytes"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionBoundarySurvivesReopenAndLargerBudget(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "65536")
	path := filepath.Join(t.TempDir(), "log")
	data := bytes.Repeat([]byte{0, 1, 2, 3}, 128*1024)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	capLogFile(path, 1)
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "1048576")
	tail, ok := readLogTail(path, 1048576)
	if !ok || tail != truncationMarker+string(data[len(data)-65536:]) {
		t.Fatalf("reopened tail includes released bytes: len=%d", len(tail))
	}
}

func TestRetentionRecognizesLegacyHoleWithoutDroppingNULs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	data := make([]byte, 3*pageSize)
	data[len(data)-1] = 1
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := punchHole(f, int64(pageSize)); err != nil {
		t.Fatal(err)
	}
	tail, ok := readLogTail(path, int64(len(data)))
	if !ok || tail != truncationMarker+string(data[pageSize:]) {
		t.Fatalf("legacy hole handling changed retained NULs: len=%d", len(tail))
	}
}

func TestRetentionPublishedBeforePunchIsConservative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	data := bytes.Repeat([]byte{0, 1}, 2*pageSize)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := recordRetainedHead(f, int64(pageSize)); err != nil {
		t.Fatal(err)
	}
	tail, ok := readLogTail(path, int64(len(data)))
	if !ok || tail != truncationMarker+string(data[pageSize:]) {
		t.Fatal("published boundary was ignored")
	}
}

func TestRetentionLocksIndependentDescriptorsAndNotJournalOwnership(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096")
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, bytes.Repeat([]byte{1}, 8*pageSize), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := lockLogFile(f, false); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { capLogFile(path, 1); close(done) }()
	select {
	case <-done:
		t.Fatal("cap bypassed reader lock")
	case <-time.After(20 * time.Millisecond):
	}
	unlockLogFile(f)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cap conflicts with journal ownership")
	}
	buf := make([]byte, pageSize)
	read, err := readRetained(f, buf)
	if err != nil || !read.skipped || read.n != pageSize || !bytes.Equal(buf, bytes.Repeat([]byte{1}, pageSize)) {
		t.Fatalf("read=%+v err=%v", read, err)
	}
}

func TestRetentionGapPersistsWithoutStateSnapshot(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096")
	pm, p, _ := structuredFixture(t)
	p.StdoutFile = filepath.Join(t.TempDir(), "source")
	data := bytes.Repeat([]byte{0, 1}, 8*pageSize)
	if err := os.WriteFile(p.StdoutFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p.StdoutFile)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	journal, err := os.OpenFile(p.LogFile, os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	capLogFile(p.StdoutFile, 1)
	buf := make([]byte, 4096)
	for pm.readAndBroadcast(f, buf, p, "stdout", journal) > 0 {
	}
	p.logIncomplete = false
	restoreCollectedOffset(p.stdout, 0)
	if !restoreStructuredPosition(p) || !p.logIncomplete || p.stdout.Len() != len(data) {
		t.Fatal("journal failed to restore gap and absolute cursor")
	}
	w := &structuredCapture{}
	st, _ := journal.Stat()
	if err := replayStructuredLogs(p, w, st.Size()); err != nil {
		t.Fatal(err)
	}
	if len(w.events) == 0 || w.events[0].Type != "truncated" {
		t.Fatal("replay lost durable truncation signal")
	}
}

func TestRetentionRetriesPublishedBoundary(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096")
	path := filepath.Join(t.TempDir(), "log")
	data := bytes.Repeat([]byte{1}, 8*pageSize)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := recordRetainedHead(f, int64(len(data)-4096)); err != nil {
		t.Fatal(err)
	}
	f.Close()
	before := diskUsage(t, path)
	capLogFile(path, 1)
	if after := diskUsage(t, path); after >= before {
		t.Fatalf("published boundary never retried: before=%d after=%d", before, after)
	}
}

func TestNewLogPreservesIntentionalSparseNULs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := files[0].WriteAt([]byte{1}, int64(2*pageSize)); err != nil {
		t.Fatal(err)
	}
	files[0].Close()
	want := make([]byte, 2*pageSize+1)
	want[len(want)-1] = 1
	got, ok := readLogTail(path, int64(len(want)))
	if !ok || !bytes.Equal([]byte(got), want) {
		t.Fatal("new log confused intentional sparse bytes with retention")
	}
}

// Schedule retention between opening a tail and reading it. The released prefix
// must become a gap without discarding the suffix that remains on disk.
func TestLogTailRetainedAfterOpen(t *testing.T) {
	for _, storage := range []string{"xattr", "sidecar"} {
		t.Run(storage, func(t *testing.T) {
			if storage == "sidecar" {
				withoutLogXattrs(t)
			}
			t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "65536")
			data := bytes.Repeat([]byte{'x', 0, '\n', 255}, 256*1024)
			path := filepath.Join(t.TempDir(), "stdout")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			file, truncated, err := openLogTail(path, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if truncated {
				t.Fatal("fresh output was reported as truncated")
			}

			capLogFile(path, 1)
			got, skipped, err := readAtMost(file, int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			if !skipped {
				t.Error("retention was not reported")
			}
			want := data[len(data)-65536:]
			if !bytes.Equal(got, want) {
				t.Fatalf("retained tail changed: got %d bytes, want %d", len(got), len(want))
			}
			fresh, ok := readLogTail(path, int64(len(data)))
			if !ok || fresh != truncationMarker+string(want) {
				t.Fatal("tail response lost retained output or its truncation marker")
			}
		})
	}
}
