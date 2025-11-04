package process

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
)

// Define process status constants
const (
	StatusFailed    = constants.ProcessStatusFailed
	StatusKilled    = constants.ProcessStatusKilled
	StatusStopped   = constants.ProcessStatusStopped
	StatusRunning   = constants.ProcessStatusRunning
	StatusCompleted = constants.ProcessStatusCompleted
)

// ProcessManager manages the running processes
type ProcessManager struct {
	processes map[string]*ProcessInfo
	mu        sync.RWMutex
}

type ProcessLogs struct {
	Stdout string `json:"stdout" example:"stdout output" binding:"required"`
	Stderr string `json:"stderr" example:"stderr output" binding:"required"`
	Logs   string `json:"logs" example:"logs output" binding:"required"`
} // @name ProcessLogs

// ProcessInfo stores information about a running process
type ProcessInfo struct {
	PID              string                  `json:"pid"`
	Name             string                  `json:"name"`
	Command          string                  `json:"command"`
	ProcessPid       int                     `json:"-"` // Store the OS process PID for kill/stop operations
	StartedAt        time.Time               `json:"startedAt"`
	CompletedAt      *time.Time              `json:"completedAt"`
	ExitCode         int                     `json:"exitCode"`
	Status           constants.ProcessStatus `json:"status"`
	WorkingDir       string                  `json:"workingDir"`
	Logs             *string                 `json:"logs"`
	RestartOnFailure bool                    `json:"restartOnFailure"`
	MaxRestarts      int                     `json:"maxRestarts"`
	RestartCount     int                     `json:"restartCount"`
	stdout           *strings.Builder
	stderr           *strings.Builder
	logs             *strings.Builder
	stdoutPipe       io.ReadCloser
	stderrPipe       io.ReadCloser
	logWriters       []io.Writer
	logLock          sync.RWMutex
}

// NewProcessManager creates a new process manager
func NewProcessManager() *ProcessManager {
	return &ProcessManager{
		processes: make(map[string]*ProcessInfo),
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

func (pm *ProcessManager) StartProcess(command string, workingDir string, env map[string]string, restartOnFailure bool, maxRestarts int, callback func(process *ProcessInfo)) (string, error) {
	name := GenerateRandomName(8)
	return pm.StartProcessWithName(command, workingDir, name, env, restartOnFailure, maxRestarts, callback)
}

func (pm *ProcessManager) StartProcessWithName(command string, workingDir string, name string, env map[string]string, restartOnFailure bool, maxRestarts int, callback func(process *ProcessInfo)) (string, error) {
	// Always use shell to execute commands
	// This ensures shell built-ins (cd, export, alias) work properly
	// Use SHELL and SHELL_ARGS environment variables if set
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}

	shellArgs := os.Getenv("SHELL_ARGS")
	if shellArgs == "" {
		shellArgs = "-c"
	}

	// Build command arguments
	cmdArgs := []string{}
	if shellArgs != "" {
		cmdArgs = append(cmdArgs, strings.Fields(shellArgs)...)
	}
	cmdArgs = append(cmdArgs, command)

	cmd := exec.Command(shell, cmdArgs...)

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Set up process group to ensure all child processes can be killed together
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Start with system environment
	systemEnv := os.Environ()

	// Create a map to track which env vars we're overriding
	envOverrides := make(map[string]bool)
	for k := range env {
		envOverrides[k] = true
	}

	// Build the final environment
	finalEnv := make([]string, 0, len(systemEnv)+len(env))

	// Add system environment variables that are not being overridden
	for _, envVar := range systemEnv {
		// Find the key part (everything before the first '=')
		idx := strings.IndexByte(envVar, '=')
		if idx > 0 {
			key := envVar[:idx]
			if !envOverrides[key] {
				finalEnv = append(finalEnv, envVar)
			}
		}
	}

	// Add all custom environment variables
	for k, v := range env {
		finalEnv = append(finalEnv, k+"="+v)
	}

	cmd.Env = finalEnv

	// Set up stdout and stderr pipes
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Ensure maxRestarts doesn't exceed the limit
	if maxRestarts > 25 {
		maxRestarts = 25
	}

	// Set up stdout and stderr capture
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	logs := &strings.Builder{}
	process := &ProcessInfo{
		Name:             name,
		Command:          command,
		StartedAt:        time.Now(),
		CompletedAt:      nil,
		Status:           StatusRunning,
		WorkingDir:       workingDir,
		RestartOnFailure: restartOnFailure,
		MaxRestarts:      maxRestarts,
		RestartCount:     0,
		stdout:           stdout,
		stderr:           stderr,
		logs:             logs,
		stdoutPipe:       stdoutPipe,
		stderrPipe:       stderrPipe,
		logWriters:       make([]io.Writer, 0),
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return "", err
	}
	process.PID = fmt.Sprintf("%d", cmd.Process.Pid)
	process.ProcessPid = cmd.Process.Pid
	// Store process in memory
	pm.mu.Lock()
	pm.processes[process.PID] = process
	pm.mu.Unlock()

	// Handle stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				process.logLock.Lock()
				process.stdout.Write(data)
				process.logs.Write(data)
				// Send to any attached log writers, prefix with stdout:
				for _, w := range process.logWriters {
					fullMsg := append([]byte("stdout:"), data...)
					_, _ = w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
				}
				process.logLock.Unlock()
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
				process.logLock.Lock()
				process.stderr.Write(data)
				process.logs.Write(data)
				// Send to any attached log writers, prefix with stderr:
				for _, w := range process.logWriters {
					fullMsg := append([]byte("stderr:"), data...)
					_, _ = w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
				}
				process.logLock.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	go func() {
		err := cmd.Wait()
		now := time.Now()

		// IMPORTANT: Release process resources immediately after Wait() to close pidfd
		// This must be done right after Wait() completes to prevent FD leaks
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}

		process.CompletedAt = &now

		// Determine exit status and create appropriate message
		if err != nil {
			if process.Status != StatusStopped && process.Status != StatusKilled {
				process.Status = StatusFailed
			}
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.ExitCode = exitErr.ExitCode()
			} else {
				process.ExitCode = 1
			}
		} else {
			process.Status = StatusCompleted
			process.ExitCode = 0
		}

		// Update process in memory
		pm.mu.Lock()
		pm.processes[process.PID] = process
		pm.mu.Unlock()

		// Check if we should restart on failure
		if process.Status == StatusFailed && process.RestartOnFailure && process.RestartCount < process.MaxRestarts {
			// Log the failure and restart attempt
			restartMsg := fmt.Sprintf("\n[Process failed with exit code %d. Attempting restart %d/%d...]\n",
				process.ExitCode, process.RestartCount+1, process.MaxRestarts)

			process.stdout.WriteString(restartMsg)
			process.logs.WriteString(restartMsg)

			// Notify log writers about the restart
			process.logLock.RLock()
			for _, w := range process.logWriters {
				_, _ = w.Write([]byte(restartMsg))
				if f, ok := w.(interface{ Flush() }); ok {
					f.Flush()
				}
			}
			process.logLock.RUnlock()

			// Increment restart count
			process.RestartCount++

			// Small delay before restart to avoid rapid restart loops
			time.Sleep(1 * time.Second)

			// Restart the process with updated restart count
			// The PID remains the same across restarts for user transparency
			_, restartErr := pm.restartProcess(process, callback)
			if restartErr != nil {
				// If restart fails, log the error and call the callback
				errorMsg := fmt.Sprintf("\n[Failed to restart process: %v]\n", restartErr)
				process.stdout.WriteString(errorMsg)
				process.logs.WriteString(errorMsg)

				// Clean up resources
				process.logLock.Lock()
				process.logWriters = nil // Clear all log writers
				process.logLock.Unlock()

				callback(process)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// Clean up resources
			process.logLock.Lock()
			process.logWriters = nil // Clear all log writers
			process.logLock.Unlock()

			callback(process)
		}
	}()

	return process.PID, nil
}

// restartProcess restarts a failed process with the same configuration
func (pm *ProcessManager) restartProcess(oldProcess *ProcessInfo, callback func(process *ProcessInfo)) (string, error) {
	command := oldProcess.Command
	workingDir := oldProcess.WorkingDir

	// Always use shell to execute commands (same as StartProcessWithName)
	// This ensures shell built-ins (cd, export, exit, alias) work properly
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}

	shellArgs := os.Getenv("SHELL_ARGS")
	if shellArgs == "" {
		shellArgs = "-c"
	}

	// Build command arguments
	cmdArgs := []string{}
	if shellArgs != "" {
		cmdArgs = append(cmdArgs, strings.Fields(shellArgs)...)
	}
	cmdArgs = append(cmdArgs, command)

	cmd := exec.Command(shell, cmdArgs...)

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Set up process group to ensure all child processes can be killed together
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Use the same environment as the original process
	cmd.Env = os.Environ()

	// Set up stdout and stderr pipes
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Keep the existing process info but reset status
	oldProcess.Status = StatusRunning
	oldProcess.StartedAt = time.Now()
	oldProcess.CompletedAt = nil
	oldProcess.ExitCode = 0
	oldProcess.stdoutPipe = stdoutPipe
	oldProcess.stderrPipe = stderrPipe

	// Start the process
	if err := cmd.Start(); err != nil {
		return "", err
	}

	// Update only the OS process PID for kill/stop operations
	// Keep the user-facing PID (oldProcess.PID) unchanged for transparency
	oldProcess.ProcessPid = cmd.Process.Pid

	// Update the process in memory (same map key, just updating the entry)
	pm.mu.Lock()
	pm.processes[oldProcess.PID] = oldProcess
	pm.mu.Unlock()

	// Handle stdout
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := stdoutPipe.Read(buf)
			if n > 0 {
				data := buf[:n]
				oldProcess.logLock.Lock()
				oldProcess.stdout.Write(data)
				oldProcess.logs.Write(data)
				// Send to any attached log writers, prefix with stdout:
				for _, w := range oldProcess.logWriters {
					fullMsg := append([]byte("stdout:"), data...)
					_, _ = w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
				}
				oldProcess.logLock.Unlock()
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
				oldProcess.logLock.Lock()
				oldProcess.stderr.Write(data)
				oldProcess.logs.Write(data)
				// Send to any attached log writers, prefix with stderr:
				for _, w := range oldProcess.logWriters {
					fullMsg := append([]byte("stderr:"), data...)
					_, _ = w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
				}
				oldProcess.logLock.Unlock()
			}
			if err != nil {
				break
			}
		}
	}()

	// Monitor the restarted process
	go func() {
		err := cmd.Wait()
		now := time.Now()

		// IMPORTANT: Release process resources immediately after Wait() to close pidfd
		// This must be done right after Wait() completes to prevent FD leaks
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}

		oldProcess.CompletedAt = &now

		// Determine exit status
		if err != nil {
			if oldProcess.Status != StatusStopped && oldProcess.Status != StatusKilled {
				oldProcess.Status = StatusFailed
			}
			if exitErr, ok := err.(*exec.ExitError); ok {
				oldProcess.ExitCode = exitErr.ExitCode()
			} else {
				oldProcess.ExitCode = 1
			}
		} else {
			oldProcess.Status = StatusCompleted
			oldProcess.ExitCode = 0
		}

		// Update process in memory (PID stays the same, just updating the entry)
		pm.mu.Lock()
		pm.processes[oldProcess.PID] = oldProcess
		pm.mu.Unlock()

		// Check if we should restart again on failure
		if oldProcess.Status == StatusFailed && oldProcess.RestartOnFailure && oldProcess.RestartCount < oldProcess.MaxRestarts {
			// Log the failure and restart attempt
			restartMsg := fmt.Sprintf("\n[Process failed with exit code %d. Attempting restart %d/%d...]\n",
				oldProcess.ExitCode, oldProcess.RestartCount+1, oldProcess.MaxRestarts)

			oldProcess.stdout.WriteString(restartMsg)
			oldProcess.logs.WriteString(restartMsg)

			// Notify log writers about the restart
			oldProcess.logLock.RLock()
			for _, w := range oldProcess.logWriters {
				_, _ = w.Write([]byte(restartMsg))
				if f, ok := w.(interface{ Flush() }); ok {
					f.Flush()
				}
			}
			oldProcess.logLock.RUnlock()

			// Increment restart count
			oldProcess.RestartCount++

			// Small delay before restart to avoid rapid restart loops
			time.Sleep(1 * time.Second)

			// Restart the process recursively
			// The PID remains the same across restarts for user transparency
			_, restartErr := pm.restartProcess(oldProcess, callback)
			if restartErr != nil {
				// If restart fails, log the error and call the callback
				errorMsg := fmt.Sprintf("\n[Failed to restart process: %v]\n", restartErr)
				oldProcess.stdout.WriteString(errorMsg)
				oldProcess.logs.WriteString(errorMsg)

				// Clean up resources
				oldProcess.logLock.Lock()
				oldProcess.logWriters = nil
				oldProcess.logLock.Unlock()

				callback(oldProcess)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// Clean up resources
			oldProcess.logLock.Lock()
			oldProcess.logWriters = nil
			oldProcess.logLock.Unlock()

			callback(oldProcess)
		}
	}()

	return oldProcess.PID, nil
}

// GetProcessByIdentifier returns a process by either PID or name
func (pm *ProcessManager) GetProcessByIdentifier(identifier string) (*ProcessInfo, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Try to convert identifier to int (PID)
	if _, err := strconv.Atoi(identifier); err == nil {
		// If conversion successful, try to get process by PID
		process, exists := pm.processes[identifier]
		if !exists {
			return nil, false
		}

		// If the process is running, try to get additional information from the OS
		if process.Status == StatusRunning {
			pidInt, err := strconv.Atoi(process.PID)
			if err == nil {
				// Store the OS process PID for kill/stop operations
				process.ProcessPid = pidInt
			}
		}
		if process.logs != nil && process.logs.Len() > 0 {
			logs := process.logs.String()
			process.Logs = &logs
		}
		return process, true
	}
	// Search by name - find the most recent process with this name
	var latestProcess *ProcessInfo
	for _, process := range pm.processes {
		if process.Name == identifier {
			if latestProcess == nil || process.StartedAt.After(latestProcess.StartedAt) {
				latestProcess = process
			}
		}
	}

	if latestProcess != nil {
		if latestProcess.logs != nil {
			logs := latestProcess.logs.String()
			latestProcess.Logs = &logs
		}
		return latestProcess, true
	}

	return nil, false
}

// ListProcesses returns information about all processes
func (pm *ProcessManager) ListProcesses() []*ProcessInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	processes := make([]*ProcessInfo, 0, len(pm.processes))
	for _, process := range pm.processes {
		processes = append(processes, process)
	}
	return processes
}

// StopProcess attempts to gracefully stop a process
func (pm *ProcessManager) StopProcess(identifier string) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	if process.Status != StatusRunning {
		return fmt.Errorf("process with Identifier %s is not running", identifier)
	}

	if process.ProcessPid == 0 {
		return fmt.Errorf("process with Identifier %s has no OS process", identifier)
	}

	// Notify log writers about termination
	process.logLock.RLock()
	terminationMsg := []byte("\n[Process is being gracefully terminated]\n")
	for _, w := range process.logWriters {
		_, _ = w.Write(terminationMsg)
	}
	process.logLock.RUnlock()

	// Add termination message to output buffers
	process.stdout.Write(terminationMsg)

	// Try to gracefully terminate the entire process group first
	pid := process.ProcessPid

	// Send SIGTERM to the process group (negative PID targets the process group)
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if err != nil {
		// If process group termination fails, fall back to terminating just the process
		err = syscall.Kill(pid, syscall.SIGTERM)
		if err != nil {
			if err.Error() != "os: process already finished" {
				return fmt.Errorf("failed to send SIGTERM to process with Identifier %s: %w", identifier, err)
			}
		}
	}

	process.Status = StatusStopped
	return nil
}

// KillProcess forcefully kills a process
func (pm *ProcessManager) KillProcess(identifier string) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	if process.ProcessPid == 0 {
		return fmt.Errorf("process with Identifier %s has no OS process", identifier)
	}

	// Notify log writers about forceful termination
	process.logLock.RLock()
	terminationMsg := []byte("\n[Process is being forcefully killed]\n")
	for _, w := range process.logWriters {
		_, _ = w.Write(terminationMsg)
	}
	process.logLock.RUnlock()

	// Add termination message to output buffers
	process.stdout.Write(terminationMsg)

	// Kill the entire process group to ensure all child processes are terminated
	// This is crucial for processes like Next.js dev servers that spawn child processes
	pid := process.ProcessPid

	// First try to kill the process group (negative PID kills the process group)
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil {
		// If process group kill fails, fall back to killing just the process
		// This might happen if the process didn't create a process group
		err = syscall.Kill(pid, syscall.SIGKILL)
		if err != nil {
			if err.Error() != "os: process already finished" {
				return fmt.Errorf("failed to kill process with Identifier %s: %w", identifier, err)
			}
		}
	}

	// Remove the process from memory
	process.Status = StatusKilled
	return nil
}

// GetProcessOutput returns the stdout and stderr output of a process
func (pm *ProcessManager) GetProcessOutput(identifier string) (ProcessLogs, error) {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return ProcessLogs{}, fmt.Errorf("process with PID %s not found", identifier)
	}

	return ProcessLogs{
		Stdout: process.stdout.String(),
		Stderr: process.stderr.String(),
		Logs:   process.logs.String(),
	}, nil
}

func (pm *ProcessManager) StreamProcessOutput(identifier string, w io.Writer) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	// Write current content first
	_, _ = w.Write([]byte(process.logs.String()))

	// Attach writer for future output
	process.logLock.Lock()
	process.logWriters = append(process.logWriters, w)
	process.logLock.Unlock()

	// Start keepalive goroutine to prevent connection timeout
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			process, exists = pm.GetProcessByIdentifier(identifier)
			// Check if process is still running
			if !exists || process.Status != StatusRunning {
				return
			}
			// Send keepalive message only to this specific writer
			keepaliveMsg := []byte("[keepalive]\n")
			_, _ = w.Write(keepaliveMsg)
			if f, ok := w.(interface{ Flush() }); ok {
				f.Flush()
			}
		}
	}()

	return nil
}

// RemoveLogWriter removes a writer from a process's log writers list
func (pm *ProcessManager) RemoveLogWriter(identifier string, w io.Writer) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
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

func GenerateRandomName(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	randomName := strings.Builder{}
	randomName.WriteString("proc-")

	// Generate random string
	for i := 0; i < length; i++ {
		randomIndex := rand.Intn(len(charset))
		randomName.WriteByte(charset[randomIndex])
	}

	return randomName.String()
}
