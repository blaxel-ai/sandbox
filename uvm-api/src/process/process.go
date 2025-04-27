package process

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ProcessManager manages the running processes
type ProcessManager struct {
	processes   map[int]*ProcessInfo
	processLock sync.RWMutex
}

// ProcessInfo stores information about a running process
type ProcessInfo struct {
	PID         int
	Command     string
	Cmd         *exec.Cmd
	StartedAt   time.Time
	CompletedAt *time.Time
	ExitCode    int
	Status      string
	WorkingDir  string
	stdout      *strings.Builder
	stderr      *strings.Builder
	stdoutPipe  io.ReadCloser
	stderrPipe  io.ReadCloser
	logWriters  []io.Writer
	logLock     sync.RWMutex
}

// NewProcessManager creates a new process manager
func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[int]*ProcessInfo),
	}
}

// Global process manager instance
var (
	processManager     *ProcessManager
	processManagerOnce sync.Once
)

// GetProcessManager returns the singleton process manager instance
func GetProcessManager() *ProcessManager {
	processManagerOnce.Do(func() {
		processManager = NewProcessManager()
	})
	return processManager
}

func (pm *ProcessManager) StartProcess(command string, workingDir string, callback func(process *ProcessInfo)) (int, error) {
	pm.processLock.Lock()
	defer pm.processLock.Unlock()

	var cmd *exec.Cmd

	// Check if the command needs a shell by looking for shell special chars
	if strings.Contains(command, "&&") || strings.Contains(command, "|") ||
		strings.Contains(command, ">") || strings.Contains(command, "<") ||
		strings.Contains(command, ";") {
		// Use shell to execute the command
		cmd = exec.Command("sh", "-c", command)
	} else {
		// Parse command string into command and arguments while respecting quotes
		args := parseCommand(command)
		if len(args) == 0 {
			return 0, fmt.Errorf("empty command")
		}
		cmd = exec.Command(args[0], args[1:]...)
	}

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Set up stdout and stderr pipes
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return 0, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Set up stdout and stderr capture
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	process := &ProcessInfo{
		Command:     command,
		Cmd:         cmd,
		StartedAt:   time.Now(),
		CompletedAt: nil,
		Status:      "running",
		WorkingDir:  workingDir,
		stdout:      stdout,
		stderr:      stderr,
		stdoutPipe:  stdoutPipe,
		stderrPipe:  stderrPipe,
		logWriters:  make([]io.Writer, 0),
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	process.PID = cmd.Process.Pid
	pm.processes[process.PID] = process

	// Handle stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				process.stdout.Write(data)

				// Send to any attached log writers
				process.logLock.RLock()
				for _, w := range process.logWriters {
					w.Write(data)
				}
				process.logLock.RUnlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Handle stderr
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stderrPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				process.stderr.Write(data)

				// Send to any attached log writers
				process.logLock.RLock()
				for _, w := range process.logWriters {
					w.Write(data)
				}
				process.logLock.RUnlock()
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		now := time.Now()

		pm.processLock.Lock()
		defer pm.processLock.Unlock()

		process.CompletedAt = &now

		// Determine exit status and create appropriate message
		var statusMsg string
		if err != nil {
			process.Status = "failed"
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.ExitCode = exitErr.ExitCode()
				statusMsg = fmt.Sprintf("\n[Process exited with code %d]\n", process.ExitCode)
			} else {
				process.ExitCode = 1
				statusMsg = fmt.Sprintf("\n[Process failed: %v]\n", err)
			}
		} else {
			process.Status = "completed"
			process.ExitCode = 0
			statusMsg = "\n[Process completed successfully]\n"
		}

		// Notify log writers about process completion
		process.logLock.RLock()
		for _, w := range process.logWriters {
			w.Write([]byte(statusMsg))
		}
		process.logLock.RUnlock()

		// Add completion message to output buffer
		process.stdout.WriteString(statusMsg)

		// Clean up resources
		// Note: The pipes are automatically closed when the process ends
		process.logLock.Lock()
		process.logWriters = nil // Clear all log writers
		process.logLock.Unlock()

		callback(process)
	}()

	return process.PID, nil
}

// parseCommand splits a command string into arguments while respecting quotes
func parseCommand(command string) []string {
	var args []string
	var currentArg strings.Builder
	inQuotes := false
	quoteChar := rune(0)

	for _, char := range command {
		switch {
		case char == '"' || char == '\'':
			if inQuotes && char == quoteChar {
				// End of quoted section
				inQuotes = false
				quoteChar = rune(0)
			} else if !inQuotes {
				// Start of quoted section
				inQuotes = true
				quoteChar = char
			} else {
				// Quote character inside another type of quotes, treat as literal
				currentArg.WriteRune(char)
			}
		case char == ' ' && !inQuotes:
			// Space outside quotes ends the current argument
			if currentArg.Len() > 0 {
				args = append(args, currentArg.String())
				currentArg.Reset()
			}
		default:
			// Add character to current argument
			currentArg.WriteRune(char)
		}
	}

	// Add the last argument if any
	if currentArg.Len() > 0 {
		args = append(args, currentArg.String())
	}

	return args
}

// GetProcess returns information about a specific process
func (pm *ProcessManager) GetProcess(pid int) (*ProcessInfo, bool) {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	process, exists := pm.processes[pid]
	return process, exists
}

// ListProcesses returns information about all processes
func (pm *ProcessManager) ListProcesses() []*ProcessInfo {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	processes := make([]*ProcessInfo, 0, len(pm.processes))
	for _, process := range pm.processes {
		processes = append(processes, process)
	}
	return processes
}

// StopProcess attempts to gracefully stop a process
func (pm *ProcessManager) StopProcess(pid int) error {
	pm.processLock.Lock()
	defer pm.processLock.Unlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	if process.Status != "running" {
		return fmt.Errorf("process with PID %d is not running", pid)
	}

	if process.Cmd.Process == nil {
		return fmt.Errorf("process with PID %d has no OS process", pid)
	}

	// Notify log writers about termination
	process.logLock.RLock()
	terminationMsg := []byte("\n[Process is being gracefully terminated]\n")
	for _, w := range process.logWriters {
		w.Write(terminationMsg)
	}
	process.logLock.RUnlock()

	// Add termination message to output buffers
	process.stdout.Write(terminationMsg)

	return process.Cmd.Process.Signal(syscall.SIGTERM)
}

// KillProcess forcefully kills a process
func (pm *ProcessManager) KillProcess(pid int) error {
	pm.processLock.Lock()
	defer pm.processLock.Unlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	if process.Status != "running" {
		return fmt.Errorf("process with PID %d is not running", pid)
	}

	if process.Cmd.Process == nil {
		return fmt.Errorf("process with PID %d has no OS process", pid)
	}

	// Notify log writers about forceful termination
	process.logLock.RLock()
	terminationMsg := []byte("\n[Process is being forcefully killed]\n")
	for _, w := range process.logWriters {
		w.Write(terminationMsg)
	}
	process.logLock.RUnlock()

	// Add termination message to output buffers
	process.stdout.Write(terminationMsg)

	return process.Cmd.Process.Kill()
}

// GetProcessOutput returns the stdout and stderr output of a process
func (pm *ProcessManager) GetProcessOutput(pid int) (string, string, error) {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	process, exists := pm.processes[pid]
	if !exists {
		return "", "", fmt.Errorf("process with PID %d not found", pid)
	}

	return process.stdout.String(), process.stderr.String(), nil
}

func (pm *ProcessManager) StreamProcessOutput(pid int, w io.Writer) error {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	// Write current content first
	w.Write([]byte(process.stdout.String()))
	w.Write([]byte(process.stderr.String()))

	// Attach writer for future output
	process.logLock.Lock()
	process.logWriters = append(process.logWriters, w)
	process.logLock.Unlock()

	return nil
}

// RemoveLogWriter removes a writer from a process's log writers list
func (pm *ProcessManager) RemoveLogWriter(pid int, w io.Writer) error {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	process.logLock.Lock()
	defer process.logLock.Unlock()

	for i, writer := range process.logWriters {
		if writer == w {
			// Remove this writer
			process.logWriters = append(process.logWriters[:i], process.logWriters[i+1:]...)
			return nil
		}
	}

	// Writer not found is not an error, just a no-op
	return nil
}

// RemoveAllLogWriters removes all writers from a process's log writers list
func (pm *ProcessManager) RemoveAllLogWriters(pid int) error {
	pm.processLock.RLock()
	defer pm.processLock.RUnlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	process.logLock.Lock()
	defer process.logLock.Unlock()

	// Clear all writers
	process.logWriters = make([]io.Writer, 0)
	return nil
}

// CleanupProcess performs cleanup for a process, removing it from the process manager
// This can be called after a process has completed and its output has been processed
func (pm *ProcessManager) CleanupProcess(pid int) error {
	pm.processLock.Lock()
	defer pm.processLock.Unlock()

	process, exists := pm.processes[pid]
	if !exists {
		return fmt.Errorf("process with PID %d not found", pid)
	}

	// Only allow cleanup for non-running processes
	if process.Status == "running" {
		return fmt.Errorf("cannot cleanup a running process with PID %d", pid)
	}

	// Clean up any remaining log writers
	process.logLock.Lock()
	process.logWriters = nil
	process.logLock.Unlock()

	// Remove from the process manager
	delete(pm.processes, pid)
	return nil
}
