package process

import (
	"io"
	"os"
	"strconv"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

// maxLogFileBytes caps what one log file of one process keeps on disk.
//
// The sandbox root is a tmpfs, so log files cost RAM as surely as the API's own
// heap does: a process writing output in a loop would fill the guest and get
// something OOM-killed. Past the cap the head of the file is released and only
// the last maxLogFileBytes are kept.
const maxLogFileBytes = 32 * 1024 * 1024

func maxLogFile() int64 {
	if raw := os.Getenv("SANDBOX_MAX_LOG_FILE_BYTES"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return maxLogFileBytes
}

// capLogFile releases the head of an oversized log file, keeping the last
// maxLogFile() bytes readable.
//
// The workload writes to these files itself, through a file descriptor this
// process does not have (that is what lets it survive an API restart), so the
// file cannot be rotated or rewritten underneath it: the write offset would
// keep growing and later reads would find a hole where the new head should be.
// Instead the head is punched out, which frees the tmpfs pages while leaving
// every offset in the file where the workload expects it. The punched range
// reads back as zero bytes, so it must always stay behind what readers ask for
// - hence the tail is kept whole and only the head, rounded down to a page
// boundary, is released.
func capLogFile(path string) {
	max := maxLogFile()
	if path == "" || max == 0 {
		return
	}

	file, err := os.OpenFile(path, os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return
	}

	// Keep a whole cap's worth of tail, and only punch entire pages: a partial
	// page cannot be freed anyway.
	head := (stat.Size() - max) & ^int64(pageSize-1)
	if head <= 0 {
		return
	}

	if err := unix.Fallocate(int(file.Fd()),
		unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, 0, head); err != nil {
		logrus.WithError(err).WithField("file", path).
			Debug("Failed to release the head of an oversized process log file")
		return
	}

	logrus.WithFields(logrus.Fields{
		"file":     path,
		"released": head,
		"size":     stat.Size(),
	}).Info("Released the head of an oversized process log file")
}

var pageSize = os.Getpagesize()

// MaxInlinedLogBytes is how much of a process' output GET /process inlines per
// stream, per process: enough to be useful, small enough that listing a hundred
// chatty processes cannot exhaust the guest's memory. The whole output stays
// available from GET /process/{id}/logs.
const MaxInlinedLogBytes = 64 * 1024

// maxLogLineBytes caps a single line when replaying a log file, so one line
// without a newline in it cannot be read into memory without bound.
const maxLogLineBytes = 1024 * 1024

// openLogTail opens a log file positioned on the last max bytes, past any head
// capLogFile has released. truncated says whether anything was skipped, so the
// caller can say the output is partial. The caller closes the file.
func openLogTail(path string, max int64) (file *os.File, truncated bool, err error) {
	file, err = os.Open(path)
	if err != nil {
		return nil, false, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, false, err
	}

	if max <= 0 || stat.Size() <= max {
		return file, false, nil
	}

	if _, err := file.Seek(stat.Size()-max, io.SeekStart); err != nil {
		file.Close()
		return nil, false, err
	}
	return file, true, nil
}

// readLogTail returns the last max bytes of a log file, skipping any head that
// capLogFile has already released, so a caller never holds more than the cap in
// memory. ok is false when the file cannot be read at all.
func readLogTail(path string, max int64) (string, bool) {
	if path == "" {
		return "", false
	}

	file, truncated, err := openLogTail(path, max)
	if err != nil {
		return "", false
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", false
	}

	// A file whose head was released reads back as zeros; a file we seeked past
	// is missing its head. Either way say so rather than handing back a tail
	// that looks like the whole output.
	if truncated {
		return truncationMarker + string(content), true
	}
	return string(trimReleasedHead(content)), true
}

// trimReleasedHead drops the zero bytes a punched-out head reads back as.
func trimReleasedHead(content []byte) []byte {
	head := 0
	for head < len(content) && content[head] == 0 {
		head++
	}
	if head == 0 {
		return content
	}
	return append([]byte(truncationMarker), content[head:]...)
}
