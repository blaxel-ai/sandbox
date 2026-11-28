package process

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// logSink records what logrus writes, optionally slowly or blocking, standing in
// for sandbox-api's stderr on a VM's serial console.
type logSink struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	delay time.Duration
	// block, when set, holds every write that contains blockOn until it is
	// closed; entered is signalled on the first such write.
	blockOn string
	block   chan struct{}
	entered chan struct{}
	once    sync.Once
	writes  atomic.Int64
}

func (s *logSink) Write(p []byte) (int, error) {
	if s.block != nil && bytes.Contains(p, []byte(s.blockOn)) {
		s.once.Do(func() { close(s.entered) })
		<-s.block
	}
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	s.writes.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// lines returns the logged lines that mention needle.
func (s *logSink) lines(needle string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, l := range strings.Split(s.buf.String(), "\n") {
		if strings.Contains(l, needle) {
			out = append(out, l)
		}
	}
	return out
}

// useLogSink sends logrus to sink for the rest of the test.
func useLogSink(t *testing.T, sink *logSink) {
	t.Helper()
	logger := logrus.StandardLogger()
	out, formatter, level := logger.Out, logger.Formatter, logger.GetLevel()
	logrus.SetOutput(sink)
	logrus.SetFormatter(&logrus.TextFormatter{DisableTimestamp: true})
	logrus.SetLevel(logrus.InfoLevel)
	t.Cleanup(func() {
		logrus.SetOutput(out)
		logrus.SetFormatter(formatter)
		logrus.SetLevel(level)
	})
}

func waitExporter(t *testing.T, tel *processTelemetry, within time.Duration) {
	t.Helper()
	select {
	case <-tel.done:
	case <-time.After(within):
		t.Fatalf("telemetry exporter still running after %v", within)
	}
}

// Past its budget a run exports nothing more, and the exporter says once how
// many lines it left out.
func TestTelemetryBudgetDropsAndSummarizesOnce(t *testing.T) {
	sink := &logSink{}
	useLogSink(t, sink)
	const name = "telemetry-budget-test"

	tel := newProcessTelemetry(name, "1", 0, 100, 1000, 5)
	for i := 0; i < 12; i++ {
		tel.offer(telemetryLine{line: []byte(fmt.Sprintf("line-%02d\n", i))})
	}
	tel.close()
	waitExporter(t, tel, 5*time.Second)

	if tel.loggedLine != 5 || tel.droppedBudget != 7 || tel.droppedQueueFull != 0 {
		t.Fatalf("exported %d, over budget %d, queue full %d; want 5, 7, 0", tel.loggedLine, tel.droppedBudget, tel.droppedQueueFull)
	}
	exported := sink.lines("line-")
	if len(exported) != 5 || !strings.Contains(exported[4], "line-04") {
		t.Fatalf("exported lines %q, want line-00..line-04", exported)
	}
	summaries := sink.lines("lines dropped")
	if len(summaries) != 1 {
		t.Fatalf("want one drop summary, got %q", summaries)
	}
	for _, want := range []string{"7 lines dropped", "dropped_over_budget=7", "dropped_queue_full=0", "process-name=" + name} {
		if !strings.Contains(summaries[0], want) {
			t.Errorf("summary %q lacks %q", summaries[0], want)
		}
	}
}

// The byte budget counts lines as exported (trimmed and capped), and once spent
// it stays spent even for a line that would still fit.
func TestTelemetryByteBudgetIsSticky(t *testing.T) {
	sink := &logSink{}
	useLogSink(t, sink)

	tel := newProcessTelemetry("telemetry-bytes-test", "1", 0, 100, 1000, 1000)
	long := strings.Repeat("y", 300)
	for i := 0; i < 4; i++ {
		tel.offer(telemetryLine{line: []byte(long + "\n")})
	}
	tel.offer(telemetryLine{line: []byte("short\n")})
	tel.close()
	waitExporter(t, tel, 5*time.Second)

	if tel.loggedLine != 3 || tel.loggedByte != 900 || tel.droppedBudget != 2 {
		t.Fatalf("exported %d lines / %d bytes, over budget %d; want 3 / 900 / 2", tel.loggedLine, tel.loggedByte, tel.droppedBudget)
	}
	if got := sink.lines("short"); len(got) != 0 {
		t.Fatalf("a line after the budget was spent was exported: %q", got)
	}
}

// An over-long line costs the budget only what is exported of it.
func TestTelemetryCapsLineLength(t *testing.T) {
	sink := &logSink{}
	useLogSink(t, sink)

	tel := newProcessTelemetry("telemetry-cap-test", "1", 0, 10, 1<<20, 10)
	tel.offer(telemetryLine{line: []byte(strings.Repeat("z", 3*maxLoggedLineBytes))})
	tel.close()
	waitExporter(t, tel, 5*time.Second)
	if tel.loggedByte != maxLoggedLineBytes {
		t.Fatalf("budget charged %d bytes, want %d", tel.loggedByte, maxLoggedLineBytes)
	}
}

// A line longer than a read arrives in fragments: the cap and the budget
// apply to the line, so its fragments past the cap cost nothing, and the
// next line starts afresh. stdout and stderr are tracked apart.
func TestTelemetryCapsALineAcrossFragments(t *testing.T) {
	sink := &logSink{}
	useLogSink(t, sink)

	tel := newProcessTelemetry("telemetry-fragments-test", "1", 0, 100, 1<<20, 100)
	chunk := strings.Repeat("f", 4096)
	for i := 0; i < 8; i++ { // one 32 KiB line in 4 KiB reads
		tel.offer(telemetryLine{line: []byte(chunk)})
	}
	tel.offer(telemetryLine{stderr: true, line: []byte("err\n")})
	tel.offer(telemetryLine{line: []byte("end\n")})
	tel.offer(telemetryLine{line: []byte("next\n")})
	tel.close()
	waitExporter(t, tel, 5*time.Second)

	if want := maxLoggedLineBytes + len("err") + len("next"); tel.loggedByte != want {
		t.Fatalf("budget charged %d bytes, want %d", tel.loggedByte, want)
	}
	if tel.droppedBudget != 0 || tel.droppedQueueFull != 0 {
		t.Fatalf("dropped %d over budget, %d queue full; a cut line is not a drop", tel.droppedBudget, tel.droppedQueueFull)
	}
}

// Exporters of every run share one cap on queued bytes, so a process
// restarting faster than the sink drains cannot pile up a queue per run.
func TestTelemetryQueuedBytesAreCappedAcrossExporters(t *testing.T) {
	const name = "telemetry-shared-cap-test"
	sink := &logSink{blockOn: name, block: make(chan struct{}), entered: make(chan struct{})}
	useLogSink(t, sink)

	line := []byte(strings.Repeat("q", maxLoggedLineBytes-1) + "\n")
	perRun := telemetryQueuedBytesMax / (maxLoggedLineBytes - 1)
	var runs []*processTelemetry
	for run := 0; run < 4; run++ {
		tel := newProcessTelemetry(name, "1", run, perRun+10, 1<<30, 1<<20)
		for i := 0; i < perRun; i++ {
			tel.offer(telemetryLine{line: line})
		}
		runs = append(runs, tel)
	}
	if queued := telemetryQueuedBytes.Load(); queued > telemetryQueuedBytesMax {
		t.Fatalf("%d bytes queued across runs, cap %d", queued, telemetryQueuedBytesMax)
	}
	dropped := 0
	for _, tel := range runs {
		dropped += tel.droppedQueueFull
		tel.close()
	}
	if dropped == 0 {
		t.Fatal("no line was dropped although four runs offered four times the cap")
	}
	close(sink.block)
	for _, tel := range runs {
		waitExporter(t, tel, 10*time.Second)
	}
	if queued := telemetryQueuedBytes.Load(); queued != 0 {
		t.Fatalf("%d bytes still counted as queued after every exporter ended", queued)
	}
}

// A stalled log sink costs lines, never the tailer's time: what does not fit in
// the queue is counted and reported.
func TestTelemetryQueueFullDropsWithoutBlocking(t *testing.T) {
	const name = "telemetry-queue-test"
	sink := &logSink{blockOn: name, block: make(chan struct{}), entered: make(chan struct{})}
	useLogSink(t, sink)

	tel := newProcessTelemetry(name, "1", 0, 2, 1<<20, 1000)
	tel.offer(telemetryLine{line: []byte("first\n")})
	select {
	case <-sink.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("exporter never wrote the first line")
	}
	// The exporter is stuck on "first": two lines fit in the queue, three do not.
	start := time.Now()
	for i := 0; i < 5; i++ {
		tel.offer(telemetryLine{line: []byte(fmt.Sprintf("next-%d\n", i))})
	}
	if d := time.Since(start); d > 100*time.Millisecond {
		t.Fatalf("offering to a full queue took %v", d)
	}
	tel.close()
	close(sink.block)
	waitExporter(t, tel, 5*time.Second)

	if tel.droppedQueueFull != 3 || tel.loggedLine != 3 {
		t.Fatalf("queue full %d, exported %d; want 3, 3", tel.droppedQueueFull, tel.loggedLine)
	}
	summaries := sink.lines("lines dropped")
	if len(summaries) != 1 || !strings.Contains(summaries[0], "dropped_queue_full=3") {
		t.Fatalf("summary %q, want one with dropped_queue_full=3", summaries)
	}
}

// A process that prints a lot must finish as fast as it runs even when the
// API's own log sink is slow - the serial console in a VM drains at tens of
// KB/s, and wait used to sit behind every line being logged to it. Its output
// must still be captured whole, and the exporter must end on its own.
func TestLargeOutputWaitIsNotHeldBySlowTelemetry(t *testing.T) {
	sink := &logSink{delay: 2 * time.Millisecond}
	useLogSink(t, sink)
	pm := newStdinTestManager(t)

	var tels []*processTelemetry
	var telsMu sync.Mutex
	hook := func(tel *processTelemetry) {
		telsMu.Lock()
		tels = append(tels, tel)
		telsMu.Unlock()
	}
	telemetryStartedHook.Store(&hook)
	t.Cleanup(func() { telemetryStartedHook.Store(nil) })

	// 2 MB of base64 in 76-column lines: about 27,000 lines, which at 2ms a
	// line held wait for close to a minute.
	raw := make([]byte, 1536*1024)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	var src strings.Builder
	for len(encoded) > 0 {
		n := min(76, len(encoded))
		src.WriteString(encoded[:n])
		src.WriteByte('\n')
		encoded = encoded[n:]
	}
	want := src.String()
	srcPath := filepath.Join(t.TempDir(), "payload.b64")
	if err := os.WriteFile(srcPath, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	p, err := pm.ExecuteProcess("cat "+srcPath, "", "", nil, true, 0, nil, false, 0, false, false)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("wait returned after %v for %d bytes", elapsed, len(want))
	if elapsed > time.Second {
		t.Fatalf("wait took %v for %d bytes with a slow log sink, want well under a second", elapsed, len(want))
	}
	if p.Status != StatusCompleted || p.ExitCode != 0 {
		t.Fatalf("status %s exit %d", p.Status, p.ExitCode)
	}

	// The log file holds every byte.
	got, err := os.ReadFile(p.StdoutFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("stdout file has %d bytes, want %d (equal: false)", len(got), len(want))
	}

	// The output API returns the file's tail up to the logs response limit,
	// as before. (The 64 KiB inline cap of an exec response is applied by the
	// HTTP handler, which this change does not touch.)
	out, err := pm.GetProcessOutput(p.PID)
	if err != nil {
		t.Fatal(err)
	}
	if body := strings.TrimPrefix(out.Stdout, truncationMarker); !strings.HasSuffix(want, body) || int64(len(body)) != min(maxLogsResponse(), int64(len(want))) {
		t.Fatalf("inline stdout is %d bytes and not the output's tail", len(body))
	}

	// /logs replays the whole output from the journal.
	w := &captureWriter{}
	if err := pm.StreamProcessOutput(p.PID, w); err != nil {
		t.Fatal(err)
	}
	var replayed strings.Builder
	for _, line := range strings.SplitAfter(w.String(), "\n") {
		replayed.WriteString(strings.TrimPrefix(line, "stdout:"))
	}
	if replayed.String() != want {
		t.Fatalf("/logs replayed %d bytes, want %d", replayed.Len(), len(want))
	}

	// The exporter ends once it has logged what it queued, and reports the rest.
	telsMu.Lock()
	started := append([]*processTelemetry(nil), tels...)
	telsMu.Unlock()
	if len(started) != 1 {
		t.Fatalf("%d exporters started, want 1", len(started))
	}
	tel := started[0]
	waitExporter(t, tel, 10*time.Second)
	if tel.droppedQueueFull+tel.droppedBudget == 0 {
		t.Fatal("no line was dropped, so the sink was not the bottleneck this test needs")
	}
	if n := len(sink.lines("lines dropped")); n != 1 {
		t.Fatalf("%d drop summaries, want 1", n)
	}
	t.Logf("exported %d lines, dropped %d (queue full) + %d (budget)", tel.loggedLine, tel.droppedQueueFull, tel.droppedBudget)
}

var _ io.Writer = (*logSink)(nil)
