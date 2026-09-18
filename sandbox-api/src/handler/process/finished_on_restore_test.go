package process

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Processes restored from disk after a sandbox-api restart get their
// ProcessInfo from a struct literal rather than the constructor. A Finished
// channel missing there is nil, and a receive on a nil channel blocks forever:
// the log stream of a restored process would never end server-side, leaving
// its client on keepalives long after the process was over.
func TestRestoredProcessHasAUsableFinishedChannel(t *testing.T) {
	originalLogDir := ProcessLogDir
	dir := t.TempDir()
	ProcessLogDir = dir
	t.Cleanup(func() { ProcessLogDir = originalLogDir })

	stateFile := filepath.Join(dir, "state.json")
	t.Setenv("SANDBOX_STATE_FILE", stateFile)

	logFile := filepath.Join(dir, "restored.log")
	for _, f := range []string{logFile,
		filepath.Join(dir, "restored.stdout.log"),
		filepath.Join(dir, "restored.stderr.log")} {
		if err := os.WriteFile(f, []byte("stdout:hi\n"), 0o644); err != nil {
			t.Fatalf("seeding log file: %v", err)
		}
	}

	completed := time.Now().Add(-time.Minute)
	state := ManagerState{
		Version: 1,
		SavedAt: time.Now(),
		Processes: map[string]ProcessState{
			"1234": {
				PID:         "1234",
				Name:        "restored",
				Command:     "echo hi",
				ProcessPid:  999999, // long gone
				StartedAt:   completed.Add(-time.Minute),
				CompletedAt: &completed,
				ExitCode:    0,
				Status:      StatusCompleted,
				LogFile:     logFile,
				StdoutFile:  filepath.Join(dir, "restored.stdout.log"),
				StderrFile:  filepath.Join(dir, "restored.stderr.log"),
			},
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("encoding state: %v", err)
	}
	if err := os.WriteFile(stateFile, encoded, 0o644); err != nil {
		t.Fatalf("writing state file: %v", err)
	}

	pm := NewProcessManager()
	if err := pm.LoadState(); err != nil {
		t.Fatalf("loading state: %v", err)
	}

	proc, ok := pm.GetProcessByIdentifier("restored")
	if !ok {
		t.Fatal("the process was not restored")
	}
	if proc.Finished == nil {
		t.Fatal("Finished is nil on a restored process: a stream waiting on it would block forever")
	}

	// It was already over when restored, so a stream attaching to it must be
	// told so immediately rather than hanging on keepalives.
	select {
	case <-proc.Finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Finished never closed for a process restored in a completed state")
	}
}
