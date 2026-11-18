package process

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// Exit reasons. A process result carries one when its exit code alone does not
// say why it ended. The status and exit code are unchanged: a reason only adds
// the cause.
const (
	// ExitReasonOOMKilled: the kernel's out-of-memory killer ended the process,
	// or the command its shell ran last (the shell then exits with 137). The
	// sandbox ran out of memory.
	ExitReasonOOMKilled = "oom_killed"
	// ExitReasonDiskFull: the process failed while the disk that holds its
	// output had no space left, so its output, error message included, may be
	// missing.
	ExitReasonDiskFull = "disk_full"
)

// vmstatPath holds the kernel's event counters; a variable so tests can point
// it at a fixture.
var vmstatPath = "/proc/vmstat"

var oomKillField = []byte("\noom_kill ")

// oomKillCount is how many processes the kernel's OOM killer has killed since
// boot (oom_kill in /proc/vmstat, Linux 4.13 and later), or -1 when that cannot
// be read, as on macOS. The kernel counts a kill before it sends the SIGKILL,
// so a count read after the victim's exit always includes it.
func oomKillCount() int64 {
	data, err := os.ReadFile(vmstatPath)
	if err != nil {
		return -1
	}
	// oom_kill is never the first line, so the leading newline keeps a longer
	// name such as oom_kill_foo from matching.
	_, rest, found := bytes.Cut(data, oomKillField)
	if !found {
		return -1
	}
	value, _, _ := bytes.Cut(rest, []byte("\n"))
	n, err := strconv.ParseInt(string(value), 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// diskFull reports whether the filesystem holding path has no space left for
// an unprivileged write. With none, a process' writes to its output files fail,
// so not even its error message reaches the logs.
func diskFull(path string) bool {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return false
	}
	return st.Bavail == 0
}

// Seams for tests: the real counters are the kernel's and the disk's.
var (
	readOOMKillCount = oomKillCount
	outputDiskFull   = diskFull
)

// killedBySIGKILL reports whether a run ended by SIGKILL, either itself or, for
// a shell, the command it ran last: shells exit with 128+9 then.
func killedBySIGKILL(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		return false
	}
	if status.Signaled() {
		return status.Signal() == syscall.SIGKILL
	}
	return status.Exited() && status.ExitStatus() == 128+int(syscall.SIGKILL)
}

// classifyExit explains a failed run (waitErr from cmd.Wait) whose exit the API
// did not ask for. oomKillsAtStart is oomKillCount() taken just before the run
// started, or -1. oomKills is how many processes the kernel OOM-killed in the
// sandbox during the run, this one or any other: a test runner whose worker
// was killed can fail with an ordinary exit code. Only a run that died of
// SIGKILL while the count rose is reported as oom_killed itself.
//
// It reads the kernel's counter and the output disk's free space, so it only
// runs for failed runs, outside the manager lock.
func classifyExit(waitErr error, oomKillsAtStart int64, outputFile string) (reason string, oomKills int) {
	if waitErr == nil {
		return "", 0
	}
	if oomKillsAtStart >= 0 {
		if now := readOOMKillCount(); now > oomKillsAtStart {
			oomKills = int(now - oomKillsAtStart)
			if killedBySIGKILL(waitErr) {
				return ExitReasonOOMKilled, oomKills
			}
		}
	}
	if outputFile != "" && outputDiskFull(filepath.Dir(outputFile)) {
		return ExitReasonDiskFull, oomKills
	}
	return "", oomKills
}
