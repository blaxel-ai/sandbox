package process

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestOOMKillCountReadsVmstat(t *testing.T) {
	dir := t.TempDir()
	original := vmstatPath
	t.Cleanup(func() { vmstatPath = original })

	vmstatPath = filepath.Join(dir, "vmstat")
	if err := os.WriteFile(vmstatPath, []byte("nr_free_pages 1\noom_kill_x 9\noom_kill 7\nnr_foo 3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := oomKillCount(); got != 7 {
		t.Errorf("oomKillCount() = %d, want 7", got)
	}

	if err := os.WriteFile(vmstatPath, []byte("nr_free_pages 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := oomKillCount(); got != -1 {
		t.Errorf("oomKillCount() without the counter = %d, want -1", got)
	}

	vmstatPath = filepath.Join(dir, "absent")
	if got := oomKillCount(); got != -1 {
		t.Errorf("oomKillCount() without the file = %d, want -1", got)
	}
}

// runErr runs a shell snippet and returns what cmd.Wait reported.
func runErr(t *testing.T, script string) error {
	t.Helper()
	err := exec.Command("sh", "-c", script).Run()
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("sh -c %q: %v", script, err)
	}
	return err
}

func TestKilledBySIGKILL(t *testing.T) {
	for _, tc := range []struct {
		script string
		want   bool
	}{
		{"kill -9 $$", true},   // the process itself
		{"exit 137", true},     // a shell whose last command was SIGKILLed
		{"kill -15 $$", false}, // another signal
		{"exit 1", false},
	} {
		if got := killedBySIGKILL(runErr(t, tc.script)); got != tc.want {
			t.Errorf("%q: killedBySIGKILL = %v, want %v", tc.script, got, tc.want)
		}
	}
	if killedBySIGKILL(nil) || killedBySIGKILL(errors.New("not an exit")) {
		t.Error("a run that did not exit by a signal was reported as SIGKILLed")
	}
}

// fakeExitSignals replaces the kernel's OOM counter and the disk check for one
// test: the counter returns oomNow, the disk is full when full is set.
func fakeExitSignals(t *testing.T, oomNow *atomic.Int64, full *atomic.Bool) {
	t.Helper()
	readCount, readFull := readOOMKillCount, outputDiskFull
	t.Cleanup(func() { readOOMKillCount, outputDiskFull = readCount, readFull })
	readOOMKillCount = oomNow.Load
	outputDiskFull = func(string) bool { return full.Load() }
}

func TestClassifyExit(t *testing.T) {
	var oom atomic.Int64
	var full atomic.Bool
	fakeExitSignals(t, &oom, &full)
	killed, failed := runErr(t, "kill -9 $$"), runErr(t, "exit 1")

	for _, tc := range []struct {
		name     string
		err      error
		start    int64
		now      int64
		full     bool
		reason   string
		oomKills int
	}{
		{"success", nil, 3, 5, true, "", 0},
		{"SIGKILL while the count rose", killed, 3, 4, false, ExitReasonOOMKilled, 1},
		{"SIGKILL with no OOM kill", killed, 3, 3, false, "", 0},
		{"failure while another process was OOM-killed", failed, 3, 5, false, "", 2},
		{"counter unknown at start", killed, -1, 9, false, "", 0},
		{"failure on a full disk", failed, 3, 3, true, ExitReasonDiskFull, 0},
		{"OOM kill on a full disk", killed, 3, 4, true, ExitReasonOOMKilled, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oom.Store(tc.now)
			full.Store(tc.full)
			reason, oomKills := classifyExit(tc.err, tc.start, "/var/log/sandbox-api/x.stdout.log")
			if reason != tc.reason || oomKills != tc.oomKills {
				t.Errorf("classifyExit = %q, %d; want %q, %d", reason, oomKills, tc.reason, tc.oomKills)
			}
		})
	}
}

// The reason reaches the process' snapshot, is never given to a run the API
// itself ended, and does not outlive the run it describes.
func TestExitReasonOnProcessSnapshot(t *testing.T) {
	var oom atomic.Int64
	var full atomic.Bool
	fakeExitSignals(t, &oom, &full)
	pm := newStdinTestManager(t)

	start := func(command string, restarts int) *ProcessInfo {
		t.Helper()
		p, err := pm.ExecuteProcess(command, "", "", nil, false, 0, nil, restarts > 0, restarts, false, false)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	finished := func(p *ProcessInfo) ProcessSnapshot {
		t.Helper()
		select {
		case <-p.Finished:
		case <-time.After(10 * time.Second):
			t.Fatalf("%q did not finish", p.Command)
		}
		snapshot, _ := pm.GetProcessSnapshot(p.PID)
		return snapshot
	}
	const killSelf = "sleep 0.2; kill -9 $$"

	if got := finished(start(killSelf, 0)); got.ExitReason != "" || got.OOMKills != 0 {
		t.Errorf("SIGKILL with no OOM kill: reason %q, oomKills %d; want none", got.ExitReason, got.OOMKills)
	}

	// The kernel's count rises while the process runs, then it is SIGKILLed.
	p := start(killSelf, 0)
	oom.Add(1)
	if got := finished(p); got.ExitReason != ExitReasonOOMKilled || got.OOMKills != 1 || got.Status != StatusFailed {
		t.Errorf("OOM-killed run: reason %q, oomKills %d, status %s; want oom_killed, 1, failed",
			got.ExitReason, got.OOMKills, got.Status)
	}

	// A kill the API sent is not blamed on the OOM killer, even if one ran.
	p = start("sleep 30", 0)
	oom.Add(1)
	if err := pm.KillProcess(p.PID); err != nil {
		t.Fatal(err)
	}
	if got := finished(p); got.ExitReason != "" || got.Status != StatusKilled {
		t.Errorf("API kill: reason %q, status %s; want none, killed", got.ExitReason, got.Status)
	}

	// The first run is OOM-killed; the restart counts from there, and its own
	// SIGKILL is not.
	p = start(killSelf, 1)
	oom.Add(1)
	if got := finished(p); got.ExitReason != "" || got.OOMKills != 0 || got.RestartCount != 1 {
		t.Errorf("restarted run: reason %q, oomKills %d, restarts %d; want none, 0, 1",
			got.ExitReason, got.OOMKills, got.RestartCount)
	}

	// A failure while the disk is full; a success on a full disk says nothing.
	full.Store(true)
	if got := finished(start("exit 1", 0)); got.ExitReason != ExitReasonDiskFull {
		t.Errorf("failure on a full disk: reason %q, want disk_full", got.ExitReason)
	}
	if got := finished(start("true", 0)); got.ExitReason != "" {
		t.Errorf("success on a full disk: reason %q, want none", got.ExitReason)
	}
}
