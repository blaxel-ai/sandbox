package archive

import (
	"sync/atomic"
	"syscall"

	"github.com/sirupsen/logrus"
)

// startupWorkloadPID is the process sandbox-api started from its own command
// line, if any.
//
// It is not one of the process manager's processes: it is started directly at
// boot, before the API serves anything, and the manager never hears about it. An
// export has to stop it all the same - it is usually *the* workload, so leaving
// it running would let the one process most likely to be writing keep writing
// into the archive while the filesystem is read.
var startupWorkloadPID atomic.Int64

// RegisterStartupWorkload records the process started from the -command flag so
// an export can stop it.
func RegisterStartupWorkload(pid int) {
	startupWorkloadPID.Store(int64(pid))
}

// UnregisterStartupWorkload forgets the process, which has exited. It only
// forgets the PID it is given: a command that exited after being replaced must
// not clear its successor.
func UnregisterStartupWorkload(pid int) {
	startupWorkloadPID.CompareAndSwap(int64(pid), 0)
}

// stopStartupWorkload asks the startup command to exit and returns it so the
// export waits for it like any other stopped process. The second return value is
// false when there is nothing to stop.
//
// Only the command's own process is signalled, not a process group: it shares
// sandbox-api's group, so signalling the group would kill this API. A shell
// running the command usually execs it, in which case this is the workload
// itself; a shell that instead forks leaves its children running, and the
// read-only remount is what stops them from writing.
func stopStartupWorkload() (stoppedProcess, bool) {
	pid := int(startupWorkloadPID.Load())
	if pid <= 0 || !processAlive(pid) {
		return stoppedProcess{}, false
	}

	candidate := stoppedProcess{
		identifier: "startup-command",
		pid:        pid,
		kill:       func() error { return syscall.Kill(pid, syscall.SIGKILL) },
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		logrus.WithError(err).WithField("pid", pid).Warn("[Archive] Failed to stop the startup command gracefully, it will be killed")
	}
	return candidate, true
}
