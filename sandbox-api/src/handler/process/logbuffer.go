package process

import (
	"os"
	"strconv"
)

// maxInMemoryLogBytes caps what one stream of one process keeps in memory.
//
// A sandbox has no swap: every byte a process writes used to stay in the API's
// heap forever (once in the stream's buffer, once in the combined one), so a
// chatty workload grew sandbox-api until it, rather than the workload, was the
// fattest process in the guest and the OOM killer's pick - taking the whole
// sandbox down. The full output lives in the process' log files, which is what
// GET /process/{id}/logs reads, so memory only has to hold the tail as a
// fallback for the (rare) case where the files are unavailable.
const maxInMemoryLogBytes = 256 * 1024

func maxInMemoryLog() int {
	if raw := os.Getenv("SANDBOX_MAX_IN_MEMORY_LOG_BYTES"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			return n
		}
	}
	return maxInMemoryLogBytes
}

// logBuffer keeps the last maxInMemoryLog() bytes written to it and counts
// everything that went through, so callers that use the count as an offset into
// the log file stay correct after the head has been dropped.
// Not safe for concurrent use: callers hold ProcessInfo.logLock.
type logBuffer struct {
	buf     []byte
	max     int
	written int
}

func newLogBuffer() *logBuffer {
	return &logBuffer{max: maxInMemoryLog()}
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.written += len(p)
	if b.max == 0 {
		return len(p), nil
	}
	if len(p) >= b.max {
		b.buf = append(b.buf[:0], p[len(p)-b.max:]...)
		return len(p), nil
	}
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.max {
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.max:]...)
	}
	return len(p), nil
}

func (b *logBuffer) WriteString(s string) (int, error) {
	return b.Write([]byte(s))
}

// String returns the buffered tail. It is prefixed with a marker when the head
// was dropped, so a client cannot mistake the tail for the whole output.
func (b *logBuffer) String() string {
	if b.written > len(b.buf) {
		return "[... truncated, see the process log file for the full output ...]\n" +
			string(b.buf)
	}
	return string(b.buf)
}

// Len is the number of bytes ever written, not the number buffered.
func (b *logBuffer) Len() int {
	return b.written
}

// streamOffset is where a restored stream resumes reading its log file: the
// byte count if the state has one, else the saved output's length (state files
// written before the count existed held the whole output).
func streamOffset(writtenBytes int, saved string) int {
	if writtenBytes > 0 {
		return writtenBytes
	}
	return len(saved)
}

// restore seeds the buffer from a saved state: tail is what is left of the
// output, written is how much the stream had produced in total, so the log file
// is re-read from where the previous run left off rather than from the tail.
func (b *logBuffer) restore(tail string, written int) {
	b.Write([]byte(tail))
	if written > b.written {
		b.written = written
	}
}
