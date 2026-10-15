package archive

import (
	"os/exec"
	"path/filepath"
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
