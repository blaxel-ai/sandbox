package process

import (
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestWaitForPortsSeparateWaitTimeout(t *testing.T) {
	for _, test := range []struct {
		name        string
		timeout     int
		waitTimeout []int
		wantProcess bool
	}{
		{"unlimited-execution", 0, []int{1}, true},
		{"longer-execution", 3, []int{1}, true},
		{"uncapped-wait", 1, nil, false},
		{"equal-deadlines", 1, []int{1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pm := newStdinTestManager(t)
			const name = "waiting-for-port"
			t.Cleanup(func() {
				if p, ok := pm.GetProcessByIdentifier(name); ok {
					_ = pm.KillProcess(p.PID)
					select {
					case <-p.Finished:
					case <-time.After(3 * time.Second):
						t.Error("process did not exit during cleanup")
					}
				}
			})
			p, err := pm.ExecuteProcess("exec sleep 30", "", name, nil, false,
				test.timeout, []int{freePort(t)}, false, 0, false, false, test.waitTimeout...)
			if err == nil {
				t.Fatal("expected waiting for an unopened port to fail")
			}
			if !test.wantProcess {
				if p != nil {
					t.Fatal("an uncapped port wait must preserve the nil process error contract")
				}
				return
			}
			if !strings.Contains(err.Error(), "timed out waiting for ports after 1 seconds") {
				t.Fatalf("unexpected wait error: %v", err)
			}
			if p == nil || p.PID == "" {
				t.Fatal("the separate wait deadline must return the running process PID")
			}
			state, ok := pm.GetProcessSnapshot(p.PID)
			if !ok || state.Status != StatusRunning {
				t.Fatalf("the wait deadline stopped execution: %+v", state)
			}
			if err := syscall.Kill(p.ProcessPid, 0); err != nil {
				t.Fatalf("the command died at the wait deadline: %v", err)
			}
			if test.timeout > 0 {
				select {
				case <-p.Finished:
				case <-time.After(4 * time.Second):
					t.Fatal("the execution deadline was lost after the port wait returned")
				}
				state, _ = pm.GetProcessSnapshot(p.PID)
				if state.Status != StatusKilled {
					t.Fatalf("expected execution timeout to kill process: %+v", state)
				}
			}
		})
	}
}
