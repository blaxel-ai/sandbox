//go:build linux

package identity

import (
	"fmt"
	"runtime"
	"syscall"

	"github.com/sirupsen/logrus"
)

// Do runs fn with the calling thread's filesystem identity set to the workload
// user, so every path the kernel resolves inside fn is checked against that
// user's permissions instead of root's.
//
// setfsuid(2)/setfsgid(2) are per-thread, hence the locked OS thread; they also
// drop the thread's CAP_DAC_OVERRIDE / CAP_DAC_READ_SEARCH for the duration,
// which is precisely the point: it stops the filesystem API from being used to
// overwrite root-owned files (for example the blfs binary the drive mount runs
// as root) and thereby escalate out of the unprivileged identity.
//
// Limitation: the fsgid change covers the primary group only. Access granted
// exclusively through a supplementary group of the workload user is not
// honoured inside Do.
func (id *Identity) Do(fn func() error) error {
	if id == nil {
		return fn()
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	previousGid := setfsgid(id.Gid)
	previousUid := setfsuid(id.Uid)

	// setfsuid(2)/setfsgid(2) have no error return: they report the previous
	// value whether or not they changed anything. Reading the value back is the
	// only way to know the thread really lost its root filesystem privileges.
	if current := setfsuid(id.Uid); current != id.Uid {
		setfsuid(previousUid)
		setfsgid(previousGid)
		return fmt.Errorf("failed to drop filesystem uid to %d", id.Uid)
	}
	if current := setfsgid(id.Gid); current != id.Gid {
		setfsuid(previousUid)
		setfsgid(previousGid)
		return fmt.Errorf("failed to drop filesystem gid to %d", id.Gid)
	}

	defer func() {
		// Restoring must succeed: a thread left with a non-root fsuid would
		// silently break the privileged work (mounts, tunnel) that runs on it
		// later. Restoring the previous value rather than 0 keeps nested calls
		// correct.
		setfsuid(previousUid)
		setfsgid(previousGid)
		if current := setfsuid(previousUid); current != previousUid {
			logrus.Fatalf("Failed to restore filesystem uid to %d", previousUid)
		}
	}()

	return fn()
}

// setfsuid and setfsgid wrap the raw syscalls because the syscall package
// discards their return value, which is the previous uid/gid.
func setfsuid(uid int) int {
	previous, _, _ := syscall.Syscall(syscall.SYS_SETFSUID, uintptr(uid), 0, 0)
	return int(previous)
}

func setfsgid(gid int) int {
	previous, _, _ := syscall.Syscall(syscall.SYS_SETFSGID, uintptr(gid), 0, 0)
	return int(previous)
}
