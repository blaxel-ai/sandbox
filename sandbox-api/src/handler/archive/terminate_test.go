package archive

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

func TestTerminateWorkloadWaitsForEveryProcessToExit(t *testing.T) {
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(t.TempDir(), "process-state.json"))
	const shutdown = 500 * time.Millisecond

	pm := process.GetProcessManager()
	identifier, err := pm.StartProcessWithName(`trap 'sleep 0.5; exit 0' TERM; while :; do sleep 0.05; done`, "", "graceful-worker", nil, true, 3, false, 0, false, func(*process.ProcessInfo) {})
	if err != nil {
		t.Fatalf("failed to start the workload: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess(identifier) })

	startup := exec.Command("sh", "-c", `trap 'sleep 0.5; exit 0' TERM; while :; do sleep 0.05; done`)
	if err := startup.Start(); err != nil {
		t.Fatalf("failed to start the startup command: %v", err)
	}
	t.Cleanup(func() { _ = startup.Process.Kill() })
	RegisterStartupWorkload(startup.Process.Pid, nil)
	t.Cleanup(func() { RegisterStartupWorkload(0, nil) })
	startupDone := make(chan struct{})
	go func() {
		_ = startup.Wait()
		UnregisterStartupWorkload(startup.Process.Pid)
		close(startupDone)
	}()
	// Let both shells install their trap.
	time.Sleep(200 * time.Millisecond)

	started := time.Now()
	TerminateWorkload()
	if elapsed := time.Since(started); elapsed < shutdown {
		t.Fatalf("TerminateWorkload returned after %s, before the workload's %s shutdown", elapsed, shutdown)
	}
	select {
	case <-startupDone:
	case <-time.After(time.Second):
		t.Fatal("the startup command is still running")
	}
	// The OS process is gone; the manager publishes its final status right after.
	deadline := time.Now().Add(2 * time.Second)
	for {
		info, ok := pm.GetProcessByIdentifier(identifier)
		if !ok {
			t.Fatal("the process is gone from the manager")
		}
		if info.Status == process.StatusStopped && info.RestartCount == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("process status = %s after %d restarts, want stopped and not restarted", info.Status, info.RestartCount)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestTerminateWorkloadStopsChildrenLeftByAnExitedProcess(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(dir, "process-state.json"))
	marker := filepath.Join(dir, "term")
	pidFile := filepath.Join(dir, "pid")

	pm := process.GetProcessManager()
	// The leader starts a background worker in its process group and exits.
	command := `sh -c 'trap "sleep 0.3; echo flushed > ` + marker + `; exit 0" TERM; while :; do sleep 0.05; done' & echo $! > ` + pidFile
	identifier, err := pm.StartProcessWithName(command, "", "leaves-a-child", nil, false, 0, false, 0, false, func(*process.ProcessInfo) {})
	if err != nil {
		t.Fatalf("failed to start the workload: %v", err)
	}
	var child int
	deadline := time.Now().Add(5 * time.Second)
	for child == 0 {
		if raw, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(raw), "\n") {
			if child, err = strconv.Atoi(strings.TrimSpace(string(raw))); err != nil {
				t.Fatalf("not a pid: %q", raw)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the leader never started its child")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })
	for {
		info, ok := pm.GetProcessByIdentifier(identifier)
		if ok && info.Status != process.StatusRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the leader never exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// Let the child install its trap.
	time.Sleep(200 * time.Millisecond)

	TerminateWorkload()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("TerminateWorkload returned before the leftover child got SIGTERM and flushed")
	}
}
