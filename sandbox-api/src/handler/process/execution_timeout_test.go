package process

import (
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestExecutionTimeoutIndependentOfKeepAlive(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		for _, synchronous := range []bool{false, true} {
			t.Run(fmt.Sprintf("keepAlive=%t/synchronous=%t", keepAlive, synchronous), func(t *testing.T) {
				pm := newStdinTestManager(t)
				t.Setenv("BLAXEL_SCALE_FILE", filepath.Join(t.TempDir(), "scale"))
				p, err := pm.ExecuteProcess("exec sleep 60", "", "", nil, synchronous, 1, nil, true, 3, keepAlive, false)
				if p == nil {
					t.Fatalf("start failed: %v", err)
				}
				t.Cleanup(func() { _ = pm.KillProcess(p.PID); <-p.Finished })
				select {
				case <-p.Finished:
				case <-time.After(3 * time.Second):
					t.Fatal("execution deadline did not terminate process")
				}
				state, _ := pm.GetProcessSnapshot(p.PID)
				if state.Status != StatusKilled || state.RestartCount != 0 {
					t.Fatalf("unexpected terminal state: %+v", state)
				}
				if err := syscall.Kill(p.ProcessPid, 0); err != syscall.ESRCH {
					t.Fatalf("process still exists: %v", err)
				}
			})
		}
	}
}

func TestExecutionTimeoutAllowsCompletion(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		for _, timeout := range []int{0, 5} {
			t.Run(fmt.Sprintf("keepAlive=%t/timeout=%d", keepAlive, timeout), func(t *testing.T) {
				pm := newStdinTestManager(t)
				t.Setenv("BLAXEL_SCALE_FILE", filepath.Join(t.TempDir(), "scale"))
				p, err := pm.ExecuteProcess("sleep 1.2; echo completed", "", "", nil, true, timeout, nil, false, 0, keepAlive, false)
				if err != nil {
					t.Fatal(err)
				}
				<-p.Finished
				state, _ := pm.GetProcessSnapshot(p.PID)
				if state.Status != StatusCompleted || state.ExitCode != 0 {
					t.Fatalf("unexpected completion: %+v", state)
				}
			})
		}
	}
}

func TestWaitTimeoutDoesNotEndExecution(t *testing.T) {
	for _, timeout := range []int{0, 3} {
		t.Run(fmt.Sprintf("timeout=%d", timeout), func(t *testing.T) {
			pm := newStdinTestManager(t)
			p, err := pm.ExecuteProcess("exec sleep 30", "", "", nil, true, timeout, nil, false, 0, false, false, 1)
			if p == nil || err == nil {
				t.Fatalf("expected wait timeout with process info, got %v, %v", p, err)
			}
			t.Cleanup(func() { _ = pm.KillProcess(p.PID); <-p.Finished })
			state, _ := pm.GetProcessSnapshot(p.PID)
			if state.Status != StatusRunning {
				t.Fatalf("wait killed the process: %+v", state)
			}
			if err := syscall.Kill(p.ProcessPid, 0); err != nil {
				t.Fatalf("process died at wait deadline: %v", err)
			}
			if timeout > 0 {
				select {
				case <-p.Finished:
				case <-time.After(4 * time.Second):
					t.Fatal("execution deadline lost after wait expired")
				}
				state, _ = pm.GetProcessSnapshot(p.PID)
				if state.Status != StatusKilled {
					t.Fatalf("expected execution timeout kill: %+v", state)
				}
			}
		})
	}
}

func TestRestartedProcessEnforcesExecutionTimeout(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		t.Run(fmt.Sprintf("keepAlive=%t", keepAlive), func(t *testing.T) {
			pm := newStdinTestManager(t)
			t.Setenv("BLAXEL_SCALE_FILE", filepath.Join(t.TempDir(), "scale"))
			pid, err := pm.StartProcess("if [ ! -f attempted ]; then touch attempted; exit 1; fi; exec sleep 30", t.TempDir(), nil, true, 3, keepAlive, 1, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			pm.mu.RLock()
			p := pm.processes[pid]
			pm.mu.RUnlock()
			t.Cleanup(func() { _ = pm.KillProcess(pid); <-p.Finished })
			select {
			case <-p.Finished:
			case <-time.After(5 * time.Second):
				t.Fatal("restarted process survived timeout")
			}
			state, _ := pm.GetProcessSnapshot(pid)
			if state.Status != StatusKilled || state.RestartCount != 1 {
				t.Fatalf("unexpected restarted process state: %+v", state)
			}
		})
	}
}
