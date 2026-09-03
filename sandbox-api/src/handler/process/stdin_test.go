package process

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// noop stands in for the completion callback, which the manager requires.
func noop(*ProcessInfo) {}

func newStdinTestManager(t *testing.T) *ProcessManager {
	t.Helper()
	original := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = original })
	return NewProcessManager()
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func stdoutContains(pm *ProcessManager, pid, needle string) func() bool {
	return func() bool {
		logs, err := pm.GetProcessOutput(pid)
		return err == nil && strings.Contains(logs.Stdout, needle)
	}
}

// A write reaches the child verbatim, and closing stdin ends it: the two halves
// of driving a stdio protocol.
func TestStdinRoundTripThenEOF(t *testing.T) {
	pm := newStdinTestManager(t)
	pid, err := pm.StartProcess("cat", "", nil, false, 0, false, 0, true, noop)
	if err != nil {
		t.Fatalf("starting cat: %v", err)
	}

	if err := pm.WriteStdin(pid, []byte(`{"jsonrpc":"2.0","id":1}`+"\n")); err != nil {
		t.Fatalf("writing stdin: %v", err)
	}
	waitFor(t, "echoed line", stdoutContains(pm, pid, `{"jsonrpc":"2.0","id":1}`))

	if err := pm.CloseStdin(pid); err != nil {
		t.Fatalf("closing stdin: %v", err)
	}
	waitFor(t, "cat to exit on EOF", func() bool {
		p, _ := pm.GetProcessByIdentifier(pid)
		return p.Status == StatusCompleted
	})

	// Closing twice is a no-op, writing afterwards is a clear error.
	if err := pm.CloseStdin(pid); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if err := pm.WriteStdin(pid, []byte("x\n")); !errors.Is(err, ErrStdinClosed) {
		t.Fatalf("write after close: got %v, want ErrStdinClosed", err)
	}
}

func TestStdinNotEnabled(t *testing.T) {
	pm := newStdinTestManager(t)
	pid, err := pm.StartProcess("sleep 5", "", nil, false, 0, false, 0, false, noop)
	if err != nil {
		t.Fatalf("starting sleep: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess(pid) })

	if err := pm.WriteStdin(pid, []byte("x\n")); !errors.Is(err, ErrStdinNotEnabled) {
		t.Fatalf("got %v, want ErrStdinNotEnabled", err)
	}
	if err := pm.CloseStdin(pid); !errors.Is(err, ErrStdinNotEnabled) {
		t.Fatalf("close: got %v, want ErrStdinNotEnabled", err)
	}
	if err := pm.WriteStdin("no-such-process", nil); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("unknown process: got %v", err)
	}
}

// A restart-on-failure run is a new child, so it needs a new pipe: the old one
// was closed by Wait when the failed run exited.
func TestStdinFreshPipeAfterRestart(t *testing.T) {
	pm := newStdinTestManager(t)
	pid, err := pm.StartProcess(`sh -c 'read l; echo "got:$l"; exit 1'`, "", nil, true, 1, false, 0, true, noop)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}

	if err := pm.WriteStdin(pid, []byte("one\n")); err != nil {
		t.Fatalf("first write: %v", err)
	}
	waitFor(t, "first echo", stdoutContains(pm, pid, "got:one"))
	waitFor(t, "restarted run", func() bool {
		p, _ := pm.GetProcessByIdentifier(pid)
		return p.RestartCount == 1 && p.Status == StatusRunning
	})

	if err := pm.WriteStdin(pid, []byte("two\n")); err != nil {
		t.Fatalf("write after restart: %v", err)
	}
	waitFor(t, "second echo", stdoutContains(pm, pid, "got:two"))
}

// Once the child has exited on its own, exec.Cmd.Wait has closed the pipe:
// writes must say "closed", not fail as a server error, and close stays a no-op.
func TestStdinAfterNaturalExit(t *testing.T) {
	pm := newStdinTestManager(t)
	pid, err := pm.StartProcess("true", "", nil, false, 0, false, 0, true, noop)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	waitFor(t, "process to exit", func() bool {
		p, _ := pm.GetProcessByIdentifier(pid)
		return p.Status == StatusCompleted
	})
	if err := pm.WriteStdin(pid, []byte("late\n")); !errors.Is(err, ErrStdinClosed) {
		t.Fatalf("write after exit: got %v, want ErrStdinClosed", err)
	}
	if err := pm.CloseStdin(pid); err != nil {
		t.Fatalf("close after exit: %v", err)
	}
}

// A child that never reads must not pin the writer forever: the write gives up
// after stdinWriteTimeout and the lock is free for the close that follows.
func TestStdinStalledWriteTimesOut(t *testing.T) {
	pm := newStdinTestManager(t)
	original := stdinWriteTimeout
	stdinWriteTimeout = 200 * time.Millisecond
	t.Cleanup(func() { stdinWriteTimeout = original })

	pid, err := pm.StartProcess("sleep 30", "", nil, false, 0, false, 0, true, noop)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess(pid) })

	// Well past the kernel pipe buffer, into a reader that never drains it.
	start := time.Now()
	err = pm.WriteStdin(pid, make([]byte, 1<<20))
	if !errors.Is(err, ErrStdinStalled) {
		t.Fatalf("got %v, want ErrStdinStalled", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("write blocked %s, deadline not applied", elapsed)
	}
	if err := pm.CloseStdin(pid); err != nil {
		t.Fatalf("close after stall: %v", err)
	}
}
