package process

import (
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

// startExecutionTimeout arms a deadline for this run, including the remaining
// time of an adopted process. KeepAlive only controls the scale-to-zero hold.
func (pm *ProcessManager) startExecutionTimeout(p *ProcessInfo) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	pm.startExecutionTimeoutLocked(p)
}

// startExecutionTimeoutLocked also serves state adoption, which holds pm.mu.
func (pm *ProcessManager) startExecutionTimeoutLocked(p *ProcessInfo) {
	timeout, startedAt, stopped := p.Timeout, p.StartedAt, p.stopTimeout
	if timeout <= 0 {
		return
	}
	timer := time.NewTimer(time.Until(startedAt.Add(time.Duration(timeout) * time.Second)))
	go func() {
		defer timer.Stop()
		select {
		case <-stopped:
			return
		case <-timer.C:
		}
		pm.mu.Lock()
		// A finished run must not kill a newer run, or a reused OS PID.
		if p.stopTimeout != stopped || p.runExited {
			pm.mu.Unlock()
			return
		}
		select {
		case <-stopped:
			pm.mu.Unlock()
			return
		default:
		}
		err := pm.signalProcessLocked(p.PID, syscall.SIGKILL, StatusKilled)
		pid, name := p.PID, p.Name
		pm.mu.Unlock()
		entry := logrus.WithFields(logrus.Fields{"process_pid": pid, "process_name": name, "timeout": timeout})
		if err != nil {
			entry.WithError(err).Warn("Failed to kill process at execution deadline")
		} else {
			entry.Info("Execution timeout expired, killing process")
		}
	}()
}
