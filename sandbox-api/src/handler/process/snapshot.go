package process

import (
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
)

// ProcessSnapshot is detached response data. Lifecycle fields are copied under
// the manager lock so a terminal status always carries the matching exit code
// and completion time. It contains no live process handles or synchronization.
type ProcessSnapshot struct {
	PID              string
	Name             string
	Command          string
	StartedAt        time.Time
	CompletedAt      *time.Time
	ExitCode         int
	Status           constants.ProcessStatus
	WorkingDir       string
	Logs             *string
	Stdout           *string
	Stderr           *string
	RestartOnFailure bool
	MaxRestarts      int
	RestartCount     int
	KeepAlive        bool
	Stdin            bool
	stdoutFile       string
	stderrFile       string
}

func copyString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// snapshotLocked requires pm.mu to be held. File I/O is deliberately kept out
// of this critical section; only the bounded in-memory fallback is copied here.
func snapshotLocked(p *ProcessInfo) ProcessSnapshot {
	snapshot := ProcessSnapshot{
		PID: p.PID, Name: p.Name, Command: p.Command, StartedAt: p.StartedAt,
		ExitCode: p.ExitCode, Status: p.Status, WorkingDir: p.WorkingDir,
		RestartOnFailure: p.RestartOnFailure, MaxRestarts: p.MaxRestarts,
		RestartCount: p.RestartCount, KeepAlive: p.KeepAlive, Stdin: p.Stdin,
		stdoutFile: p.StdoutFile, stderrFile: p.StderrFile,
	}
	if p.CompletedAt != nil {
		completedAt := *p.CompletedAt
		snapshot.CompletedAt = &completedAt
	}
	p.logLock.RLock()
	defer p.logLock.RUnlock()
	snapshot.Logs = copyString(p.Logs)
	snapshot.Stdout = copyString(p.Stdout)
	snapshot.Stderr = copyString(p.Stderr)
	if snapshot.Logs == nil && p.logs != nil && p.logs.Len() > 0 {
		logs := p.logs.String()
		snapshot.Logs = &logs
	}
	if snapshot.Stdout == nil && p.stdout != nil {
		stdout := p.stdout.String()
		snapshot.Stdout = &stdout
	}
	if snapshot.Stderr == nil && p.stderr != nil {
		stderr := p.stderr.String()
		snapshot.Stderr = &stderr
	}
	return snapshot
}

// GetProcessSnapshot returns detached data, unlike GetProcessByIdentifier which
// is a mutable handle used internally for signalling and stream lifecycles.
func (pm *ProcessManager) GetProcessSnapshot(identifier string) (ProcessSnapshot, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	p := pm.lookupProcessLocked(identifier)
	if p == nil {
		return ProcessSnapshot{}, false
	}
	return snapshotLocked(p), true
}

// ListProcessSnapshots copies all response data while it is stable.
func (pm *ProcessManager) ListProcessSnapshots() []ProcessSnapshot {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]ProcessSnapshot, 0, len(pm.processes))
	for _, p := range pm.processes {
		result = append(result, snapshotLocked(p))
	}
	return result
}

// OutputTail reads files belonging to this snapshot, without another process
// lookup. In particular, reusing a name cannot mix one process's state with
// another process's output. The captured buffers are a fallback if files vanish.
func (p ProcessSnapshot) OutputTail(max int64) ProcessLogs {
	stdout, ok := readLogTail(p.stdoutFile, max)
	if !ok && p.Stdout != nil {
		stdout = *p.Stdout
	}
	stderr, ok := readLogTail(p.stderrFile, max)
	if !ok && p.Stderr != nil {
		stderr = *p.Stderr
	}
	return ProcessLogs{Stdout: stdout, Stderr: stderr, Logs: stdout + stderr}
}
