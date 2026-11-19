package process

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/network"
)

// exitedBeforePortsTailLines bounds how much of the process's output is quoted
// when it ends before opening its ports: enough to show why it failed.
const exitedBeforePortsTailLines = 20

// The caller's wait ran out before the process opened its ports or exited.
// Returned wrapped with the wait's length; the messages read as they always
// have ("process timed out waiting for ports after N seconds").
var (
	ErrPortsWaitExpired      = errors.New("process timed out waiting for ports")
	ErrCompletionWaitExpired = errors.New("process timed out")
)

// StartError is an exec that failed before its command was started, so the
// command did not run. The message reads as it always has ("failed to start
// process: ...").
type StartError struct{ Err error }

func (e *StartError) Error() string { return "failed to start process: " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// ExitedBeforePortsError is a process that failed for good before it opened
// the ports it was awaited on: the command ran.
type ExitedBeforePortsError struct{ msg string }

func (e *ExitedBeforePortsError) Error() string { return e.msg }

// processByPID returns the process record for a PID without copying its logs.
func (pm *ProcessManager) processByPID(pid string) *ProcessInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.processes[pid]
}

// FinishedChannel resolves a PID or name to its PID and the channel closed once
// that process is over for good. The PID pins the caller to this process even
// if the name is reused later. Read under the lock because a restart of a
// finished process swaps in a fresh channel.
func (pm *ProcessManager) FinishedChannel(identifier string) (string, <-chan struct{}, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	proc := pm.lookupProcessLocked(identifier)
	if proc == nil {
		return "", nil, false
	}
	return proc.PID, proc.Finished, true
}

// endedInFailure reports whether a finished process failed or was killed or
// stopped, as opposed to exiting cleanly with code 0.
func (pm *ProcessManager) endedInFailure(pid string) bool {
	proc := pm.processByPID(pid)
	if proc == nil {
		return false
	}
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return proc.Status != StatusCompleted || proc.ExitCode != 0
}

// exitedBeforePortsError explains that the process failed before the ports it
// was expected to open came up, with its exit code and the end of its output.
func (pm *ProcessManager) exitedBeforePortsError(pid string, ports []int) error {
	proc := pm.processByPID(pid)
	if proc == nil {
		return &ExitedBeforePortsError{msg: fmt.Sprintf("process failed before ports %v opened", ports)}
	}
	pm.mu.RLock()
	status, exitCode := proc.Status, proc.ExitCode
	pm.mu.RUnlock()

	msg := fmt.Sprintf("process exited with code %d before ports %v opened", exitCode, ports)
	if status == StatusKilled || status == StatusStopped {
		msg = fmt.Sprintf("process was %s before ports %v opened", status, ports)
	}
	if output, err := pm.GetProcessOutputTail(pid, 4096); err == nil {
		if tail := lastLines(output.Logs, exitedBeforePortsTailLines); tail != "" {
			msg += "; last output:\n" + tail
		}
	}
	return &ExitedBeforePortsError{msg: msg}
}

// lastLines returns the last n lines of s, without surrounding whitespace.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ExecuteProcess starts a process with its own execution timeout. An optional
// waitTimeout limits only how long this call waits; it never stops the process.
func (pm *ProcessManager) ExecuteProcess(command string, workingDir string, name string, env map[string]string, waitForCompletion bool, timeout int, waitForPorts []int, restartOnFailure bool, maxRestarts int, keepAlive bool, stdin bool, waitTimeout ...int) (*ProcessInfo, error) {
	return pm.ExecuteProcessWithCallback(ExitCallback{}, command, workingDir, name, env, waitForCompletion, timeout, waitForPorts, restartOnFailure, maxRestarts, keepAlive, stdin, waitTimeout...)
}

// ExecuteProcessWithCallback is ExecuteProcess with a per-process exit
// webhook; the zero ExitCallback sends none.
func (pm *ProcessManager) ExecuteProcessWithCallback(
	cb ExitCallback,
	command string,
	workingDir string,
	name string,
	env map[string]string,
	waitForCompletion bool,
	timeout int,
	waitForPorts []int,
	restartOnFailure bool,
	maxRestarts int,
	keepAlive bool,
	stdin bool,
	waitTimeout ...int,
) (*ProcessInfo, error) {
	portCh := make(chan int)
	completionCh := make(chan string, 1)

	// Track port readiness independently of process completion.
	portChClosed := false

	// Protect the port channel from concurrent readiness notifications.
	var mu sync.Mutex

	// Stop port polling when this call returns.
	defer func() {
		mu.Lock()
		defer mu.Unlock()

		if !portChClosed {
			close(portCh)
		}
	}()

	// Bound waiting independently of the process execution deadline.
	waitSeconds := timeout
	if len(waitTimeout) > 0 {
		waitSeconds = waitTimeout[0]
	}

	// This context never controls the child process.
	var ctx context.Context
	var cancel context.CancelFunc
	if waitSeconds > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(waitSeconds)*time.Second)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	// Completion can arrive after the caller's wait expired. A buffered channel
	// lets the single final callback finish without racing a channel close.
	callback := func(p *ProcessInfo) {
		if waitForCompletion {
			completionCh <- p.PID
		}
	}

	// Start the process
	var pid string
	var err error
	if name == "" {
		name = GenerateRandomName(8)
	}
	pid, err = pm.StartProcessWithCallback(command, workingDir, name, env, restartOnFailure, maxRestarts, keepAlive, timeout, stdin, cb, callback)
	if err != nil {
		return nil, &StartError{Err: err}
	}

	// Set up port monitoring if requested
	if len(waitForPorts) > 0 {
		n := network.GetNetwork()
		ports := make([]int, 0, len(waitForPorts))
		pidInt, _ := strconv.Atoi(pid)
		n.RegisterPortOpenCallback(pidInt, func(pid int, port *network.PortInfo) {
			// Only count a port as ready once it is a listening socket bound to a
			// routable interface. A loopback-only bind is reachable via a local
			// connect but not through the edge gateway, so it must not satisfy
			// waitForPorts.
			if slices.Contains(waitForPorts, port.LocalPort) &&
				network.IsRoutableListener(port) &&
				!slices.Contains(ports, port.LocalPort) {
				ports = append(ports, port.LocalPort)
			}
			if len(ports) == len(waitForPorts) {
				// Safely close the channel with defer-recover to prevent panics
				func() {
					defer func() {
						_ = recover()
					}()

					mu.Lock()
					if !portChClosed {
						close(portCh)
						portChClosed = true
					}
					mu.Unlock()

					// Unregister callbacks for this PID to stop monitoring
					n.UnregisterPortOpenCallback(pidInt)
				}()
			}
		})

		// Also start a direct port polling goroutine as a fallback (especially for macOS)
		go func() {
			ticker := time.NewTicker(200 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-ticker.C:
					allPortsOpen := true
					for _, port := range waitForPorts {
						if !network.IsPortReady(pidInt, port) {
							allPortsOpen = false
							break
						}
					}
					if allPortsOpen {
						func() {
							defer func() {
								_ = recover()
							}()

							mu.Lock()
							if !portChClosed {
								close(portCh)
								portChClosed = true
							}
							mu.Unlock()

							n.UnregisterPortOpenCallback(pidInt)
						}()
						return
					}
				case <-ctx.Done():
					return
				case <-portCh:
					// Already closed by PID-based monitoring
					return
				}
			}
		}()
	}

	// Wait for ports if requested
	if len(waitForPorts) > 0 {
		// A process that fails for good can never open its ports, so stop
		// waiting as soon as it does instead of running out the timeout (or
		// waiting forever when there is none). Finished stays open across
		// restarts, so restartOnFailure still gets its retries.
		//
		// A clean exit is not treated as a failure: a command can exit 0 after
		// starting a server in the background (`npm run dev &`), and where the
		// port owner can't be enumerated the connect-probe fallback still sees
		// that server open the port. So on exit code 0 we keep waiting as before.
		var finished <-chan struct{}
		if proc := pm.processByPID(pid); proc != nil {
			finished = proc.Finished
		}
	waitPorts:
		for {
			select {
			case <-portCh:
				break waitPorts // Ports are ready
			case <-finished:
				finished = nil // fires once; a nil channel never selects again
				select {
				case <-portCh:
					// The ports opened just before the process ended; that
					// still counts as ready, as it did before.
					break waitPorts
				default:
				}
				if pm.endedInFailure(pid) {
					return nil, pm.exitedBeforePortsError(pid, waitForPorts)
				}
			case <-ctx.Done():
				err := fmt.Errorf("%w after %d seconds", ErrPortsWaitExpired, waitSeconds)
				if len(waitTimeout) > 0 && waitSeconds > 0 && (timeout <= 0 || waitSeconds < timeout) {
					// A capped caller wait leaves execution running. Return its
					// identity so the caller can continue monitoring the process.
					return pm.processByPID(pid), err
				}
				return nil, err
			}
		}
	}

	// Wait for completion if requested
	if waitForCompletion {
		select {
		case receivedPID := <-completionCh:
			_, exists := pm.GetProcessByIdentifier(receivedPID)
			if !exists {
				return nil, fmt.Errorf("process creation failed because process does not exist")
			}
			pid = receivedPID // Update pid to the received PID
			break
		case <-ctx.Done():
			// The wait deadline may precede the execution deadline (e.g. MCP).
			// Return process info so the caller can inspect the eventual outcome.
			err := fmt.Errorf("%w after %d seconds", ErrCompletionWaitExpired, waitSeconds)
			processInfo, exists := pm.GetProcessByIdentifier(pid)
			if exists {
				return processInfo, err
			}
			return nil, err
		}
	}

	// Get the process info
	processInfo, exists := pm.GetProcessByIdentifier(pid)
	if !exists {
		return nil, fmt.Errorf("process creation failed because process does not exist")
	}
	if waitForCompletion {
		// Read logs from file if available (more reliable than in-memory)
		output, err := pm.GetProcessOutput(pid)
		processInfo.logLock.Lock()
		if err == nil {
			processInfo.Logs = &output.Logs
			processInfo.Stdout = &output.Stdout
			processInfo.Stderr = &output.Stderr
			processInfo.outputCutBytes = output.OutputLimitBytes
		} else {
			// Fall back to in-memory
			logs := processInfo.logs.String()
			processInfo.Logs = &logs
			stdout := processInfo.stdout.String()
			processInfo.Stdout = &stdout
			stderr := processInfo.stderr.String()
			processInfo.Stderr = &stderr
			_, processInfo.outputCutBytes = outputCut(processInfo.stdout.cut(), processInfo.stderr.cut())
		}
		processInfo.logLock.Unlock()
	}
	return processInfo, nil
}
