package process

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

const structuredLogFormat = "jsonl-v1"
const maxStructuredRecordBytes = 16 * 1024

var ErrLegacyLogFormat = errors.New("structured logs are unavailable for this process; start a new process")

// StructuredLogWriter opts into source-preserving replay and typed gap events.
type StructuredLogWriter interface{ RequireStructuredLogs() bool }

func requiresStructuredLogs(w io.Writer) bool {
	w = unwrapWriter(w)
	s, ok := w.(StructuredLogWriter)
	return ok && s.RequireStructuredLogs()
}

// SupportsStructuredLogs is immutable for the lifetime of a process.
func (p *ProcessInfo) SupportsStructuredLogs() bool { return p.LogFormat == structuredLogFormat }

// []byte is base64 in JSON, preserving arbitrary output including split UTF-8.
// Offsets allow recovery after a state snapshot without replaying collected bytes.
type logRecord struct {
	RawText       bool   `json:"rawText,omitempty"`
	Type          string `json:"type"`
	Data          []byte `json:"data"`
	LineStart     bool   `json:"lineStart"`
	StdoutOffset  int    `json:"stdoutOffset"`
	StderrOffset  int    `json:"stderrOffset"`
	StdoutMidLine bool   `json:"stdoutMidLine,omitempty"`
	StderrMidLine bool   `json:"stderrMidLine,omitempty"`
}

func prefixedRecord(r logRecord) []byte {
	if r.RawText {
		return r.Data
	}
	out := make([]byte, 0, len(r.Data)+16)
	start := r.LineStart
	for rest := r.Data; len(rest) > 0; {
		line := rest
		if n := bytes.IndexByte(rest, '\n'); n >= 0 {
			line, rest = rest[:n+1], rest[n+1:]
		} else {
			rest = nil
		}
		if start {
			out = append(out, r.Type...)
			out = append(out, ':')
		}
		out = append(out, line...)
		start = line[len(line)-1] == '\n'
	}
	return out
}

func writeStreamGap(w io.Writer) {
	if requiresStructuredLogs(w) {
		writeToLogWriter(w, "truncated", []byte("Output is incomplete"))
	} else {
		writeToLogWriter(w, "stdout", []byte(truncationMarker))
	}
}

// readLogRecords bounds both total replay and record size. Seeking or hole
// punching can cut a record: discard it, never infer its source from raw text.
func readLogRecords(path string, end, max int64, emit func(logRecord), gap func()) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	start := int64(0)
	if max > 0 && end > max {
		start = end - max
	}
	if _, err = f.Seek(start, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(io.LimitReader(f, end-start))
	zeroHead := false
	for {
		chunk, _ := reader.Peek(reader.Size())
		zeros := len(chunk) - len(bytes.TrimLeft(chunk, "\x00"))
		if zeros > 0 {
			_, _ = reader.Discard(zeros)
			zeroHead = true
		}
		if zeros < len(chunk) || len(chunk) == 0 {
			break
		}
	}
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), maxStructuredRecordBytes)
	skip := start > 0 || zeroHead
	if skip {
		var prev [1]byte
		if !zeroHead {
			if _, err := f.ReadAt(prev[:], start-1); err == nil && prev[0] == '\n' {
				skip = false
			}
		}
		gap()
	}
	for s.Scan() {
		line := s.Bytes()
		if skip {
			// A punched head may end exactly at a record boundary. Its
			// preceding newline is gone, so validate before discarding it.
			skip = false
			var candidate logRecord
			if json.Unmarshal(line, &candidate) == nil && validLogRecord(candidate) {
				emit(candidate)
			}
			continue
		}
		if len(line) == 0 {
			continue
		}
		if line[0] == 0 {
			gap()
			continue
		}
		var r logRecord
		if err := json.Unmarshal(line, &r); err != nil || !validLogRecord(r) {
			return fmt.Errorf("invalid structured log record")
		}
		emit(r)
	}
	return s.Err()
}

func replayStructuredLogs(p *ProcessInfo, w io.Writer, end int64) error {
	return readLogRecords(p.LogFile, end, maxLogFile(), func(r logRecord) { writeLogRecord(w, r) }, func() { writeStreamGap(w) })
}

func restoreStructuredPosition(p *ProcessInfo) bool {
	st, err := os.Stat(p.LogFile)
	if err != nil {
		p.logIncomplete = true
		return false
	}
	var last *logRecord
	err = readLogRecords(p.LogFile, st.Size(), maxStructuredRecordBytes*2, func(r logRecord) { v := r; last = &v }, func() {})
	if err != nil {
		p.logIncomplete = true
		return false
	}
	if last != nil {
		restoreCollectedOffset(p.stdout, last.StdoutOffset)
		restoreCollectedOffset(p.stderr, last.StderrOffset)
		p.stdoutMidLine = last.StdoutMidLine
		p.stderrMidLine = last.StderrMidLine
	} else if st.Size() > 0 {
		p.logIncomplete = true
		return false
	} else if st.Size() == 0 {
		restoreCollectedOffset(p.stdout, 0)
		restoreCollectedOffset(p.stderr, 0)
		p.stdoutMidLine = false
		p.stderrMidLine = false
	}
	return true
}

// persistLogRecord runs under logLock, matching writer attachment with durable ordering.
func (p *ProcessInfo) persistLogRecord(file *os.File, kind string, data []byte, lineStart bool, rawText ...bool) {
	rawNotice := len(rawText) > 0 && rawText[0]
	raw, _ := json.Marshal(logRecord{RawText: rawNotice, Type: kind, Data: data, LineStart: lineStart, StdoutOffset: p.stdout.Len(), StderrOffset: p.stderr.Len(), StdoutMidLine: p.stdoutMidLine, StderrMidLine: p.stderrMidLine})
	raw = append(raw, '\n')
	var err error
	if file == nil {
		err = os.ErrNotExist
	} else {
		var n int
		n, err = file.Write(raw)
		if err == nil && n != len(raw) {
			err = io.ErrShortWrite
		}
	}
	if err != nil && !p.logIncomplete {
		p.logIncomplete = true
		for _, w := range p.logWriters {
			if pending, ok := w.(*pendingWriter); ok {
				if requiresStructuredLogs(pending.target) {
					writeToLogWriter(pending, "truncated", []byte("Output is incomplete"))
				}
			} else if requiresStructuredLogs(w) {
				writeStreamGap(w)
			}
		}
	}
}

// One collector owns a process journal, including during upgrade validation.
func acquireLogOwnership(file *os.File, done <-chan struct{}) bool {
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return true
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return false
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-done:
			timer.Stop()
			return false
		case <-timer.C:
		}
	}
}

// WaitForLogCollector prevents attachment between an old owner's final write
// and a restored collector's replay boundary. New processes need no gate.
func (p *ProcessInfo) WaitForLogCollector(ctx context.Context) error {
	if p.logReady == nil {
		return nil
	}
	select {
	case <-p.logReady:
		return p.logReadyErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *ProcessInfo) finishLogReady(err error) {
	if p.logReady == nil {
		return
	}
	p.logReadyOnce.Do(func() { p.logReadyErr = err; close(p.logReady) })
}

// A killed writer may leave a partial last append. Only the unterminated suffix
// is discarded; complete malformed records remain errors, never guessed data.
func repairJournalTail(f *os.File) error {
	st, err := f.Stat()
	if err != nil || st.Size() == 0 {
		return err
	}
	size := st.Size()
	start := size - int64(maxStructuredRecordBytes)
	if start < 0 {
		start = 0
	}
	buf := make([]byte, size-start)
	if _, err = f.ReadAt(buf, start); err != nil {
		return err
	}
	if buf[len(buf)-1] == '\n' {
		return nil
	}
	last := bytes.LastIndexByte(buf, '\n')
	if last < 0 && start > 0 {
		return errors.New("unterminated structured log record exceeds limit")
	}
	return f.Truncate(start + int64(last+1))
}
func restoreCollectedOffset(b *logBuffer, offset int) {
	if offset < b.written {
		dropped := b.written - offset
		if dropped >= len(b.buf) {
			b.buf = nil
		} else {
			b.buf = b.buf[:len(b.buf)-dropped]
		}
	}
	b.written = offset
}

func (p *ProcessInfo) releaseJournalOwnership() {
	p.logLock.Lock()
	defer p.logLock.Unlock()
	if p.journalOwner != nil {
		_ = p.journalOwner.Close()
		p.journalOwner = nil
	}
}

func writeLogRecord(w io.Writer, r logRecord) {
	if r.RawText && requiresStructuredLogs(w) {
		writeToLogWriter(w, "restart", r.Data)
		return
	}
	if jw, ok := w.(JSONStreamWriter); ok && jw.IsJSONStreamWriter() {
		writeChunkToLogWriter(w, r.Type, r.Data, nil)
		return
	}
	writeChunkToLogWriter(w, r.Type, r.Data, prefixedRecord(r))
}

func validLogRecord(r logRecord) bool {
	if len(r.Data) == 0 || r.StdoutOffset < 0 || r.StderrOffset < 0 {
		return false
	}
	switch r.Type {
	case "stdout":
		return r.StdoutOffset >= len(r.Data)
	case "stderr":
		return r.StderrOffset >= len(r.Data)
	default:
		return false
	}
}
