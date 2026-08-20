package oom

import (
	"os"
	"os/exec"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
)

func readScoreAdj(t *testing.T, pid int) string {
	t.Helper()
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/oom_score_adj")
	if err != nil {
		t.Skipf("oom_score_adj is not readable on this platform: %v", err)
	}
	return strings.TrimSpace(string(data))
}

func TestPreferAsVictimRaisesTheScoreOfTheChildOnly(t *testing.T) {
	own := readScoreAdj(t, os.Getpid())

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start the child: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	PreferAsVictim(cmd.Process.Pid)

	if got := readScoreAdj(t, cmd.Process.Pid); got != strconv.Itoa(VictimScoreAdj) {
		t.Errorf("child oom_score_adj = %s, want %d", got, VictimScoreAdj)
	}
	if got := readScoreAdj(t, os.Getpid()); got != own {
		t.Errorf("own oom_score_adj changed to %s, want %s", got, own)
	}
}

func TestLimitHeapSetsAShareOfTheGuestMemory(t *testing.T) {
	total := memTotalBytes()
	if total <= 0 {
		t.Skip("cannot read MemTotal on this host")
	}
	previous := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(previous)

	t.Setenv("SANDBOX_MAX_HEAP_PERCENT", "50")
	LimitHeap()

	want := total / 2
	if want < HeapFloorBytes {
		want = HeapFloorBytes
	}
	if got := debug.SetMemoryLimit(-1); got != want {
		t.Errorf("soft memory limit = %d, want %d", got, want)
	}
}

// GOMEMLIMIT is the runtime's own knob, so an operator who set it keeps it.
func TestLimitHeapLeavesAnExplicitLimitAlone(t *testing.T) {
	previous := debug.SetMemoryLimit(-1)
	defer debug.SetMemoryLimit(previous)

	t.Setenv("GOMEMLIMIT", "123456789")
	debug.SetMemoryLimit(700 * 1024 * 1024)
	LimitHeap()

	if got := debug.SetMemoryLimit(-1); got != 700*1024*1024 {
		t.Errorf("soft memory limit = %d, want the %d it was already set to", got, 700*1024*1024)
	}
}

// A process that is already gone must not turn into an error for the caller.
func TestPreferAsVictimToleratesADeadProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run the child: %v", err)
	}
	PreferAsVictim(cmd.Process.Pid)
}
