package process

import (
	"os"
	"testing"
	"time"
)

func assertPrivateProcessLogs(t *testing.T, p *ProcessInfo) {
	t.Helper()
	for _, path := range []string{p.StdoutFile, p.StderrFile, p.LogFile} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0600 {
			t.Errorf("%s mode = %04o, want 0600", path, info.Mode().Perm())
		}
	}
}

func TestProcessLogFilesPrivateAcrossRestart(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		name := "existing"
		if recreate {
			name = "recreated"
		}
		t.Run(name, func(t *testing.T) {
			pm := newStdinTestManager(t)
			p, err := pm.ExecuteProcess("printf private-output", "", "private-logs", nil, false, 0, nil, false, 0, false, false)
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.Finished:
			case <-time.After(5 * time.Second):
				t.Fatal("process did not finish")
			}
			assertPrivateProcessLogs(t, p)
			for _, path := range []string{p.StdoutFile, p.StderrFile, p.LogFile} {
				if recreate {
					err = os.Remove(path)
				} else {
					err = os.Chmod(path, 0644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			// Model a completed process restored by a new API, with no old waiter.
			restored := newTestProcess(&captureWriter{})
			restored.Command = p.Command
			restored.StdoutFile, restored.StderrFile, restored.LogFile = p.StdoutFile, p.StderrFile, p.LogFile
			restored.LogFormat = p.LogFormat
			restored.Finished = make(chan struct{})
			close(restored.Finished)
			p = restored
			if _, err := pm.restartProcess(p, nil); err != nil {
				t.Fatal(err)
			}
			select {
			case <-p.Finished:
			case <-time.After(5 * time.Second):
				t.Fatal("restart did not finish")
			}
			assertPrivateProcessLogs(t, p)
		})
	}
}
