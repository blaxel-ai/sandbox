package process

import (
	"errors"
	"io"
	"os"
)

var errLogRetentionGap = errors.New("process log retention overtook the reader")

type retainedRead struct {
	n       int
	start   int64
	skipped bool
}

// readRetained reads one bounded chunk under the lock shared with retention.
// It returns the absolute start position so callers don't reconstruct it later.
// The caller owns its descriptor. The lock is released before broadcasting.
func readRetained(file *os.File, buf []byte) (retainedRead, error) {
	if err := lockLogFile(file, false); err != nil {
		return retainedRead{}, err
	}
	defer unlockLogFile(file)
	head, err := retainedHead(file)
	if err != nil {
		return retainedRead{}, err
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return retainedRead{}, err
	}
	result := retainedRead{start: offset}
	if offset < head {
		if _, err = file.Seek(head, io.SeekStart); err != nil {
			return retainedRead{}, err
		}
		result.start = head
		result.skipped = true
	}
	result.n, err = file.Read(buf)
	return result, err
}

// recordLogGap keeps cursor, buffered-tail and line state changes together.
func (p *ProcessInfo) recordLogGap(source string, read retainedRead) {
	p.logLock.Lock()
	defer p.logLock.Unlock()
	if read.skipped || !p.logIncomplete {
		for _, writer := range p.logWriters {
			writeStreamGap(writer)
		}
	}
	p.logIncomplete = true
	if !read.skipped {
		return
	}
	stream := p.stdout
	if source == "stderr" {
		stream = p.stderr
		p.stderrMidLine = false
	} else {
		p.stdoutMidLine = false
	}
	skipped := stream.skipTo(int(read.start))
	p.logs.skipTo(p.logs.Len() + skipped)
}

// retainedReader stops replay if retention overtakes it between chunks.
// Already buffered bytes remain valid. Never splice a partial record across a gap.
type retainedReader struct{ file *os.File }

func (r retainedReader) Read(buf []byte) (int, error) {
	read, err := readRetained(r.file, buf)
	if read.skipped {
		return 0, errLogRetentionGap
	}
	return read.n, err
}
