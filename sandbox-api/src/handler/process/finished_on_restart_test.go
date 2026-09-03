package process

import (
	"testing"
	"time"
)

// A restart closes the per-run Done channel and installs a fresh one. Anything
// user-facing that waits on Done therefore sees "the process ended" every time
// the process merely bounces: the log stream handler returned 200 mid-restart
// while the process came back up and kept producing output nobody received.
// Finished must stay open across restarts and close only once, for good.
func TestFinishedStaysOpenAcrossRestart(t *testing.T) {
	originalLogDir := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	pm := NewProcessManager()

	// Fails once, then runs long enough for us to observe the restarted state.
	cmd := `if [ ! -f "$MARKER" ]; then touch "$MARKER"; echo run1; exit 1; fi; echo run2; sleep 30`
	proc, err := pm.ExecuteProcess(
		"sh -c '"+cmd+"'", "", "restarter",
		map[string]string{"MARKER": ProcessLogDir + "/once"},
		false, 0, nil, true, 3, false, false,
	)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	firstRunDone := proc.Done

	// The first run's Done closes as soon as that run fails.
	select {
	case <-firstRunDone:
	case <-time.After(15 * time.Second):
		t.Fatal("first run never signalled Done")
	}

	// That is exactly the moment the stream handler used to give up. Finished
	// must not be closed here: the process is coming back.
	select {
	case <-proc.Finished:
		t.Fatal("Finished closed on a restart; a log stream would end while the process runs on")
	default:
	}

	// Wait for the restart to land (the manager sleeps 1s before restarting).
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		current, ok := pm.GetProcessByIdentifier("restarter")
		if ok && current.Status == StatusRunning && current.Done != firstRunDone {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	restarted, ok := pm.GetProcessByIdentifier("restarter")
	if !ok {
		t.Fatal("process disappeared after restart")
	}
	if restarted.Status != StatusRunning {
		t.Fatalf("expected the process to be running again, got %q", restarted.Status)
	}
	if restarted.Done == firstRunDone {
		t.Fatal("expected a fresh Done channel for the new run")
	}
	select {
	case <-restarted.Finished:
		t.Fatal("Finished closed while the restarted process is still running")
	default:
	}

	// And it does close once the process is really over.
	if err := pm.KillProcess("restarter"); err != nil {
		t.Fatalf("killing process: %v", err)
	}
	select {
	case <-restarted.Finished:
	case <-time.After(15 * time.Second):
		t.Fatal("Finished never closed after the process was killed")
	}
}

// Without a restart, Finished closes on the single run ending.
func TestFinishedClosesWithoutRestart(t *testing.T) {
	originalLogDir := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	pm := NewProcessManager()

	proc, err := pm.ExecuteProcess("sh -c 'echo hi'", "", "oneshot", nil, false, 0, nil, false, 0, false, false)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}

	select {
	case <-proc.Finished:
	case <-time.After(15 * time.Second):
		t.Fatal("Finished never closed for a process that ran once and exited")
	}
}
