package process

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type structuredCapture struct {
	mu     sync.Mutex
	events []StreamEvent
	hook   func()
}

func (*structuredCapture) IsJSONStreamWriter() bool      { return true }
func (*structuredCapture) RequireStructuredLogs() bool   { return true }
func (w *structuredCapture) Write(p []byte) (int, error) { return len(p), nil }
func (w *structuredCapture) WriteEvent(kind, data string) (int, error) {
	w.mu.Lock()
	w.events = append(w.events, StreamEvent{Type: kind, Data: data})
	hook := w.hook
	w.hook = nil
	w.mu.Unlock()
	if hook != nil {
		hook()
	}
	return len(data), nil
}
func structuredFixture(t *testing.T) (*ProcessManager, *ProcessInfo, func(string, []byte)) {
	t.Helper()
	p := newTestProcess(&captureWriter{})
	p.LogFormat = structuredLogFormat
	p.LogFile = filepath.Join(t.TempDir(), "combined")
	p.Finished = make(chan struct{})
	close(p.Finished)
	combined, err := os.Create(p.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { combined.Close() })
	pm := NewProcessManager()
	pm.processes[p.PID] = p
	feed := func(kind string, data []byte) {
		t.Helper()
		f, err := os.CreateTemp(t.TempDir(), "input")
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err = f.Write(data); err != nil {
			t.Fatal(err)
		}
		f.Seek(0, io.SeekStart)
		buf := make([]byte, 4096)
		for pm.readAndBroadcast(f, buf, p, kind, combined) > 0 {
		}
	}
	return pm, p, feed
}
func TestStructuredReplayPreservesPartialStreamsAndBytes(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	feed("stdout", []byte("Name?"))
	feed("stderr", []byte("oops\n"))
	feed("stdout", []byte("Bob\n"))
	feed("stdout", []byte{0xf0, 0x9f})
	feed("stdout", []byte{0x98, 0x80, 0xff, 0})
	w := &structuredCapture{}
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	want := []StreamEvent{{"stdout", "Name?"}, {"stderr", "oops\n"}, {"stdout", "Bob\n"}, {"stdout", string([]byte{0xf0, 0x9f})}, {"stdout", string([]byte{0x98, 0x80, 0xff, 0})}}
	if !reflect.DeepEqual(w.events, want) {
		t.Fatalf("events=%#v want %#v", w.events, want)
	}
	text := &captureWriter{}
	if err := pm.StreamProcessOutputForProcess(p, text); err != nil {
		t.Fatal(err)
	}
	wantText := "stdout:Name?stderr:oops\nBob\nstdout:" + string([]byte{0xf0, 0x9f, 0x98, 0x80, 0xff, 0})
	if text.String() != wantText {
		t.Fatalf("text bytes changed: %q", text.String())
	}
}
func TestStructuredReplayQueuesLiveWithoutDuplicates(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	feed("stdout", []byte("before"))
	w := &structuredCapture{}
	w.hook = func() { feed("stderr", []byte("during")) }
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	feed("stdout", []byte("after"))
	want := []StreamEvent{{"stdout", "before"}, {"stderr", "during"}, {"stdout", "after"}}
	if !reflect.DeepEqual(w.events, want) {
		t.Fatalf("events=%#v", w.events)
	}
}
func TestStructuredRetentionDropsPartialRecord(t *testing.T) {
	_, p, feed := structuredFixture(t)
	feed("stdout", bytes.Repeat([]byte("a"), 4000))
	feed("stderr", []byte("kept\n"))
	st, _ := os.Stat(p.LogFile)
	var events []logRecord
	gaps := 0
	if err := readLogRecords(p.LogFile, st.Size(), 300, func(r logRecord) { events = append(events, r) }, func() { gaps++ }); err != nil {
		t.Fatal(err)
	}
	if gaps == 0 || len(events) != 1 || string(events[0].Data) != "kept\n" {
		t.Fatalf("gap=%d records=%#v", gaps, events)
	}
	f, _ := os.OpenFile(p.LogFile, os.O_WRONLY, 0600)
	_, err := f.WriteAt(make([]byte, 4096), 0)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	events = nil
	gaps = 0
	if err := readLogRecords(p.LogFile, st.Size(), st.Size(), func(r logRecord) { events = append(events, r) }, func() { gaps++ }); err != nil {
		t.Fatal(err)
	}
	if gaps == 0 || len(events) != 1 || events[0].Type != "stderr" {
		t.Fatalf("hole gap=%d records=%#v", gaps, events)
	}
}
func TestStructuredLegacyRejectedWithoutAttaching(t *testing.T) {
	pm := NewProcessManager()
	p := newTestProcess(&captureWriter{})
	before := len(p.logWriters)
	if err := pm.StreamProcessOutputForProcess(p, &structuredCapture{}); err != ErrLegacyLogFormat {
		t.Fatalf("err=%v", err)
	}
	if len(p.logWriters) != before {
		t.Fatal("attached to unsupported legacy log")
	}
}
func TestStructuredStateRestoresFormatAndCollectedPosition(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	p.Status = StatusCompleted
	p.ProcessPid = -1
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	feed("stdout", []byte("before"))
	if err := pm.SaveState(); err != nil {
		t.Fatal(err)
	}
	feed("stderr", []byte("after\n"))
	restored := NewProcessManager()
	if err := restored.LoadState(); err != nil {
		t.Fatal(err)
	}
	got := restored.processes[p.PID]
	if !got.SupportsStructuredLogs() || got.stdout.Len() != 6 || got.stderr.Len() != 6 || !got.stdoutMidLine || got.stderrMidLine {
		t.Fatalf("incorrect restored position")
	}
	w := &structuredCapture{}
	if err := restored.StreamProcessOutputForProcess(got, w); err != nil {
		t.Fatal(err)
	}
	if len(w.events) != 2 {
		t.Fatalf("events=%#v", w.events)
	}
}

func TestStructuredCollectorOwnershipAndTakeover(t *testing.T) {
	dir := t.TempDir()
	stdout := filepath.Join(dir, "stdout")
	stderr := filepath.Join(dir, "stderr")
	combined := filepath.Join(dir, "combined")
	for _, path := range []string{stdout, stderr, combined} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	makeProcess := func() *ProcessInfo {
		p := newTestProcess(&captureWriter{})
		p.LogFormat = structuredLogFormat
		p.LogFile = combined
		p.StdoutFile = stdout
		p.StderrFile = stderr
		p.Done = make(chan struct{})
		p.TailDone = make(chan struct{})
		return p
	}
	first, second := makeProcess(), makeProcess()
	pm1, pm2 := NewProcessManager(), NewProcessManager()
	appendSource := func(s string) {
		f, err := os.OpenFile(stdout, os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString(s)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	appendSource("first\n")
	go pm1.tailLogFiles(first)
	waitFor(t, "first collection", func() bool { first.logLock.RLock(); defer first.logLock.RUnlock(); return first.stdout.Len() == 6 })
	go pm2.tailLogFiles(second)
	time.Sleep(100 * time.Millisecond)
	second.logLock.RLock()
	n := second.stdout.Len()
	second.logLock.RUnlock()
	if n != 0 {
		t.Fatal("second collector bypassed ownership")
	}
	close(first.Done)
	<-first.TailDone
	// A per-run tail ending must not give an upgrade validator ownership
	// during the automatic restart delay.
	time.Sleep(100 * time.Millisecond)
	second.logLock.RLock()
	n = second.stdout.Len()
	second.logLock.RUnlock()
	if n != 0 {
		t.Fatal("ownership escaped between process runs")
	}
	first.releaseJournalOwnership()
	appendSource("second\n")
	waitFor(t, "takeover", func() bool { second.logLock.RLock(); defer second.logLock.RUnlock(); return second.stdout.Len() == 13 })
	close(second.Done)
	<-second.TailDone
	second.releaseJournalOwnership()
	var joined string
	st, _ := os.Stat(combined)
	if err := readLogRecords(combined, st.Size(), st.Size(), func(r logRecord) { joined += string(r.Data) }, func() { t.Error("unexpected gap") }); err != nil {
		t.Fatal(err)
	}
	if joined != "first\nsecond\n" {
		t.Fatalf("duplicated/lost output: %q", joined)
	}
}

func TestStructuredRestartReplaysEachRunAndNoticeOnce(t *testing.T) {
	pm := newStdinTestManager(t)
	command := `if [ ! -f "$MARKER" ]; then touch "$MARKER"; printf first; exit 1; fi; printf second`
	p, err := pm.ExecuteProcess("sh -c '"+command+"'", "", "structured-restart", map[string]string{"MARKER": filepath.Join(t.TempDir(), "once")}, false, 0, nil, true, 1, false, false)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.Finished:
	case <-time.After(10 * time.Second):
		t.Fatal("restart did not finish")
	}
	w := &structuredCapture{}
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	var out string
	notices := 0
	for _, e := range w.events {
		if e.Type == "stdout" {
			out += e.Data
		}
		if e.Type == "restart" && strings.Contains(e.Data, "Attempting restart") {
			notices++
		}
	}
	if out != "firstsecond" || notices != 1 {
		t.Fatalf("incorrect replay: %#v", w.events)
	}
}

func TestStructuredWriteFailureIsVisible(t *testing.T) {
	_, p, _ := structuredFixture(t)
	w := &structuredCapture{}
	p.logWriters = []io.Writer{newPendingWriter(w)}
	p.persistLogRecord(nil, "stdout", []byte("lost"), true)
	p.logWriters[0].(*pendingWriter).release()
	if !p.logIncomplete || len(w.events) != 1 || w.events[0].Type != "truncated" {
		t.Fatalf("invisible write failure: %#v", w.events)
	}
}

func TestStructuredCorruptRecordFailsReplay(t *testing.T) {
	pm, p, _ := structuredFixture(t)
	if err := os.WriteFile(p.LogFile, []byte("{invalid}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w := &structuredCapture{}
	before := len(p.logWriters)
	if err := pm.StreamProcessOutputForProcess(p, w); err == nil {
		t.Fatal("corrupt record accepted")
	}
	if len(p.logWriters) != before {
		t.Fatal("failed replay left writer attached")
	}
}

func TestStructuredKeepaliveIsNotOutput(t *testing.T) {
	w := &structuredCapture{}
	if err := writeLogKeepalive(w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.events, []StreamEvent{{Type: "keepalive"}}) {
		t.Fatalf("keepalive=%#v", w.events)
	}
	plain := &captureWriter{}
	if err := writeLogKeepalive(plain); err != nil {
		t.Fatal(err)
	}
	if plain.String() != "[keepalive]\n" {
		t.Fatalf("legacy keepalive changed: %q", plain.String())
	}
}

func TestStructuredRestoreDrainsOutputWrittenWhileOffline(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	p.Status = StatusRunning
	p.ProcessPid = -1
	p.StdoutFile = filepath.Join(t.TempDir(), "stdout")
	p.StderrFile = filepath.Join(t.TempDir(), "stderr")
	if err := os.WriteFile(p.StdoutFile, []byte("beforeafter"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.StderrFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	feed("stdout", []byte("before"))
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	if err := pm.SaveState(); err != nil {
		t.Fatal(err)
	}
	restored := NewProcessManager()
	if err := restored.LoadState(); err != nil {
		t.Fatal(err)
	}
	got := restored.processes[p.PID]
	select {
	case <-got.Finished:
	case <-time.After(3 * time.Second):
		t.Fatal("offline output was not drained")
	}
	w := &structuredCapture{}
	if err := restored.StreamProcessOutputForProcess(got, w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.events, []StreamEvent{{"stdout", "before"}, {"stdout", "after"}}) {
		t.Fatalf("offline replay=%#v", w.events)
	}
}

func TestStructuredMissingJournalFailsBeforeAttach(t *testing.T) {
	pm, p, _ := structuredFixture(t)
	if err := os.Remove(p.LogFile); err != nil {
		t.Fatal(err)
	}
	before := len(p.logWriters)
	if err := pm.StreamProcessOutputForProcess(p, &structuredCapture{}); err == nil {
		t.Fatal("missing journal accepted")
	}
	if len(p.logWriters) != before {
		t.Fatal("writer attached despite missing journal")
	}
}

func TestStructuredReadinessBlocksAttachAndHonorsCancellation(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	p.logReady = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.WaitForLogCollector(ctx); err != context.Canceled {
		t.Fatalf("cancel error=%v", err)
	}
	w := &structuredCapture{}
	done := make(chan error, 1)
	go func() { done <- pm.StreamProcessOutputForProcess(p, w) }()
	select {
	case err := <-done:
		t.Fatalf("attached before collector ready: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	feed("stdout", []byte("old owner's last bytes"))
	p.finishLogReady(nil)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("attach never resumed")
	}
	if !reflect.DeepEqual(w.events, []StreamEvent{{"stdout", "old owner's last bytes"}}) {
		t.Fatalf("lost handoff data: %#v", w.events)
	}
}

func TestStructuredRepairsPartialFinalRecordAndRecollectsSource(t *testing.T) {
	pm, p, feed := structuredFixture(t)
	feed("stdout", []byte("before"))
	p.StdoutFile = filepath.Join(t.TempDir(), "stdout")
	p.StderrFile = filepath.Join(t.TempDir(), "stderr")
	os.WriteFile(p.StdoutFile, []byte("beforeafter"), 0600)
	os.WriteFile(p.StderrFile, nil, 0600)
	f, err := os.OpenFile(p.LogFile, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"type":"stdout","data":"YW`)
	f.Close()
	// Saved counters can be ahead of the last durable complete record.
	p.stdout.Write([]byte("after"))
	p.Done = make(chan struct{})
	close(p.Done)
	p.TailDone = make(chan struct{})
	pm.tailLogFiles(p)
	p.releaseJournalOwnership()
	w := &structuredCapture{}
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(w.events, []StreamEvent{{"stdout", "before"}, {"stdout", "after"}}) {
		t.Fatalf("repair duplicated/lost bytes: %#v", w.events)
	}
}

func TestStructuredRestartWaitsForRestoreFinalization(t *testing.T) {
	pm := NewProcessManager()
	previousDone := make(chan struct{})
	close(previousDone)
	previousTail := make(chan struct{})
	close(previousTail)
	p := &ProcessInfo{Command: "true", WorkingDir: filepath.Join(t.TempDir(), "missing"), Done: previousDone, TailDone: previousTail, Finished: make(chan struct{}), restoreFinalized: make(chan struct{})}
	p.markFinished()
	result := make(chan error, 1)
	go func() { _, err := pm.restartProcess(p, nil); result <- err }()
	select {
	case err := <-result:
		t.Fatalf("restart crossed unfinished restore: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	pm.mu.RLock()
	same := p.Done == previousDone && p.TailDone == previousTail
	pm.mu.RUnlock()
	if !same {
		t.Fatal("replaced lifecycle channels before restore finalization")
	}
	close(p.restoreFinalized)
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("expected missing working directory error")
		}
	case <-time.After(time.Second):
		t.Fatal("restart remained blocked after finalization")
	}
	select {
	case <-p.Finished:
		t.Fatal("new run inherited closed Finished")
	default:
	}
}

func TestStructuredSameNameProcessesKeepSeparateLogs(t *testing.T) {
	pm := newStdinTestManager(t)
	oldPID, err := pm.StartProcessWithName("printf old-start; sleep 0.3; printf old-end", "", "same-name", nil, false, 0, false, 0, false, noop)
	if err != nil {
		t.Fatal(err)
	}
	old, _ := pm.GetProcessByIdentifier(oldPID)
	t.Cleanup(func() { _ = pm.KillProcess(oldPID) })
	waitFor(t, "old output collected", func() bool { old.logLock.RLock(); defer old.logLock.RUnlock(); return old.stdout.Len() > 0 })
	newPID, err := pm.StartProcessWithName("printf new-output", "", "same-name", nil, false, 0, false, 0, false, noop)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _ := pm.GetProcessByIdentifier(newPID)
	t.Cleanup(func() { _ = pm.KillProcess(newPID) })
	for _, p := range []*ProcessInfo{old, fresh} {
		select {
		case <-p.Finished:
		case <-time.After(5 * time.Second):
			t.Fatal("same-name process did not finish")
		}
	}
	for _, tc := range []struct {
		p    *ProcessInfo
		want string
	}{{old, "old-startold-end"}, {fresh, "new-output"}} {
		w := &structuredCapture{}
		if err := pm.StreamProcessOutputForProcess(tc.p, w); err != nil {
			t.Fatal(err)
		}
		var got string
		for _, event := range w.events {
			if event.Type != "stdout" {
				t.Fatalf("unexpected event %#v", event)
			}
			got += event.Data
		}
		if got != tc.want {
			t.Fatalf("process %s replay=%q want %q", tc.p.PID, got, tc.want)
		}
		snapshot, ok := pm.GetProcessSnapshot(tc.p.PID)
		if !ok {
			t.Fatal("process missing")
		}
		if got := snapshot.OutputTail(1024).Stdout; got != tc.want {
			t.Fatalf("snapshot=%q want %q", got, tc.want)
		}
	}
	if old.LogFile == fresh.LogFile || old.StdoutFile == fresh.StdoutFile || old.StderrFile == fresh.StderrFile {
		t.Fatal("same-name instances share log paths")
	}
}

func TestStructuredFailedStartRemovesAllLogFiles(t *testing.T) {
	pm := newStdinTestManager(t)
	t.Setenv("SHELL", filepath.Join(t.TempDir(), "missing-shell"))
	if _, err := pm.StartProcess("true", "", nil, false, 0, false, 0, true, noop); err == nil {
		t.Fatal("missing shell unexpectedly started")
	}
	entries, err := os.ReadDir(ProcessLogDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed startup leaked %d log files: %v", len(entries), entries)
	}
}

func TestStructuredRetentionPreservesRecordAtPunchedBoundary(t *testing.T) {
	_, p, feed := structuredFixture(t)
	feed("stdout", []byte("discarded"))
	before, err := os.Stat(p.LogFile)
	if err != nil {
		t.Fatal(err)
	}
	feed("stderr", []byte("retained\n"))
	info, _ := os.Stat(p.LogFile)
	file, err := os.OpenFile(p.LogFile, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.WriteAt(make([]byte, before.Size()), 0); err != nil {
		t.Fatal(err)
	}
	file.Close()
	var records []logRecord
	gaps := 0
	if err := readLogRecords(p.LogFile, info.Size(), info.Size(), func(r logRecord) { records = append(records, r) }, func() { gaps++ }); err != nil {
		t.Fatal(err)
	}
	if gaps != 1 || len(records) != 1 || records[0].Type != "stderr" || string(records[0].Data) != "retained\n" {
		t.Fatalf("gap=%d retained=%#v", gaps, records)
	}
}

func TestStructuredPartialLogCreationRemovesOnlyCreatedFiles(t *testing.T) {
	for _, conflict := range []int{0, 1, 2} {
		t.Run(fmt.Sprint(conflict), func(t *testing.T) {
			dir := t.TempDir()
			paths := []string{filepath.Join(dir, "stdout"), filepath.Join(dir, "stderr"), filepath.Join(dir, "combined")}
			if err := os.WriteFile(paths[conflict], []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := openProcessLogFiles(paths...); err == nil {
				t.Fatal("expected exclusive-create failure")
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 1 {
				t.Fatalf("partial creation leaked files: %v", entries)
			}
			body, err := os.ReadFile(paths[conflict])
			if err != nil || string(body) != "existing" {
				t.Fatalf("preexisting file changed: %q %v", body, err)
			}
		})
	}
}
