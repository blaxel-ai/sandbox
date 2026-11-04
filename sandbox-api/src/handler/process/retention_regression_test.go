package process

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Exercise the sweeper with the collector still at offset zero. Retention must
// report discarded output as a gap instead of returning fabricated bytes.
func TestRetentionOvertakesCollectorExplicitly(t *testing.T) {
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream, func(t *testing.T) { testRetentionOvertakesCollector(t, stream) })
	}
}

func testRetentionOvertakesCollector(t *testing.T, stream string) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux hole punching")
	}
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(64*1024))
	pm, p, feed := structuredFixture(t)
	prefix := []byte("before gap")
	feed(stream, prefix)
	source := append(prefix, bytes.Repeat([]byte{0, 1, 2, 255, '\n', 0}, 128*1024)...)
	path := filepath.Join(t.TempDir(), stream)
	if stream == "stdout" {
		p.StdoutFile = path
	} else {
		p.StderrFile = path
	}
	if err := os.WriteFile(path, source, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Seek(int64(len(prefix)), 0); err != nil {
		t.Fatal(err)
	}
	journal, err := os.OpenFile(p.LogFile, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	w := &structuredCapture{}
	p.logWriters = append(p.logWriters, w)
	pm.CapLogFiles()
	buf := make([]byte, 4096)
	for pm.readAndBroadcast(f, buf, p, stream, journal) > 0 {
	}
	var actual []byte
	gap := false
	for _, event := range w.events {
		if event.Type == "truncated" {
			gap = true
		}
		if event.Type == stream {
			actual = append(actual, []byte(event.Data)...)
		}
	}
	if !gap {
		t.Error("retention dropped unread output without a truncation event")
	}
	if len(actual) == 0 || len(actual) >= len(source) || !bytes.Equal(actual, source[len(source)-len(actual):]) {
		t.Fatalf("collector returned fabricated bytes: got %d bytes, source %d", len(actual), len(source))
	}
	buffer := p.stdout
	if stream == "stderr" {
		buffer = p.stderr
	}
	if buffer.Len() != len(source) {
		t.Fatalf("source cursor=%d want %d", buffer.Len(), len(source))
	}
	if p.logs.Len() != len(source) {
		t.Fatalf("combined cursor=%d want %d", p.logs.Len(), len(source))
	}
	tail := actual[max(0, len(actual)-buffer.max):]
	if !bytes.Equal([]byte(buffer.tail()), tail) || !bytes.Equal([]byte(p.logs.tail()), tail) {
		t.Fatal("retention left stale bytes in buffered output")
	}
}

func TestLogTailPreservesWrittenNULs(t *testing.T) {
	for _, data := range [][]byte{{0, 0, 1, 0, 255}, make([]byte, 8192)} {
		path := filepath.Join(t.TempDir(), "stdout")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		got, ok := readLogTail(path, 64*1024)
		if !ok || !bytes.Equal([]byte(got), data) {
			t.Fatalf("literal NUL bytes changed: got %d bytes, want %d", len(got), len(data))
		}
	}
}

func TestReplayRetentionDuringDeliveryIsExplicit(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux hole punching")
	}
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", fmt.Sprint(1024*1024))
	pm, p, feed := structuredFixture(t)
	data := bytes.Repeat([]byte("hello\n"), 10000)
	feed("stdout", data)
	w := &structuredCapture{}
	w.hook = func() { t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096"); pm.CapLogFiles() }
	if err := pm.StreamProcessOutputForProcess(p, w); err != nil {
		t.Fatal(err)
	}
	gap := false
	var got []byte
	for _, e := range w.events {
		if e.Type == "truncated" {
			gap = true
		}
		if e.Type == "stdout" {
			got = append(got, []byte(e.Data)...)
		}
	}
	if !gap {
		t.Fatal("concurrent retention was not reported")
	}
	if !bytes.Equal(got, data[:len(got)]) {
		t.Fatal("replay fabricated bytes")
	}
	feed("stderr", []byte("after"))
	if w.events[len(w.events)-1].Data != "after" {
		t.Fatal("live writer was not released")
	}
}
