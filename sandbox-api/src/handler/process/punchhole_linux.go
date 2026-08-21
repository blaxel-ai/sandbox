//go:build linux

package process

import (
	"os"

	"golang.org/x/sys/unix"
)

// punchHole releases the first length bytes of file without moving any offset
// after them: the range reads back as zero bytes and the file keeps its size.
func punchHole(file *os.File, length int64) error {
	return unix.Fallocate(int(file.Fd()),
		unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, 0, length)
}
