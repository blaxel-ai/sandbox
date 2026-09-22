package process

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/sirupsen/logrus"
)

// lookupProcessLocked returns a mutable handle without materializing log output.
func (pm *ProcessManager) lookupProcessLocked(identifier string) *ProcessInfo {
	if _, err := strconv.Atoi(identifier); err == nil {
		return pm.processes[identifier]
	}
	var latest *ProcessInfo
	for _, p := range pm.processes {
		if p.Name == identifier && (latest == nil || p.StartedAt.After(latest.StartedAt)) {
			latest = p
		}
	}
	return latest
}

func (pm *ProcessManager) signalProcess(identifier string, signal syscall.Signal, status constants.ProcessStatus) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p := pm.lookupProcessLocked(identifier)
	if p == nil {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}
	if p.Status != StatusRunning {
		// Repeating a completed stop is harmless and must never signal a reused PID.
		if p.terminationRequested != "" {
			return nil
		}
		return fmt.Errorf("process with Identifier %s is not running", identifier)
	}
	if p.terminationRequested == StatusKilled || p.terminationRequested == status {
		return nil
	}
	if !p.runExited {
		if p.ProcessPid == 0 {
			return fmt.Errorf("process with Identifier %s has no OS process", identifier)
		}
		err := syscall.Kill(-p.ProcessPid, signal)
		if err != nil {
			err = syscall.Kill(p.ProcessPid, signal)
		}
		if err != nil && !errors.Is(err, syscall.ESRCH) {
			return fmt.Errorf("failed to send %s to process %s: %w", signal, identifier, err)
		}
	}
	// ESRCH races with Wait: remember intent but let the observer confirm exit.
	p.terminationRequested = status
	// During restart backoff the previous run has already exited and drained.
	// Publish its terminal outcome and prevent the delayed spawn.
	if p.runExited && p.CompletedAt != nil {
		p.Status = status
	}
	return nil
}

// waitForRun is the sole completion path for child processes, including restarts.
func (pm *ProcessManager) waitForRun(p *ProcessInfo, cmd *exec.Cmd, callback func(*ProcessInfo)) {
	err := cmd.Wait()
	if cmd.Process != nil {
		_ = cmd.Process.Release()
	}
	pm.mu.Lock()
	p.runExited = true
	pm.mu.Unlock()
	// The tailer must ingest the child's final writes before any terminal response.
	close(p.Done)
	<-p.TailDone
	p.logLock.Lock()
	p.Logs, p.Stdout, p.Stderr = nil, nil, nil
	p.logLock.Unlock()
	now := time.Now()
	pm.mu.Lock()
	p.CompletedAt = &now
	p.ExitCode = 0
	p.Status = StatusCompleted
	if err != nil {
		p.Status = StatusFailed
		p.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			p.ExitCode = exitErr.ExitCode()
		}
	}
	if p.terminationRequested != "" {
		p.Status = p.terminationRequested
	}
	restart := shouldRestart(p)
	if restart {
		p.Status = StatusRunning
	}
	if p.stopTimeout != nil {
		p.stopTimeoutOnce.Do(func() { close(p.stopTimeout) })
	}
	pm.mu.Unlock()
	if !restart {
		pm.finishProcess(p, callback)
		return
	}

	restartMsg := fmt.Sprintf("\n[Process failed with exit code %d. Attempting restart %d/%s...]\n", p.ExitCode, p.RestartCount+1, restartLimitLabel(p.MaxRestarts))
	p.logLock.Lock()
	p.stdout.WriteString(restartMsg)
	p.logs.WriteString(restartMsg)
	if p.StdoutFile != "" {
		if f, err := os.OpenFile(p.StdoutFile, os.O_APPEND|os.O_WRONLY, 0644); err == nil {
			_, _ = f.WriteString(restartMsg)
			_ = f.Close()
		}
	}
	for _, w := range p.logWriters {
		_, _ = w.Write([]byte(restartMsg))
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
	p.logLock.Unlock()
	time.Sleep(time.Second)
	if !beginRestart() {
		pm.leaveStopped(p, callback)
		return
	}
	_, restartErr := pm.restartProcess(p, callback)
	endRestart()
	if restartErr != nil {
		p.logLock.Lock()
		errorMsg := fmt.Sprintf("\n[Failed to restart process: %v]\n", restartErr)
		p.stdout.WriteString(errorMsg)
		p.logs.WriteString(errorMsg)
		p.logLock.Unlock()
		close(p.Done)
		<-p.TailDone
		pm.leaveStopped(p, callback)
	}
}

// finishProcess releases the scale hold once, after an observed final exit.
func (pm *ProcessManager) finishProcess(p *ProcessInfo, callback func(*ProcessInfo)) {
	pm.releaseKeepAlive(p)
	p.logLock.Lock()
	p.logWriters = nil
	p.logLock.Unlock()
	p.markFinished()
	if callback != nil {
		callback(p)
	}
}

func (pm *ProcessManager) releaseKeepAlive(p *ProcessInfo) {
	pm.mu.Lock()
	held := p.KeepAlive
	p.KeepAlive = false
	if p.stopTimeout != nil {
		p.stopTimeoutOnce.Do(func() { close(p.stopTimeout) })
	}
	pm.mu.Unlock()
	if held {
		if err := blaxel.ScaleEnable(); err != nil {
			logrus.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero after process exit")
		}
	}
}
