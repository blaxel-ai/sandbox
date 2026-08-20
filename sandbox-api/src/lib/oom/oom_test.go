package oom

import (
	"os"
	"os/exec"
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

// A process that is already gone must not turn into an error for the caller.
func TestPreferAsVictimToleratesADeadProcess(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("failed to run the child: %v", err)
	}
	PreferAsVictim(cmd.Process.Pid)
}
