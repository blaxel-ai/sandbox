package process

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

	capLogFile(path, maxLogFile())

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

	capLogFile(path, maxLogFile())

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

	capLogFile(path, maxLogFile())

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

// A punched-out head has no newline in it, so a scanner starting at offset 0
// would see it as one huge line and give up before reaching the real output.
func TestOpenLogTailStartsPastAReleasedHead(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(64*1024))

	path := filepath.Join(t.TempDir(), "combined.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("stdout:x\n", 400*1024)+"stdout:THE TAIL\n"), 0644); err != nil {
		t.Fatal(err)
	}
	capLogFile(path, maxLogFile())

	file, truncated, err := openLogTail(path, maxLogFile())
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if !truncated {
		t.Error("openLogTail must report a capped file as truncated")
	}

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)
	lines, last := 0, ""
	for scanner.Scan() {
		lines++
		last = scanner.Text()
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scanning the tail failed: %v", err)
	}
	if lines == 0 || last != "stdout:THE TAIL" {
		t.Errorf("scanned %d lines, last = %q, want the tail of the file", lines, last)
	}
}

func TestProcessOutputComesFromDiskAndTheTailIsBounded(t *testing.T) {
	pm := GetProcessManager()

	done := make(chan *ProcessInfo, 1)
	// ~200 KiB of output: more than the in-memory buffer holds, so it can only
	// be returned in full if it is read from the log file.
	command := `for i in $(seq 1 200); do for j in $(seq 1 1024); do printf 'Z'; done; done`
	pid, err := pm.StartProcess(command, "", nil, false, 0, false, 0, false, func(p *ProcessInfo) { done <- p })
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

// The log files live on the tmpfs root, so the budget is memory the workload
// cannot have back: it is split between every log file still on disk.
func TestFileShareSplitsTheBudgetBetweenFiles(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(90*1024*1024))
	big := int64(1 << 40)

	if got, want := fileShare([]int64{big, big, big}), int64(30*1024*1024); got != want {
		t.Errorf("three full files get %d bytes each, want %d", got, want)
	}
	if got, want := fileShare(slices.Repeat([]int64{big}, 30)), int64(3*1024*1024); got != want {
		t.Errorf("thirty full files get %d bytes each, want %d", got, want)
	}
	// Nothing to cap yet: every file may grow up to the ceiling.
	if got, want := fileShare([]int64{1024, 2048}), maxLogFile(); got != want {
		t.Errorf("small files get %d, want the %d ceiling", got, want)
	}
	// The share never grows past what one file may hold on its own.
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(1024*1024))
	if got, want := fileShare([]int64{big}), int64(1024*1024); got != want {
		t.Errorf("per-file share = %d, want the %d ceiling", got, want)
	}
}

// A sandbox that ran hundreds of short commands used to squeeze a long-lived
// process down to the floor, although the finished ones held almost nothing.
func TestFileShareIgnoresWhatSmallFilesDoNotUse(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(200*1024*1024))
	sizes := slices.Repeat([]int64{512}, 300*3) // 300 finished processes
	sizes = append(sizes, 2*1024*1024, 0, 2*1024*1024)

	if got := fileShare(sizes); got < 2*1024*1024 {
		t.Errorf("share = %d, want the busy process' 2 MiB kept whole", got)
	}
}

func TestFileShareFloorAndDisabling(t *testing.T) {
	// A thousand full files would divide the budget into nothing; each is left
	// enough output to be worth reading instead.
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(16*1024*1024))
	if got, want := fileShare(slices.Repeat([]int64{1 << 30}, 1000)), int64(minLogFileBytes); got != want {
		t.Errorf("per-file share = %d, want the %d floor", got, want)
	}

	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "0")
	if got := fileShare([]int64{1 << 30}); got != 0 {
		t.Errorf("per-file share = %d, want capping disabled", got)
	}
}

func TestLogBudgetIsAShareOfTheGuestMemory(t *testing.T) {
	total := memTotalBytes()
	if total <= 0 {
		t.Skip("cannot read MemTotal on this host")
	}

	if got, want := logBudget(), total*logBudgetPercent/100; got != want {
		t.Errorf("log budget = %d, want %d (%d%% of MemTotal)", got, want, logBudgetPercent)
	}

	t.Setenv("SANDBOX_MAX_LOG_PERCENT", "25")
	if got, want := logBudget(), total*25/100; got != want {
		t.Errorf("log budget = %d, want %d (25%% of MemTotal)", got, want)
	}

	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(7*1024*1024))
	if got, want := logBudget(), int64(7*1024*1024); got != want {
		t.Errorf("log budget = %d, want the %d override", got, want)
	}
}

func TestCapLogFilesShrinksFilesNobodyIsTailing(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(3*minLogFileBytes))
	pm := NewProcessManager()

	dir := t.TempDir()
	path := filepath.Join(dir, "exited.stdout.log")
	if err := os.WriteFile(path, []byte(strings.Repeat("w", 4*1024*1024)+"THE TAIL\n"), 0644); err != nil {
		t.Fatal(err)
	}
	before := diskUsage(t, path)

	pm.mu.Lock()
	pm.processes["exited"] = &ProcessInfo{PID: "exited", StdoutFile: path}
	pm.mu.Unlock()

	pm.CapLogFiles()

	if after := diskUsage(t, path); after >= before {
		t.Errorf("disk usage %d bytes, want less than %d: an exited process' output is still costing the guest memory", after, before)
	}
	tail, ok := readLogTail(path, maxLogFile())
	if !ok || !strings.HasSuffix(tail, "THE TAIL\n") {
		t.Error("the newest output was not kept")
	}
}

func TestCapLogFilesKeepsABusyProcessNextToManyFinishedOnes(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(16*1024*1024))
	pm := NewProcessManager()
	dir := t.TempDir()

	pm.mu.Lock()
	for i := 0; i < 300; i++ {
		path := filepath.Join(dir, fmt.Sprintf("done-%d.log", i))
		if err := os.WriteFile(path, []byte("ok\n"), 0644); err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprint(i)
		pm.processes[id] = &ProcessInfo{PID: id, LogFile: path}
	}
	busy := filepath.Join(dir, "busy.log")
	if err := os.WriteFile(busy, []byte(strings.Repeat("e", 1024*1024)), 0644); err != nil {
		t.Fatal(err)
	}
	pm.processes["busy"] = &ProcessInfo{PID: "busy", LogFile: busy}
	pm.mu.Unlock()

	share := pm.CapLogFiles()
	if share < 1024*1024 {
		t.Errorf("share = %d, want the busy process' 1 MiB kept whole", share)
	}
	if got := pm.currentLogShare(); got != share {
		t.Errorf("cached share = %d, want %d", got, share)
	}
	if tail, ok := readLogTail(busy, maxLogFile()); !ok || len(tail) != 1024*1024 {
		t.Errorf("busy log lost output: %d bytes left", len(tail))
	}
}

// A released head keeps the file's apparent size; only what is still resident
// may count against the budget, or finished processes whose output was already
// capped keep squeezing the running ones.
func TestCapLogFilesBudgetsReleasedFilesByWhatTheyStillHold(t *testing.T) {
	t.Setenv("SANDBOX_MAX_LOG_BYTES_TOTAL", fmt.Sprint(100*1024*1024))
	pm := NewProcessManager()
	dir := t.TempDir()

	pm.mu.Lock()
	for i := 0; i < 300; i++ {
		path := filepath.Join(dir, fmt.Sprintf("done-%d.log", i))
		if err := os.WriteFile(path, []byte(strings.Repeat("d", 1024*1024)), 0644); err != nil {
			t.Fatal(err)
		}
		capLogFile(path, minLogFileBytes)
		if i == 0 && diskUsage(t, path) >= 1024*1024 {
			pm.mu.Unlock()
			t.Skip("this filesystem cannot release a file's head")
		}
		id := fmt.Sprint(i)
		pm.processes[id] = &ProcessInfo{PID: id, LogFile: path}
	}
	busy := filepath.Join(dir, "busy.log")
	if err := os.WriteFile(busy, []byte(strings.Repeat("e", 1024*1024)), 0644); err != nil {
		t.Fatal(err)
	}
	pm.processes["busy"] = &ProcessInfo{PID: "busy", LogFile: busy}
	pm.mu.Unlock()

	if share := pm.CapLogFiles(); share < 1024*1024 {
		t.Errorf("share = %d, want the busy process' 1 MiB kept whole", share)
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
