package process

import (
	"bytes"
	"sync/atomic"

	"github.com/sirupsen/logrus"
)

// Process output is exported to telemetry by writing it through logrus, which
// goes to sandbox-api's stderr. In a VM the guest init copies that to the
// serial console a byte at a time, and each line comes out several times larger
// once its labels are added, so it drains at a few tens of KB/s. The tailer used
// to log every line itself, under the process' log lock: a command printing a
// megabyte stayed "running" for minutes after it had exited, because wait and
// status wait for the tailer.
//
// So the tailer only hands lines to a bounded queue that a goroutine of its own
// drains, and drops what does not fit. The file capture, the inline status
// output, /logs and the streams never depend on this path.
const (
	// telemetryQueueLines is how many lines may wait for the exporter. At the
	// maxLoggedLineBytes cap per line that holds at most 4 MiB.
	telemetryQueueLines = 512
	// telemetryBudgetBytes and telemetryBudgetLines are what one run of a
	// process may export in all, after which its lines are only counted. The
	// output itself is still captured whole in the log files.
	telemetryBudgetBytes = 2 * 1024 * 1024
	telemetryBudgetLines = 20000
	// telemetryQueuedBytesMax caps the bytes waiting for export across every
	// exporter of the sandbox. A run's exporter outlives the run until it has
	// drained, so a noisy process restarting faster than the sink drains would
	// otherwise stack up one full queue per run.
	telemetryQueuedBytesMax = 4 * 1024 * 1024
)

// telemetryQueuedBytes is what every exporter holds, queued and not yet
// logged.
var telemetryQueuedBytes atomic.Int64

// telemetryLine is one line queued for export.
type telemetryLine struct {
	stderr bool
	line   []byte
}

// processTelemetry exports one run's output lines. The tailer is its only
// sender: offer and close are called from the tailer goroutine alone, so the
// counters need no lock; the exporter reads them only after the queue is
// closed, which orders those reads after the last write.
type processTelemetry struct {
	queue chan telemetryLine
	// done is closed once the exporter has logged every queued line and the
	// drop summary, and has returned.
	done chan struct{}

	name, pid  string
	run        int
	maxBytes   int
	maxLines   int
	loggedLine int
	loggedByte int
	exhausted  bool
	// Lines past the budget, and lines the budget allowed that found the queue
	// full because the exporter is behind.
	droppedBudget    int
	droppedQueueFull int
	// lineBytes is how much of the current line of each stream (stdout,
	// stderr) was already offered: a line longer than a read comes in
	// fragments, and maxLoggedLineBytes caps the line, not each fragment.
	lineBytes [2]int

	// pending collects the lines of one chunk while the log lock is held, so
	// they are queued only after it is released. The slices point into the
	// tailer's read buffer, which it does not reuse before the next read.
	pending []telemetryLine
}

// telemetryStartedHook, when set, is handed each exporter the tailer starts.
// Tests use it to wait for the exporter to end.
var telemetryStartedHook atomic.Pointer[func(*processTelemetry)]

// newProcessTelemetry starts the exporter for one run of a process.
// run is the process' restart count, so the runs of one process (which can
// share a PID label) stay apart.
func newProcessTelemetry(name, pid string, run, queueLines, maxBytes, maxLines int) *processTelemetry {
	t := &processTelemetry{
		queue:    make(chan telemetryLine, queueLines),
		done:     make(chan struct{}),
		name:     name,
		pid:      pid,
		run:      run,
		maxBytes: maxBytes,
		maxLines: maxLines,
	}
	go t.export()
	if hook := telemetryStartedHook.Load(); hook != nil {
		(*hook)(t)
	}
	return t
}

// collect remembers a line of the chunk being ingested. Called with the log
// lock held; it neither logs nor blocks.
func (t *processTelemetry) collect(streamType string, line []byte) {
	t.pending = append(t.pending, telemetryLine{stderr: streamType == "stderr", line: line})
}

// flush queues the lines collect gathered. Called without the log lock.
func (t *processTelemetry) flush() {
	for i := range t.pending {
		t.offer(t.pending[i])
		t.pending[i] = telemetryLine{}
	}
	t.pending = t.pending[:0]
}

// offer queues a line for export, or counts it as dropped when the run's budget
// is spent or the exporter is behind. It never blocks.
func (t *processTelemetry) offer(l telemetryLine) {
	stream := 0
	if l.stderr {
		stream = 1
	}
	endsLine := bytes.HasSuffix(l.line, []byte("\n"))
	line := bytes.TrimSuffix(l.line, []byte("\n"))
	// The rest of a line already offered up to the cap is neither exported
	// nor charged to the budget: it was cut, not dropped.
	room := maxLoggedLineBytes - t.lineBytes[stream]
	if endsLine {
		t.lineBytes[stream] = 0
	} else {
		t.lineBytes[stream] += len(line)
	}
	if len(line) == 0 || room <= 0 {
		return
	}
	if len(line) > room {
		line = line[:room]
	}
	// Once the budget is spent it stays spent: a short line after a long one
	// that did not fit would otherwise be exported out of its context.
	if t.exhausted || t.loggedLine >= t.maxLines || t.loggedByte+len(line) > t.maxBytes {
		t.exhausted = true
		t.droppedBudget++
		return
	}
	// The tailer is the queue's only sender, so a queue with room now still
	// has room at the send: the line is copied only when it will be queued.
	size := int64(len(line))
	if len(t.queue) == cap(t.queue) || telemetryQueuedBytes.Add(size) > telemetryQueuedBytesMax {
		if len(t.queue) < cap(t.queue) {
			telemetryQueuedBytes.Add(-size)
		}
		t.droppedQueueFull++
		return
	}
	t.queue <- telemetryLine{stderr: l.stderr, line: bytes.Clone(line)}
	t.loggedLine++
	t.loggedByte += len(line)
}

// close ends the run's export. It does not wait: the exporter logs what is
// still queued, then the drop summary, and returns.
func (t *processTelemetry) close() {
	close(t.queue)
}

func (t *processTelemetry) export() {
	defer close(t.done)
	base := logrus.WithFields(logrus.Fields{
		"source":       "process",
		"process-name": t.name,
		"process-pid":  t.pid,
		"process-run":  t.run,
	})
	stdout := base.WithField("stream", "stdout")
	stderr := base.WithField("stream", "stderr")
	for l := range t.queue {
		if l.stderr {
			logProcessLine(stderr, "stderr", l.line)
		} else {
			logProcessLine(stdout, "stdout", l.line)
		}
		telemetryQueuedBytes.Add(-int64(len(l.line)))
	}
	if dropped := t.droppedBudget + t.droppedQueueFull; dropped > 0 {
		base.WithFields(logrus.Fields{
			"dropped_over_budget": t.droppedBudget,
			"dropped_queue_full":  t.droppedQueueFull,
			"exported_lines":      t.loggedLine,
			"exported_bytes":      t.loggedByte,
		}).Warnf("%d lines dropped from process output telemetry; read the process' logs for its output (they keep its most recent output up to their size cap)", dropped)
	}
}
