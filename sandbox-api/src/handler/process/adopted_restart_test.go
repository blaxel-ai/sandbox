package process

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// newAdoptedProcess builds a process the way LoadState does for one it adopts,
// and installs it in pm.
func newAdoptedProcess(t *testing.T, pm *ProcessManager, pid int, command string, restartCount, maxRestarts int) *ProcessInfo {
	t.Helper()
	t.Setenv("BLAXEL_SCALE_FILE", filepath.Join(t.TempDir(), "scale_to_zero_disable"))
	stdout, stderr, combined := getLogFilePaths("adopted")
	for _, f := range []string{stdout, stderr, combined} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &ProcessInfo{
		PID:              "adopted",
		Name:             "adopted",
		Command:          command,
		ProcessPid:       pid,
		StartedAt:        time.Now().Add(-time.Minute),
		Status:           StatusRunning,
		LogFile:          combined,
		StdoutFile:       stdout,
		StderrFile:       stderr,
		RestartOnFailure: true,
		MaxRestarts:      maxRestarts,
		RestartCount:     restartCount,
		Done:             make(chan struct{}),
		TailDone:         make(chan struct{}),
		Finished:         make(chan struct{}),
		stdout:           newLogBuffer(),
		stderr:           newLogBuffer(),
		logs:             newLogBuffer(),
		logWriters:       make([]io.Writer, 0),
		stopTimeout:      make(chan struct{}),
	}
	pm.mu.Lock()
	pm.processes[p.PID] = p
	pm.mu.Unlock()
	return p
}

// exitedChild starts a child of the test, which the API adopting it is too
// after an upgrade re-exec, and waits for it to exit without reaping it.
func exitedChild(t *testing.T, script string) int {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	cmd := exec.Command("sh", "-c", script)
	cmd.Stdout = w
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w.Close()
	_, _ = io.ReadAll(r)
	pid := cmd.Process.Pid
	waitFor(t, "the child to exit", func() bool { return isZombie(pid) })
	return pid
}

// isZombie waits a moment where there is no /proc to tell: callers only ask
// once the process has closed its descriptors on exit.
func isZombie(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		time.Sleep(200 * time.Millisecond)
		return true
	}
	stat := string(data)
	i := strings.LastIndex(stat, ")")
	return i >= 0 && i+2 < len(stat) && stat[i+2] == 'Z'
}

// orphanPid returns the pid of a process that was never our child and has
// exited, as an adopted process is once the API that started it was killed.
func orphanPid(t *testing.T) int {
	t.Helper()
	out, err := exec.Command("sh", "-c", "sleep 0.1 & echo $!").Output()
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the orphan to exit", func() bool {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		_, err := os.Stat("/proc/" + strconv.Itoa(pid) + "/stat")
		return err == nil && isZombie(pid)
	})
	return pid
}

func waitFinished(t *testing.T, pm *ProcessManager, p *ProcessInfo) ProcessSnapshot {
	t.Helper()
	pm.mu.RLock()
	finished := p.Finished
	pm.mu.RUnlock()
	select {
	case <-finished:
	case <-time.After(15 * time.Second):
		t.Fatal("the process never finished")
	}
	snap, ok := pm.GetProcessSnapshot(p.PID)
	if !ok {
		t.Fatal("process missing")
	}
	return snap
}

func stdoutOf(s ProcessSnapshot) string {
	if s.Stdout == nil {
		return ""
	}
	return *s.Stdout
}

// The restart budget carries over from the run that was adopted, and the
// restarted runs go through waitForRun until it is spent.
func TestAdoptedProcessFailureRestartsWithinPersistedBudget(t *testing.T) {
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, exitedChild(t, "exit 3"), "exit 3", 1, 2)

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusFailed || snap.ExitCode != 3 || snap.RestartCount != 2 {
		t.Fatalf("got status %q exit %d restarts %d, want failed/3/2", snap.Status, snap.ExitCode, snap.RestartCount)
	}
	out := stdoutOf(snap)
	if !strings.Contains(out, "Process failed with exit code 3. Attempting restart 2/2") {
		t.Fatalf("missing restart notice: %q", out)
	}
	if strings.Contains(out, "Attempting restart 3/2") {
		t.Fatalf("restarted past MaxRestarts: %q", out)
	}
}

func TestAdoptedProcessExitingCleanlyIsNotRestarted(t *testing.T) {
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, exitedChild(t, "exit 0"), "exit 0", 0, 3)

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusCompleted || snap.ExitCode != 0 || snap.RestartCount != 0 {
		t.Fatalf("got status %q exit %d restarts %d, want completed/0/0", snap.Status, snap.ExitCode, snap.RestartCount)
	}
	if strings.Contains(stdoutOf(snap), "Attempting restart") {
		t.Fatalf("a clean exit was restarted: %q", stdoutOf(snap))
	}
}

// Once the API that spawned it is gone, the exit code of a process cannot be
// reaped. That is not a success: a restartOnFailure process is brought back.
func TestAdoptedProcessWithUnknownExitIsRestarted(t *testing.T) {
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, orphanPid(t), "exit 0", 0, 3)

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusCompleted || snap.RestartCount != 1 {
		t.Fatalf("got status %q restarts %d, want the restarted run completed after 1 restart", snap.Status, snap.RestartCount)
	}
	if !strings.Contains(stdoutOf(snap), "Process failed with exit code -1. Attempting restart 1/3") {
		t.Fatalf("missing restart notice: %q", stdoutOf(snap))
	}
}

func TestAdoptedProcessWithUnknownExitAndNoRestartIsFailed(t *testing.T) {
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, orphanPid(t), "exit 0", 0, 3)
	p.RestartOnFailure = false

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusFailed || snap.ExitCode != -1 || snap.RestartCount != 0 {
		t.Fatalf("got status %q exit %d restarts %d, want failed/-1/0", snap.Status, snap.ExitCode, snap.RestartCount)
	}
}

func TestStoppedAdoptedProcessIsNotRestarted(t *testing.T) {
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, orphanPid(t), "exit 0", 0, 3)
	p.terminationRequested = StatusStopped

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusStopped || snap.RestartCount != 0 {
		t.Fatalf("got status %q restarts %d, want stopped/0", snap.Status, snap.RestartCount)
	}
}

// The binary an upgrade vets adopts the same processes as the instance being
// upgraded, which restarts them itself.
func TestValidationInstanceDoesNotRestartAdoptedProcess(t *testing.T) {
	validationInstance.Store(true)
	t.Cleanup(func() { validationInstance.Store(false) })
	pm := newStdinTestManager(t)
	p := newAdoptedProcess(t, pm, orphanPid(t), "exit 0", 0, 3)

	pm.endAdoptedRun(p)
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusFailed || snap.RestartCount != 0 {
		t.Fatalf("got status %q restarts %d, want failed/0", snap.Status, snap.RestartCount)
	}
}

func writeState(t *testing.T, procs ...ProcessState) {
	t.Helper()
	stateFile := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("SANDBOX_STATE_FILE", stateFile)
	t.Setenv("BLAXEL_SCALE_FILE", filepath.Join(t.TempDir(), "scale_to_zero_disable"))
	state := ManagerState{Version: 1, SavedAt: time.Now(), Processes: map[string]ProcessState{}}
	for _, p := range procs {
		state.Processes[p.PID] = p
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func savedRunningState(t *testing.T, restartCount, maxRestarts int) ProcessState {
	stdout, stderr, combined := getLogFilePaths("saved")
	for _, f := range []string{stdout, stderr, combined} {
		if err := os.WriteFile(f, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return ProcessState{
		PID:              "saved",
		Name:             "saved",
		Command:          "exit 0",
		ProcessPid:       orphanPid(t),
		StartedAt:        time.Now().Add(-time.Minute),
		Status:           StatusRunning,
		LogFile:          combined,
		StdoutFile:       stdout,
		StderrFile:       stderr,
		RestartOnFailure: true,
		MaxRestarts:      maxRestarts,
		RestartCount:     restartCount,
		KeepAlive:        true,
	}
}

// A restartOnFailure process that died while no API watched it is restarted
// on load, its budget carried over, and its keep-alive hold balanced.
func TestProcessDeadAtLoadIsRestarted(t *testing.T) {
	pm := newStdinTestManager(t)
	writeState(t, savedRunningState(t, 1, 3))
	if err := pm.LoadState(); err != nil {
		t.Fatal(err)
	}
	p := pm.processes["saved"]
	snap := waitFinished(t, pm, p)

	if snap.Status != StatusCompleted || snap.RestartCount != 2 {
		t.Fatalf("got status %q restarts %d, want the restarted run completed after 2 restarts", snap.Status, snap.RestartCount)
	}
	if !strings.Contains(stdoutOf(snap), "Process failed with exit code -1. Attempting restart 2/3") {
		t.Fatalf("missing restart notice: %q", stdoutOf(snap))
	}
	if content, err := os.ReadFile(os.Getenv("BLAXEL_SCALE_FILE")); err != nil || string(content) != "-" {
		t.Fatalf("keep-alive hold not released after the restarted run, scale file %q (%v)", content, err)
	}
}

func TestProcessDeadAtLoadWithSpentBudgetStaysFailed(t *testing.T) {
	pm := newStdinTestManager(t)
	writeState(t, savedRunningState(t, 3, 3))
	if err := pm.LoadState(); err != nil {
		t.Fatal(err)
	}
	snap := waitFinished(t, pm, pm.processes["saved"])

	if snap.Status != StatusFailed || snap.ExitCode != -1 || snap.RestartCount != 3 {
		t.Fatalf("got status %q exit %d restarts %d, want failed/-1/3", snap.Status, snap.ExitCode, snap.RestartCount)
	}
}

func TestStructuredProcessDeadAtLoadIsRestartedWithOneNotice(t *testing.T) {
	pm := newStdinTestManager(t)
	state := savedRunningState(t, 0, 1)
	state.LogFormat = structuredLogFormat
	state.KeepAlive = false
	state.Command = "printf second"
	if err := os.WriteFile(state.StdoutFile, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeState(t, state)
	if err := pm.LoadState(); err != nil {
		t.Fatal(err)
	}
	p := pm.processes["saved"]
	snap := waitFinished(t, pm, p)
	if snap.Status != StatusCompleted || snap.RestartCount != 1 {
		t.Fatalf("got status %q restarts %d, want completed/1", snap.Status, snap.RestartCount)
	}

	w := &structuredCapture{}
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	var out string
	notices := 0
	for _, e := range w.events {
		if e.Type == "stdout" {
			out += e.Data
		}
		if e.Type == "restart" && strings.Contains(e.Data, "Attempting restart 1/1") {
			notices++
		}
	}
	if out != "firstsecond" || notices != 1 {
		t.Fatalf("incorrect replay: %#v", w.events)
	}
}

// The monitor path end to end: LoadState adopts a live child, which then
// fails with a real exit code. Adoption needs /proc.
func TestLoadStateAdoptedProcessFailureIsRestarted(t *testing.T) {
	if _, err := os.Stat("/proc/self/stat"); err != nil {
		t.Skip("adoption checks liveness through /proc")
	}
	pm := newStdinTestManager(t)
	command := "sleep 1; exit 3"
	cmd := exec.Command("sh", "-c", command)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	state := savedRunningState(t, 0, 1)
	state.ProcessPid = cmd.Process.Pid
	state.Command = command
	state.KeepAlive = false
	writeState(t, state)
	if err := pm.LoadState(); err != nil {
		t.Fatal(err)
	}
	if snap, _ := pm.GetProcessSnapshot("saved"); snap.Status != StatusRunning {
		t.Fatalf("expected the live child to be adopted, got %q", snap.Status)
	}
	snap := waitFinished(t, pm, pm.processes["saved"])

	if snap.Status != StatusFailed || snap.ExitCode != 3 || snap.RestartCount != 1 {
		t.Fatalf("got status %q exit %d restarts %d, want failed/3/1", snap.Status, snap.ExitCode, snap.RestartCount)
	}
	if !strings.Contains(stdoutOf(snap), "Process failed with exit code 3. Attempting restart 1/1") {
		t.Fatalf("missing restart notice: %q", stdoutOf(snap))
	}
}
