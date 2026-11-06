//go:build linux

package lifecycle

import "golang.org/x/sys/unix"

// becomeSubreaper makes this process the parent of the orphans of its
// descendants, as PID 1 is of everyone's.
func becomeSubreaper() {
	_ = unix.Prctl(unix.PR_SET_CHILD_SUBREAPER, 1, 0, 0, 0)
}
