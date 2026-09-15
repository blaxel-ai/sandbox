package archive

import (
	"errors"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
)

// startupWorkload is the process sandbox-api started from its own command
// line, if any.
//
// It is not one of the process manager's processes: it is started directly at
// boot, before the API serves anything, and the manager never hears about it. An
// export has to stop it all the same - it is usually *the* workload, so leaving
// it running would let the one process most likely to be writing keep writing
// into the archive while the filesystem is read.
var startupWorkload struct {
	sync.Mutex
	pid int
	// restart starts the command over. It is what a failed export gives the
	// sandbox back its workload with: the command was stopped for an archive
	// that never landed, and a sandbox whose main process is gone is bricked.
	restart func()
}

// RegisterStartupWorkload records the process started from the -command flag so
// an export can stop it, and how to start the command over should the export
// fail. A nil restart leaves a stopped command stopped.
func RegisterStartupWorkload(pid int, restart func()) {
	startupWorkload.Lock()
	defer startupWorkload.Unlock()
	startupWorkload.pid = pid
	startupWorkload.restart = restart
}

// UnregisterStartupWorkload forgets the process, which has exited. It only
// forgets the PID it is given: a command that exited after being replaced must
// not clear its successor. How to restart the command is kept: it is what a
// failed export relaunches the command it stopped with.
func UnregisterStartupWorkload(pid int) {
	startupWorkload.Lock()
	defer startupWorkload.Unlock()
	if startupWorkload.pid == pid {
		startupWorkload.pid = 0
	}
}

// startupWorkloadPID is the PID of the startup command, or 0 when there is none
// or it has exited.
func startupWorkloadPID() int {
	startupWorkload.Lock()
	defer startupWorkload.Unlock()
	return startupWorkload.pid
}

// restartStartupWorkload starts the startup command over after an export
// stopped it and failed. Nothing is started when the command already runs
// again - it was restarted from outside, or its stop never took - since two
// copies of the workload is worse than none.
func restartStartupWorkload() bool {
	startupWorkload.Lock()
	restart := startupWorkload.restart
	pid := startupWorkload.pid
	startupWorkload.Unlock()

	if restart == nil {
		return false
	}
	if pid > 0 && processAlive(pid) {
		return false
	}
	logrus.Info("[Archive] Restarting the startup command the failed export stopped")
	restart()
	return true
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
	pid := startupWorkloadPID()
	if pid <= 0 || !processAlive(pid) {
		return stoppedProcess{}, false
	}

	candidate := stoppedProcess{
		identifier: "startup-command",
		pid:        pid,
		kill:       func() error { return syscall.Kill(pid, syscall.SIGKILL) },
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			// Exited on its own before the signal: the export did not stop it.
			return stoppedProcess{}, false
		}
		logrus.WithError(err).WithField("pid", pid).Warn("[Archive] Failed to stop the startup command gracefully, it will be killed")
	}
	return candidate, true
}
