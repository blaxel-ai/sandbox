//go:build linux

package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// retentionSyscalls lets tests simulate unsupported attributes and failed renames.
type retentionSyscalls struct {
	getXattr func(int, string, []byte) (int, error)
	setXattr func(int, string, []byte, int) error
	rename   func(string, string) error
}

var retentionIO = func() *atomic.Pointer[retentionSyscalls] {
	calls := new(atomic.Pointer[retentionSyscalls])
	calls.Store(&retentionSyscalls{unix.Fgetxattr, unix.Fsetxattr, os.Rename})
	return calls
}()

const retainedHeadAttribute = "user.blaxel.log_head"

// retainedHead reads the boundary from an inode attribute or a companion file
// with a matching device and inode. Callers hold a file lock.
func retainedHead(file *os.File) (int64, error) {
	var value [32]byte
	n, err := retentionIO.Load().getXattr(int(file.Fd()), retainedHeadAttribute, value[:])
	head := int64(-1)
	if err == nil {
		head, err = strconv.ParseInt(string(value[:n]), 10, 64)
		if err != nil || head < 0 {
			return 0, fmt.Errorf("invalid log retention watermark")
		}
	} else if !errors.Is(err, unix.ENODATA) && !errors.Is(err, unix.ENOTSUP) {
		return 0, err
	}
	sidecar, found, err := readRetentionSidecar(file)
	if err != nil {
		return 0, err
	}
	if found && sidecar > head {
		head = sidecar
	}
	if head >= 0 {
		return head, nil
	}
	// Older APIs punched files without recording a boundary. Use allocated
	// extents to find it because written NUL bytes are valid output. SEEK_DATA
	// moves the descriptor's cursor, so restore it before returning.
	offset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return 0, err
	}
	head, seekErr := unix.Seek(int(file.Fd()), 0, unix.SEEK_DATA)
	_, restoreErr := file.Seek(offset, io.SeekStart)
	if restoreErr != nil {
		return 0, restoreErr
	}
	if errors.Is(seekErr, unix.ENXIO) {
		st, err := file.Stat()
		if err != nil {
			return 0, err
		}
		return st.Size(), nil
	}
	if errors.Is(seekErr, unix.EINVAL) || errors.Is(seekErr, unix.ENOTSUP) {
		return 0, fmt.Errorf("cannot determine legacy log retention boundary: %w", seekErr)
	}
	return head, seekErr
}

func recordRetainedHead(file *os.File, head int64) error {
	err := retentionIO.Load().setXattr(int(file.Fd()), retainedHeadAttribute, []byte(strconv.FormatInt(head, 10)), 0)
	if errors.Is(err, unix.ENOTSUP) {
		return writeRetentionSidecar(file, head)
	}
	return err
}

// lockLogFile uses open-file-description (OFD) locks to coordinate readers and
// retention across API processes. These locks do not conflict with the flock
// held for journal ownership.
func lockLogFile(file *os.File, exclusive bool) error {
	kind := int16(unix.F_RDLCK)
	if exclusive {
		kind = unix.F_WRLCK
	}
	lock := unix.Flock_t{Type: kind, Whence: int16(io.SeekStart)}
	for {
		err := unix.FcntlFlock(file.Fd(), unix.F_OFD_SETLKW, &lock)
		if err != unix.EINTR {
			return err
		}
	}
}
func unlockLogFile(file *os.File) {
	lock := unix.Flock_t{Type: unix.F_UNLCK, Whence: int16(io.SeekStart)}
	_ = unix.FcntlFlock(file.Fd(), unix.F_OFD_SETLK, &lock)
}

// initializeLogRetention records a zero boundary for new logs. Sparse NUL
// ranges remain ordinary data if a workload later seeks its output descriptor.
func initializeLogRetention(file *os.File) error {
	// Callers create the file exclusively before the workload can write. Clear
	// stale metadata even if the filesystem recycled a prior inode number.
	if err := os.Remove(retentionSidecarPath(file)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return recordRetainedHead(file, 0)
}
