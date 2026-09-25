package process

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestTerminationWaitsForExit(t *testing.T) {
	for _, graceful := range []bool{true, false} {
		name := "ignores-term"
		command := `exec sh -c 'trap "" TERM; echo READY; while :; do sleep 1; done'`
		if graceful {
			name = "graceful"
			command = `exec sh -c 'trap "sleep 1; echo FINAL; exit 7" TERM; echo READY; while :; do sleep 1; done'`
		}
		t.Run(name, func(t *testing.T) {
			pm := newStdinTestManager(t)
			pid, err := pm.StartProcess(command, "", nil, true, 3, false, 0, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			pm.mu.RLock()
			proc := pm.processes[pid]
			finished, osPID := proc.Finished, proc.ProcessPid
			pm.mu.RUnlock()
			t.Cleanup(func() {
				_ = pm.KillProcess(pid)
				select {
				case <-finished:
				case <-time.After(5 * time.Second):
					t.Error("child did not finish")
				}
			})
			waitFor(t, "ready", stdoutContains(pm, pid, "READY"))
			if err := pm.StopProcess(pid); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(200 * time.Millisecond)
			for time.Now().Before(deadline) {
				state, _ := pm.GetProcessSnapshot(pid)
				if state.Status != StatusRunning || state.CompletedAt != nil {
					t.Fatalf("premature terminal observation: %+v", state)
				}
				time.Sleep(5 * time.Millisecond)
			}
			wantStatus, wantExit := StatusStopped, 7
			if !graceful {
				// Timeout escalation must still work after an ignored graceful stop.
				go pm.enforceKeepAliveTimeout(proc, 0)
				wantStatus, wantExit = StatusKilled, -1
			}
			var readers sync.WaitGroup
			for range 4 {
				readers.Add(1)
				go func() {
					defer readers.Done()
					for {
						select {
						case <-finished:
							return
						default:
							pm.GetProcessSnapshot(pid)
							pm.ListProcessSnapshots()
						}
					}
				}()
			}
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("termination timed out")
			}
			readers.Wait()
			state, _ := pm.GetProcessSnapshot(pid)
			if state.Status != wantStatus || state.ExitCode != wantExit || state.CompletedAt == nil || state.RestartCount != 0 {
				t.Fatalf("wrong terminal observation: %+v", state)
			}
			if graceful && (state.Stdout == nil || !strings.Contains(*state.Stdout, "FINAL")) {
				t.Fatal("final output not drained")
			}
			if err := syscall.Kill(osPID, 0); err != syscall.ESRCH {
				t.Fatalf("child still exists after terminal state: %v", err)
			}
		})
	}
}

func TestTerminationDuringRestartDelay(t *testing.T) {
	pm := newStdinTestManager(t)
	pid, err := pm.StartProcess("exit 7", "", nil, true, 3, false, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "restart delay", func() bool {
		pm.mu.RLock()
		defer pm.mu.RUnlock()
		p := pm.processes[pid]
		return p.runExited && p.CompletedAt != nil
	})
	if err := pm.StopProcess(pid); err != nil {
		t.Fatal(err)
	}
	pm.mu.RLock()
	finished := pm.processes[pid].Finished
	pm.mu.RUnlock()
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("restart was not canceled")
	}
	state, _ := pm.GetProcessSnapshot(pid)
	if state.Status != StatusStopped || state.ExitCode != 7 || state.RestartCount != 0 {
		t.Fatalf("unexpected restart: %+v", state)
	}
}

// Killing the managed shell must still signal its process group, including children.
func TestTerminationKillsProcessGroup(t *testing.T) {
	pm := newStdinTestManager(t)
	marker := filepath.Join(t.TempDir(), "heartbeat")
	pid, err := pm.StartProcess(`sh -c 'while :; do echo alive >> "$MARKER"; sleep 0.05; done' & wait`, "", map[string]string{"MARKER": marker}, false, 0, false, 0, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	pm.mu.RLock()
	p := pm.processes[pid]
	osPID, finished := p.ProcessPid, p.Finished
	pm.mu.RUnlock()
	t.Cleanup(func() { _ = syscall.Kill(-osPID, syscall.SIGKILL) })
	waitFor(t, "child heartbeat", func() bool { data, _ := os.ReadFile(marker); return len(data) > 0 })
	if err := pm.KillProcess(pid); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("kill did not finish")
	}
	// Allow an in-flight final write, then prove the child is no longer running.
	time.Sleep(100 * time.Millisecond)
	before, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	after, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("child survived process group kill")
	}
}
