//go:build linux

package process

import (
	"os"

	"golang.org/x/sys/unix"
)

// growPipe asks the kernel for a pipe buffer of at least size bytes.
func growPipe(w *os.File, size int) {
	if rc, err := w.SyscallConn(); err == nil {
		_ = rc.Control(func(fd uintptr) {
			_, _ = unix.FcntlInt(fd, unix.F_SETPIPE_SZ, size)
		})
	}
}
