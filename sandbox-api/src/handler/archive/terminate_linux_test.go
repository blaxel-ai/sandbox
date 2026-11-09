//go:build linux

package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// A startup shell that forks instead of execing dies on SIGTERM; its child has
// to get SIGTERM too, and the stop has to wait for it.
func TestTerminateWorkloadStopsChildrenOfTheStartupCommand(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "term")
	startup := exec.Command("sh", "-c", `sh -c 'trap "sleep 0.3; touch `+marker+`; exit 0" TERM; while :; do sleep 0.05; done' & wait`)
	startup.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := startup.Start(); err != nil {
		t.Fatalf("failed to start the startup command: %v", err)
	}
	pid := startup.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	RegisterStartupWorkload(pid, nil)
	t.Cleanup(func() { RegisterStartupWorkload(0, nil) })
	go func() {
		_ = startup.Wait()
		UnregisterStartupWorkload(pid)
	}()
	// Let the child install its trap.
	time.Sleep(200 * time.Millisecond)

	TerminateWorkload()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("TerminateWorkload returned before the startup command's child got SIGTERM and exited")
	}
}
