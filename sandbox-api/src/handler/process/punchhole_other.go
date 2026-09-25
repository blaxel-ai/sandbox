//go:build !linux

package process

import (
	"errors"
	"os"
)

// punchHole is a no-op off Linux: releasing a range without moving the offsets
// after it needs FALLOC_FL_PUNCH_HOLE. The sandbox itself only ever runs on
// Linux; these builds exist for the CLI.
func punchHole(file *os.File, length int64) error {
	return errors.ErrUnsupported
}
