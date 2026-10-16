package lifecycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startShell(t *testing.T, script string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command("sh", "-c", script)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %q: %v", script, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

func alive(cmd *exec.Cmd) bool {
	return cmd.ProcessState == nil && cmd.Process.Signal(syscall.Signal(0)) == nil
}

func TestForwardSignalsOnlyTheProcessesThatHandleIt(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "usr2")
	handler := startShell(t, `trap 'echo got > `+marker+`' USR2; while :; do sleep 0.05; done`)
	plain := startShell(t, `exec sleep 30`)

	deadline := time.Now().Add(5 * time.Second)
	for !Catches(handler.Process.Pid, syscall.SIGUSR2) {
		if time.Now().After(deadline) {
			t.Fatal("the trap never showed up in SigCgt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if Catches(plain.Process.Pid, syscall.SIGUSR2) {
		t.Fatal("sleep reported as handling SIGUSR2")
	}

	if sent := Forward(syscall.SIGUSR2); sent != 1 {
		t.Fatalf("Forward signalled %d processes, want 1", sent)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the handler never ran")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !alive(plain) {
		t.Fatal("a process with no SIGUSR2 handler was signalled and died")
	}
}

func TestDescendantsIncludesGrandchildren(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "pid")
	shell := startShell(t, `sleep 30 & echo $! > `+marker+`; wait`)

	deadline := time.Now().Add(5 * time.Second)
	var grandchild int
	for grandchild == 0 {
		if raw, err := os.ReadFile(marker); err == nil && len(raw) > 1 {
			if grandchild, err = strconv.Atoi(strings.TrimSpace(string(raw))); err != nil {
				t.Fatalf("not a pid: %q", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the shell never started its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	found := descendants(os.Getpid())
	if !slices.Contains(found, shell.Process.Pid) || !slices.Contains(found, grandchild) {
		t.Fatalf("descendants = %v, want both %d and %d", found, shell.Process.Pid, grandchild)
	}
	_ = syscall.Kill(grandchild, syscall.SIGKILL)
}

func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if raw, err := os.ReadFile(path); err == nil && strings.HasSuffix(string(raw), "\n") {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("not a pid: %q", raw)
			}
			return pid
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s was never written", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestForwardReachesAdoptedProcessesThroughRoots(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "usr2")
	pidFile := filepath.Join(dir, "pid")
	// The outer shell exits at once, so the trapping shell is reparented away
	// from this process, like a workload adopted after an API restart.
	launcher := exec.Command("sh", "-c", `sh -c 'trap "echo got > `+marker+`" USR2; while :; do sleep 0.05; done' & echo $! > `+pidFile)
	if err := launcher.Run(); err != nil {
		t.Fatalf("launch: %v", err)
	}
	orphan := waitForPID(t, pidFile)
	t.Cleanup(func() { _ = syscall.Kill(orphan, syscall.SIGKILL) })
	deadline := time.Now().Add(5 * time.Second)
	for !Catches(orphan, syscall.SIGUSR2) {
		if time.Now().After(deadline) {
			t.Fatal("the trap never showed up in SigCgt")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if slices.Contains(descendants(os.Getpid()), orphan) {
		t.Skip("orphans are reparented to this process (a subreaper), nothing to cover")
	}

	if sent := Forward(syscall.SIGUSR2, orphan); sent != 1 {
		t.Fatalf("Forward signalled %d processes, want 1", sent)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the adopted process never got SIGUSR2")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGroupAliveIgnoresEmptyGroups(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	pgid := cmd.Process.Pid
	if !GroupAlive(pgid) {
		t.Fatal("a running group reported as gone")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if GroupAlive(pgid) {
		t.Fatal("an empty group reported as alive")
	}
	if GroupAlive(0) || GroupAlive(-1) {
		t.Fatal("an invalid group reported as alive")
	}
}

func TestCatchesAnUnknownProcess(t *testing.T) {
	if Catches(-1, syscall.SIGUSR2) {
		t.Fatal("a missing process reported as handling SIGUSR2")
	}
}
