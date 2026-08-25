package process

import (
	"path/filepath"
	"sync"
	"testing"
)

// A restart that fails before it gets going must not take the API down.
//
// The caller closes Done before calling restartProcess and closes it again if
// restartProcess returns an error. While the channel was only swapped after the
// working-directory and log-file checks, an early failure left the old, already
// closed channel in place, and that second close panicked on a closed channel,
// killing the whole API rather than one process.
func TestFailedRestartLeavesAFreshDoneChannel(t *testing.T) {
	originalLogDir := ProcessLogDir
	dir := t.TempDir()
	ProcessLogDir = dir
	t.Cleanup(func() { ProcessLogDir = originalLogDir })

	pm := NewProcessManager()
	closedDone := make(chan struct{})
	close(closedDone) // the caller has already signalled the previous run's end
	closedTail := make(chan struct{})
	close(closedTail)

	proc := &ProcessInfo{
		PID:        "1",
		Name:       "early-fail",
		Command:    "echo hi",
		WorkingDir: filepath.Join(dir, "does-not-exist"),
		StdoutFile: filepath.Join(dir, "p.stdout.log"),
		StderrFile: filepath.Join(dir, "p.stderr.log"),
		LogFile:    filepath.Join(dir, "p.log"),
		Done:       closedDone,
		TailDone:   closedTail,
		Finished:   make(chan struct{}),
		stdout:     newLogBuffer(),
		stderr:     newLogBuffer(),
		logs:       newLogBuffer(),
	}

	_, err := pm.restartProcess(proc, func(*ProcessInfo) {})
	if err == nil {
		t.Fatal("expected the restart to fail on a missing working directory")
	}

	if proc.Done == closedDone {
		t.Fatal("Done still points at the closed channel: the caller's second close would panic")
	}

	// What the caller does next, and what used to bring the API down.
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		close(proc.Done)
	}()
	if panicked != nil {
		t.Fatalf("closing Done after a failed restart panicked: %v", panicked)
	}
}

// restartProcess is called from the manager's own goroutine, so a panic there
// is fatal to the API. Guard the concurrent shape too.
func TestFailedRestartIsSafeFromSeveralGoroutines(t *testing.T) {
	originalLogDir := ProcessLogDir
	dir := t.TempDir()
	ProcessLogDir = dir
	t.Cleanup(func() { ProcessLogDir = originalLogDir })

	pm := NewProcessManager()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			proc := &ProcessInfo{
				PID:        "1",
				Name:       "early-fail",
				Command:    "echo hi",
				WorkingDir: filepath.Join(dir, "does-not-exist"),
				StdoutFile: filepath.Join(dir, "p.stdout.log"),
				StderrFile: filepath.Join(dir, "p.stderr.log"),
				LogFile:    filepath.Join(dir, "p.log"),
				Done:       make(chan struct{}),
				TailDone:   make(chan struct{}),
				Finished:   make(chan struct{}),
				stdout:     newLogBuffer(),
				stderr:     newLogBuffer(),
				logs:       newLogBuffer(),
			}
			if _, err := pm.restartProcess(proc, func(*ProcessInfo) {}); err == nil {
				t.Error("expected the restart to fail")
				return
			}
			close(proc.Done) // must not panic
			proc.markFinished()
			proc.markFinished() // idempotent
		}()
	}
	wg.Wait()
}
