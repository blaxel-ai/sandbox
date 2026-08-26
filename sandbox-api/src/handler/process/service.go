package process

import (
	"context"
	"fmt"
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
	keepAlive bool,
) (*ProcessInfo, error) {
	portCh := make(chan int)

	portChClosed := false

	var mu sync.Mutex

	defer func() {
		mu.Lock()
		defer mu.Unlock()

		if !portChClosed {
			close(portCh)
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

	callback := func(*ProcessInfo) {}

	// Start the process
	var pid string
	var err error
	if name != "" {
		pid, err = pm.StartProcessWithName(command, workingDir, name, env, restartOnFailure, maxRestarts, keepAlive, timeout, callback)
	} else {
		pid, err = pm.StartProcess(command, workingDir, env, restartOnFailure, maxRestarts, keepAlive, timeout, callback)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}
	pm.mu.RLock()
	processInfo, exists := pm.processes[pid]
	pm.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("process creation failed because process does not exist")
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
		select {
		case <-portCh:
			// Ports are ready
		case <-ctx.Done():
			return nil, fmt.Errorf("process timed out waiting for ports after %d seconds", timeout)
		}
	}

	// Wait for completion if requested
	if waitForCompletion {
		select {
		case <-processInfo.Finished:
		case <-ctx.Done():
			return processInfo, fmt.Errorf("process timed out after %d seconds", timeout)
		}
	}
	return processInfo, nil
}
