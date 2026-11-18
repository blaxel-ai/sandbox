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
	ExitReason       string
	OOMKills         int
	WorkingDir       string
	Logs             *string
	Stdout           *string
	Stderr           *string
	RestartOnFailure bool
	MaxRestarts      int
	RestartCount     int
	KeepAlive        bool
	Stdin            bool
	// OutputTruncated and OutputLimitBytes describe Logs, Stdout and Stderr:
	// whether their start is missing and the limit that cut it.
	OutputTruncated  bool
	OutputLimitBytes int64
	stdoutFile       string
	stderrFile       string
	logRetention     int64
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
		ExitCode: p.ExitCode, Status: p.Status, WorkingDir: p.EffectiveWorkingDir,
		ExitReason: p.ExitReason, OOMKills: p.OOMKills,
		RestartOnFailure: p.RestartOnFailure, MaxRestarts: p.MaxRestarts,
		RestartCount: p.RestartCount, KeepAlive: p.KeepAlive, Stdin: p.Stdin,
		stdoutFile: p.StdoutFile, stderrFile: p.StderrFile,
	}
	if snapshot.WorkingDir == "" {
		// Older state files may only contain an explicit requested directory.
		snapshot.WorkingDir = p.WorkingDir
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
	var stdoutCut, stderrCut int64
	if snapshot.Stdout != nil {
		// An exec read Logs, Stdout and Stderr together; keep its cut.
		stdoutCut = p.outputCutBytes
	}
	if snapshot.Logs == nil && p.logs != nil && p.logs.Len() > 0 {
		logs := p.logs.String()
		snapshot.Logs = &logs
	}
	if snapshot.Stdout == nil && p.stdout != nil {
		stdout := p.stdout.String()
		snapshot.Stdout = &stdout
		stdoutCut = p.stdout.cut()
	}
	if snapshot.Stderr == nil && p.stderr != nil {
		stderr := p.stderr.String()
		snapshot.Stderr = &stderr
		stderrCut = p.stderr.cut()
	}
	snapshot.OutputTruncated, snapshot.OutputLimitBytes = outputCut(stdoutCut, stderrCut)
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
	snapshot := snapshotLocked(p)
	snapshot.logRetention = pm.logRetention()
	return snapshot, true
}

// ListProcessSnapshots copies all response data while it is stable.
func (pm *ProcessManager) ListProcessSnapshots() []ProcessSnapshot {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	result := make([]ProcessSnapshot, 0, len(pm.processes))
	retention := pm.logRetention()
	for _, p := range pm.processes {
		snapshot := snapshotLocked(p)
		snapshot.logRetention = retention
		result = append(result, snapshot)
	}
	return result
}

// OutputTail reads files belonging to this snapshot, without another process
// lookup. In particular, reusing a name cannot mix one process's state with
// another process's output. The captured buffers are a fallback if files vanish.
func (p ProcessSnapshot) OutputTail(max int64) ProcessLogs {
	stdout, stdoutCut, ok := readLogTail(p.stdoutFile, max, p.logRetention)
	if !ok && p.Stdout != nil {
		stdout, stdoutCut = *p.Stdout, p.OutputLimitBytes
	}
	stderr, stderrCut, ok := readLogTail(p.stderrFile, max, p.logRetention)
	if !ok && p.Stderr != nil {
		stderr, stderrCut = *p.Stderr, p.OutputLimitBytes
	}
	truncated, limit := outputCut(stdoutCut, stderrCut)
	return ProcessLogs{Stdout: stdout, Stderr: stderr, Logs: stdout + stderr, OutputTruncated: truncated, OutputLimitBytes: limit}
}
