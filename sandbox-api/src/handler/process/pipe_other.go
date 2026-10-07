//go:build !linux

package process

import "os"

// growPipe is a no-op off Linux, which has no F_SETPIPE_SZ.
func growPipe(w *os.File, size int) {}
