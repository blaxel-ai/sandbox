package process

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/network"
)

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
		pid, err = pm.StartProcessWithName(command, workingDir, name, env, restartOnFailure, maxRestarts, callback)
	} else {
		pid, err = pm.StartProcess(command, workingDir, env, restartOnFailure, maxRestarts, callback)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	// Set up port monitoring if requested
	if len(waitForPorts) > 0 {
		// Check for Mac OS and skip port monitoring if needed
		if runtime.GOOS == "darwin" {
			// Just close the channel without trying to monitor ports
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
			}()
		} else {
			n := network.GetNetwork()
			ports := make([]int, 0, len(waitForPorts))
			pidInt, _ := strconv.Atoi(pid)
			n.RegisterPortOpenCallback(pidInt, func(pid int, port *network.PortInfo) {
				if slices.Contains(waitForPorts, port.LocalPort) {
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
			return nil, fmt.Errorf("process timed out after %d seconds", timeout)
		}
	}

	// Get the process info
	processInfo, exists := pm.GetProcessByIdentifier(pid)
	if !exists {
		return nil, fmt.Errorf("process creation failed because process does not exist")
	}
	if waitForCompletion {
		logs := processInfo.logs.String()
		processInfo.Logs = &logs
	}
	return processInfo, nil
}
