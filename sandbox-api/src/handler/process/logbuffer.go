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
// sandbox down. Output is served from the process' log files instead, so this
// buffer is only the fallback for when a file cannot be read, and small enough
// that many processes still cost the API next to nothing.
const maxInMemoryLogBytes = 64 * 1024

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

// String returns the buffered tail for a client. It is prefixed with a marker
// when the head was dropped, so the tail cannot be mistaken for the whole
// output. Use tail() for anything that round-trips through the buffer.
func (b *logBuffer) String() string {
	if b.written > len(b.buf) {
		return truncationMarker + b.tail()
	}
	return b.tail()
}

const truncationMarker = "[... truncated, see the process log file for the full output ...]\n"

// tail is the buffered bytes as they were written, with no marker: what a
// saved state holds, so restoring it does not accumulate a marker per restart.
func (b *logBuffer) tail() string {
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

// resume records that the stream has produced offset bytes in total, for when a
// bounded read skipped over output that only exists in the log file.
func (b *logBuffer) resume(offset int) {
	if offset > b.written {
		b.written = offset
	}
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
