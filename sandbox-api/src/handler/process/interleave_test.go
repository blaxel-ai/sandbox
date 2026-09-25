package process

import (
	"strings"
	"testing"
	"time"
)

// A stdout line longer than the read buffer and a stderr line written at the
// same time must reach a text consumer as two whole lines. The tailer used to
// read the streams in turns, so the stderr line landed at byte 4096 of the
// stdout line; an MCP server logging to stderr while answering tools/list
// produced exactly that.
func TestLongStdoutLineIsNotSplicedByStderr(t *testing.T) {
	pm := newStdinTestManager(t)
	w := &captureWriter{}
	// Build the line first so its single write lands in one go, then stderr,
	// then stdout: the order the tailer used to get wrong.
	cmd := `x=$(head -c 9000 /dev/zero | tr "\0" x); echo "to stderr" >&2; printf '%s\n' "$x"; sleep 1`
	pid, err := pm.StartProcess(cmd, "", nil, false, 0, false, 0, false, noop)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	if err := pm.StreamProcessOutput(pid, w); err != nil {
		t.Fatalf("attaching writer: %v", err)
	}
	proc, _ := pm.GetProcessByIdentifier(pid)
	select {
	case <-proc.Finished:
	case <-time.After(15 * time.Second):
		t.Fatal("process did not finish")
	}
	<-proc.TailDone

	out := w.String()
	for _, want := range []string{"stdout:" + strings.Repeat("x", 9000) + "\n", "stderr:to stderr\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("line spliced: %q missing from stream; stream starts of lines: %v", want[:20], lineStarts(out))
		}
	}
}

func lineStarts(s string) []string {
	var starts []string
	for _, l := range strings.Split(s, "\n") {
		if len(l) > 24 {
			l = l[:24] + "…"
		}
		starts = append(starts, l)
	}
	return starts
}

// Finishing the current line must not turn into never leaving stdout: a child
// that streams newline-free output forever still lets stderr through. In
// practice the tailer outruns the writer and yields at EOF anyway, so this
// mostly pins the loop's exit conditions; maxDrainReads is what guards a writer
// that keeps up.
func TestEndlessStdoutDoesNotStarveStderr(t *testing.T) {
	pm := newStdinTestManager(t)
	w := &captureWriter{}
	cmd := `echo "to stderr" >&2; exec cat /dev/zero | tr "\0" x`
	pid, err := pm.StartProcess(cmd, "", nil, false, 0, false, 0, false, noop)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	t.Cleanup(func() { _ = pm.KillProcess(pid) })
	if err := pm.StreamProcessOutput(pid, w); err != nil {
		t.Fatalf("attaching writer: %v", err)
	}
	waitFor(t, "stderr line while stdout streams", func() bool {
		return strings.Contains(w.String(), "stderr:to stderr\n")
	})
}
