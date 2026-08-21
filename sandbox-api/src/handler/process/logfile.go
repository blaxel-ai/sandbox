package process

import (
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/sirupsen/logrus"
)

// maxLogFileBytes is the ceiling on what one log file of one process keeps on
// disk. The shared budget below can lower it further, it never raises it.
//
// The sandbox root is a tmpfs, so log files cost RAM as surely as the API's own
// heap does: a process writing output in a loop would fill the guest and get
// something OOM-killed. Past the cap the head of the file is released and only
// the tail is kept.
const maxLogFileBytes = 32 * 1024 * 1024

// maxLogFile is the largest a single log file may be, before the shared budget
// is taken into account. Override with SANDBOX_MAX_LOG_FILE_BYTES; 0 disables
// capping entirely.
func maxLogFile() int64 {
	if raw := os.Getenv("SANDBOX_MAX_LOG_FILE_BYTES"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return maxLogFileBytes
}

// logBudgetPercent is the share of the guest's memory all process log files
// together may hold. They live on the tmpfs root, so this is memory the workload
// cannot have back: a per-file cap alone is not a bound, since it is paid three
// times (stdout, stderr, combined) per process and a sandbox can run hundreds.
const logBudgetPercent = 10

// logBudgetFallbackBytes is used when the guest's memory size cannot be read.
const logBudgetFallbackBytes = 96 * 1024 * 1024

// minLogFileBytes is the floor the shared budget never divides below: enough
// output left per stream to be worth reading.
const minLogFileBytes = 256 * 1024

// logBudget is how many bytes every process log file may hold together:
// SANDBOX_MAX_LOG_BYTES_TOTAL if set, else SANDBOX_MAX_LOG_PERCENT (default
// logBudgetPercent) of the guest's total memory.
func logBudget() int64 {
	if raw := os.Getenv("SANDBOX_MAX_LOG_BYTES_TOTAL"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			return n
		}
	}

	percent := int64(logBudgetPercent)
	if raw := os.Getenv("SANDBOX_MAX_LOG_PERCENT"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 && n <= 100 {
			percent = n
		}
	}

	total := memTotalBytes()
	if total <= 0 {
		return logBudgetFallbackBytes
	}
	return total * percent / 100
}

// logFilesPerProcess is how many log files each process keeps: stdout, stderr
// and the combined one.
const logFilesPerProcess = 3

// perFileBudget is what one log file may keep when that many processes are
// sharing the budget. 0 means capping is disabled.
func perFileBudget(processes int) int64 {
	max := maxLogFile()
	if max == 0 {
		return 0
	}
	if processes < 1 {
		processes = 1
	}

	budget := logBudget()
	if budget == 0 {
		return max
	}

	share := budget / int64(processes*logFilesPerProcess)
	if share < minLogFileBytes {
		share = minLogFileBytes
	}
	if share < max {
		return share
	}
	return max
}

// memTotalBytes is the guest's total memory, or 0 when it cannot be read. Read
// once: it does not change while the sandbox is alive.
var memTotalBytes = sync.OnceValue(func() int64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
})

// capLogFile releases the head of an oversized log file, keeping the tail its
// share of the budget allows readable. processes is how many processes are
// sharing that budget.
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
func capLogFile(path string, processes int) {
	capLogFileUpTo(path, processes, -1)
}

// capLogFileUpTo is capLogFile with a ceiling on what it may release: a tailer
// reading the file has a cursor of its own, and output past it has not been
// broadcast yet. Punching that range would hand the tailer zero bytes instead
// of the workload's output. A negative limit means there is no reader to wait
// for. It is rounded down to a page boundary like the head itself.
func capLogFileUpTo(path string, processes int, limit int64) {
	max := perFileBudget(processes)
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
	if limit >= 0 {
		if unread := limit & ^int64(pageSize-1); head > unread {
			head = unread
		}
	}
	if head <= 0 {
		return
	}

	if err := punchHole(file, head); err != nil {
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

// maxLogsResponseBytes is how much of a stream GET /process/{id}/logs answers
// with. A response holds stdout, stderr and their concatenation, and the JSON
// encoder copies all of it again, so answering with a whole capped log file
// would cost several times maxLogFileBytes of heap per call - on a guest with no
// swap, enough to be OOM-killed for serving output that is already on disk.
// /process/{id}/logs/stream replays the whole file a line at a time instead.
const maxLogsResponseBytes = 4 * 1024 * 1024

func maxLogsResponse() int64 {
	if raw := os.Getenv("SANDBOX_MAX_LOGS_RESPONSE_BYTES"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n >= 0 {
			return n
		}
	}
	return maxLogsResponseBytes
}

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

	if max > 0 && stat.Size() > max {
		if _, err := file.Seek(stat.Size()-max, io.SeekStart); err != nil {
			file.Close()
			return nil, false, err
		}
		truncated = true
	}

	// capLogFile may have released more than max - what one file may keep
	// shrinks as more processes write - so the position above can sit inside the
	// hole. It reads back as zero bytes, which a caller would hand to a client or
	// scan as one enormous line, so skip to where the kept output starts.
	skipped, err := skipReleasedHead(file)
	if err != nil {
		file.Close()
		return nil, false, err
	}
	return file, truncated || skipped, nil
}

// skipReleasedHead advances file past the zero bytes a released head reads back
// as, and says whether it skipped any.
func skipReleasedHead(file *os.File) (bool, error) {
	start, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return false, err
	}

	buf := make([]byte, 32*1024)
	offset := start
	for {
		n, readErr := file.Read(buf)
		for i := 0; i < n; i++ {
			if buf[i] == 0 {
				continue
			}
			if _, err := file.Seek(offset+int64(i), io.SeekStart); err != nil {
				return false, err
			}
			return offset+int64(i) > start, nil
		}
		offset += int64(n)
		if readErr != nil {
			// Including EOF: the whole tail was released.
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				return false, err
			}
			return offset > start, nil
		}
	}
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

	// Read into one buffer of the size that is left to read: io.ReadAll grows by
	// reallocating, so it can hold twice the tail at once, and this path is what
	// a poller calls on every process.
	content, err := readAtMost(file, max)
	if err != nil {
		return "", false
	}

	// A file whose head was released, or one we seeked past, is missing its
	// head: say so rather than handing back a tail that looks like the whole
	// output.
	if truncated {
		return truncationMarker + string(content), true
	}
	return string(content), true
}

// backlogReader reads file up to the absolute offset end, so a replay stops
// where the live stream takes over.
func backlogReader(file *os.File, end int64) io.Reader {
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return file
	}
	if end <= offset {
		return io.LimitReader(file, 0)
	}
	return io.LimitReader(file, end-offset)
}

// readAtMost reads the rest of file, up to max bytes, into a single buffer.
func readAtMost(file *os.File, max int64) ([]byte, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}

	left := stat.Size() - offset
	if left < 0 {
		left = 0
	}
	if max > 0 && left > max {
		left = max
	}

	buf := make([]byte, left)
	n, err := io.ReadFull(file, buf)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	return buf[:n], nil
}

