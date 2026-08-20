package process

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// diskUsage is what the file costs the filesystem, which is what a punched-out
// head releases - the file's apparent size stays the same.
func diskUsage(t *testing.T, path string) int64 {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Stat(path, &stat); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return stat.Blocks * 512
}

func TestCapLogFileReleasesTheHeadAndKeepsTheTail(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(64*1024))

	path := filepath.Join(t.TempDir(), "big.log")
	// A recognisable tail after ~1 MiB of filler.
	filler := strings.Repeat("x", 1024*1024)
	if err := os.WriteFile(path, []byte(filler+"THE TAIL\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := diskUsage(t, path)

	capLogFile(path)

	if after := diskUsage(t, path); after >= before {
		t.Errorf("disk usage %d bytes after capping, want less than %d", after, before)
	}
	if usage, max := diskUsage(t, path), int64(256*1024); usage > max {
		t.Errorf("disk usage %d bytes after capping, want at most %d", usage, max)
	}

	// Every offset in the file has to stay where the workload's own fd expects
	// it, so the file keeps its apparent size.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Size(), int64(len(filler)+len("THE TAIL\n")); got != want {
		t.Errorf("size = %d, want %d (offsets must not move)", got, want)
	}

	tail, ok := readLogTail(path, 64*1024)
	if !ok {
		t.Fatal("readLogTail failed")
	}
	if !strings.HasSuffix(tail, "THE TAIL\n") {
		t.Error("the newest output was not kept")
	}
	if !strings.HasPrefix(tail, truncationMarker) {
		t.Errorf("tail = %.80q, want it to say the output was truncated", tail)
	}
	if strings.ContainsRune(tail, 0) {
		t.Error("the released head is being served as NUL bytes")
	}
}

func TestCapLogFileLeavesASmallFileAlone(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(64*1024))

	path := filepath.Join(t.TempDir(), "small.log")
	if err := os.WriteFile(path, []byte("hello\n"), 0644); err != nil {
		t.Fatal(err)
	}

	capLogFile(path)

	if got, ok := readLogTail(path, 64*1024); !ok || got != "hello\n" {
		t.Errorf("readLogTail = %q (ok=%v), want %q verbatim", got, ok, "hello\n")
	}
}

func TestCapLogFileDisabled(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "0")

	path := filepath.Join(t.TempDir(), "uncapped.log")
	content := strings.Repeat("y", 512*1024)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	before := diskUsage(t, path)

	capLogFile(path)

	if after := diskUsage(t, path); after != before {
		t.Errorf("disk usage changed from %d to %d with the cap disabled", before, after)
	}
}

func TestReadLogTailBoundsWhatItReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("z", 200*1024)), 0644); err != nil {
		t.Fatal(err)
	}

	tail, ok := readLogTail(path, 8*1024)
	if !ok {
		t.Fatal("readLogTail failed")
	}
	if got, max := len(tail), 8*1024+len(truncationMarker); got > max {
		t.Errorf("read %d bytes, want at most %d", got, max)
	}
	if !strings.HasPrefix(tail, truncationMarker) {
		t.Error("a tail read of a bigger file must say it is truncated")
	}
}

func TestProcessOutputComesFromDiskAndTheTailIsBounded(t *testing.T) {
	pm := GetProcessManager()

	done := make(chan *ProcessInfo, 1)
	// ~200 KiB of output: more than the in-memory buffer holds, so it can only
	// be returned in full if it is read from the log file.
	command := `for i in $(seq 1 200); do for j in $(seq 1 1024); do printf 'Z'; done; done`
	pid, err := pm.StartProcess(command, "", nil, false, 0, false, 0, func(p *ProcessInfo) { done <- p })
	if err != nil {
		t.Fatalf("Error starting process: %v", err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Timeout waiting for process to complete")
	}

	full, err := pm.GetProcessOutput(pid)
	if err != nil {
		t.Fatalf("GetProcessOutput: %v", err)
	}
	if got, want := strings.Count(full.Stdout, "Z"), 200*1024; got != want {
		t.Errorf("GetProcessOutput returned %d bytes of output, want %d (it should read the log file)", got, want)
	}

	tail, err := pm.GetProcessOutputTail(pid, 4*1024)
	if err != nil {
		t.Fatalf("GetProcessOutputTail: %v", err)
	}
	if got, max := len(tail.Stdout), 4*1024+len(truncationMarker); got > max {
		t.Errorf("GetProcessOutputTail returned %d bytes, want at most %d", got, max)
	}
	if !strings.HasPrefix(tail.Stdout, truncationMarker) {
		t.Error("a truncated tail must say so")
	}
}

func TestReadLogsSinceIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "grew.log")
	// What a chatty process wrote while the API was down.
	content := strings.Repeat("q", 4*1024*1024) + "END"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	got, end := readLogsSince(path, 0)

	if len(got) > maxInMemoryLogBytes {
		t.Errorf("read %d bytes into memory, want at most %d", len(got), maxInMemoryLogBytes)
	}
	if !strings.HasSuffix(string(got), "END") {
		t.Error("the newest output was not the part that was read")
	}
	// The offset has to account for what the bounded read skipped, or the next
	// restart re-reads output that is already accounted for.
	if got, want := end, len(content); got != want {
		t.Errorf("end offset = %d, want %d", got, want)
	}
}

func TestReadLogsSinceNothingNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "done.log")
	if err := os.WriteFile(path, []byte("all of it\n"), 0644); err != nil {
		t.Fatal(err)
	}

	got, end := readLogsSince(path, len("all of it\n"))

	if len(got) != 0 {
		t.Errorf("read %q, want nothing new", got)
	}
	if want := len("all of it\n"); end != want {
		t.Errorf("end offset = %d, want %d", end, want)
	}
}

func TestReadLogTailMissingFile(t *testing.T) {
	if _, ok := readLogTail(filepath.Join(t.TempDir(), "absent.log"), 1024); ok {
		t.Error("readLogTail reported success for a file that does not exist")
	}
	if _, ok := readLogTail("", 1024); ok {
		t.Error("readLogTail reported success for an empty path")
	}
}
