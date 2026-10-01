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

// processByPID returns the process record for a PID without copying its logs.
func (pm *ProcessManager) processByPID(pid string) *ProcessInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return pm.processes[pid]
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
		return fmt.Errorf("process failed before ports %v opened", ports)
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
	return errors.New(msg)
}

// lastLines returns the last n lines of s, without surrounding whitespace.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// ExecuteProcess executes a process with the given parameters
func (pm *ProcessManager) ExecuteProcess(
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
) (*ProcessInfo, error) {
	portCh := make(chan int)
	completionCh := make(chan string)

	// Add flags to track if channels have been closed
	portChClosed := false
	completionChClosed := false

	// Use a mutex to protect the flags
	var mu sync.Mutex

	// Defer closing the channels if they're not already closed
	defer func() {
		mu.Lock()
		defer mu.Unlock()

		if !portChClosed {
			close(portCh)
		}

		if !completionChClosed {
			close(completionCh)
		}
	}()

	// Create a context with the specified timeout
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
		defer cancel()
	} else {
		ctx = context.Background()
	}

	// Create a callback function
	callback := func(p *ProcessInfo) {
		if waitForCompletion {
			mu.Lock()
			closed := completionChClosed
			mu.Unlock()
			if !closed {
				// Use a recover block in case of a race condition
				defer func() {
					_ = recover()
				}()
				completionCh <- p.PID
			}
		}
	}

	// Start the process
	var pid string
	var err error
	if name != "" {
		pid, err = pm.StartProcessWithName(command, workingDir, name, env, restartOnFailure, maxRestarts, keepAlive, timeout, stdin, callback)
	} else {
		pid, err = pm.StartProcess(command, workingDir, env, restartOnFailure, maxRestarts, keepAlive, timeout, stdin, callback)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
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
				return nil, fmt.Errorf("process timed out waiting for ports after %d seconds", timeout)
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
			// Process timed out but is still running - return process info along with error
			// so the caller can still access the running process
			processInfo, exists := pm.GetProcessByIdentifier(pid)
			if exists {
				return processInfo, fmt.Errorf("process timed out after %d seconds", timeout)
			}
			return nil, fmt.Errorf("process timed out after %d seconds", timeout)
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
		} else {
			// Fall back to in-memory
			logs := processInfo.logs.String()
			processInfo.Logs = &logs
			stdout := processInfo.stdout.String()
			processInfo.Stdout = &stdout
			stderr := processInfo.stderr.String()
			processInfo.Stderr = &stderr
		}
		processInfo.logLock.Unlock()
	}
	return processInfo, nil
}
