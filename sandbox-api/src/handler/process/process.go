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

// StreamEvent represents a streaming event sent to JSON log writers
type StreamEvent struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

// JSONStreamWriter is an interface for writers that want JSON-formatted events
type JSONStreamWriter interface {
	io.Writer
	WriteEvent(eventType string, data string) (int, error)
	IsJSONStreamWriter() bool
}

// writeToLogWriter sends data to a log writer, using JSON format if supported
func writeToLogWriter(w io.Writer, eventType string, data []byte) {
	if jw, ok := w.(JSONStreamWriter); ok {
		// JSON writer - send structured event
		jw.WriteEvent(eventType, string(data))
	} else {
		// Regular writer - send prefixed text (stdout: or stderr:)
		prefixed := append([]byte(eventType+":"), data...)
		_, _ = w.Write(prefixed)
	}
	if f, ok := w.(interface{ Flush() }); ok {
		f.Flush()
	}
}

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
	Stdout           *string                 `json:"stdout"`
	Stderr           *string                 `json:"stderr"`
	RestartOnFailure bool                    `json:"restartOnFailure"`
	MaxRestarts      int                     `json:"maxRestarts"`
	RestartCount     int                     `json:"restartCount"`
	LogFile          string                  `json:"-"` // Path to combined log file
	StdoutFile       string                  `json:"-"` // Path to stdout log file
	StderrFile       string                  `json:"-"` // Path to stderr log file
	Done             chan struct{}
	stdout           *strings.Builder
	stderr           *strings.Builder
	logs             *strings.Builder
	logWriters       []io.Writer
	logLock          sync.RWMutex
}

// ProcessLogDir is the directory where process logs are stored
// Can be configured via SANDBOX_LOG_DIR environment variable
var ProcessLogDir = "/var/log/sandbox-api"

func init() {
	if dir := os.Getenv("SANDBOX_LOG_DIR"); dir != "" {
		ProcessLogDir = dir
	}
}

// getLogFilePaths returns the log file paths for a process (stdout, stderr, combined)
func getLogFilePaths(name string) (stdout, stderr, combined string) {
	stdout = fmt.Sprintf("%s/%s.stdout.log", ProcessLogDir, name)
	stderr = fmt.Sprintf("%s/%s.stderr.log", ProcessLogDir, name)
	combined = fmt.Sprintf("%s/%s.log", ProcessLogDir, name)
	return
}

// ensureLogDir ensures the log directory exists
func ensureLogDir() error {
	return os.MkdirAll(ProcessLogDir, 0755)
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
		// Check if the working directory exists
		if _, err := os.Stat(workingDir); os.IsNotExist(err) {
			return "", fmt.Errorf("could not execute command '%s' because folder '%s' does not exist", command, workingDir)
		} else if err != nil {
			return "", fmt.Errorf("could not access working directory '%s': %w", workingDir, err)
		}
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

	// Add all custom environment variables (these take priority)
	for k, v := range env {
		finalEnv = append(finalEnv, k+"="+v)
	}

	cmd.Env = finalEnv

	// Ensure log directory exists
	if err := ensureLogDir(); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	// Set up in-memory buffers
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	logs := &strings.Builder{}

	// Create separate log files for stdout and stderr
	// Child processes write DIRECTLY to these files (no pipes)
	// This ensures processes survive sandbox-api restarts
	stdoutPath, stderrPath, combinedPath := getLogFilePaths(name)

	stdoutFile, err := os.OpenFile(stdoutPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to create stdout log file: %w", err)
	}

	stderrFile, err := os.OpenFile(stderrPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		stdoutFile.Close()
		return "", fmt.Errorf("failed to create stderr log file: %w", err)
	}

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
		LogFile:          combinedPath,
		StdoutFile:       stdoutPath,
		StderrFile:       stderrPath,
		Done:             make(chan struct{}),
		stdout:           stdout,
		stderr:           stderr,
		logs:             logs,
		logWriters:       make([]io.Writer, 0),
	}

	// Redirect stdout/stderr directly to files
	// This is crucial - child writes to files, not pipes
	// So child survives sandbox-api restart without blocking
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// Start the process
	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		os.Remove(stdoutPath)
		os.Remove(stderrPath)
		return "", err
	}

	process.PID = fmt.Sprintf("%d", cmd.Process.Pid)
	process.ProcessPid = cmd.Process.Pid

	// Close the write handles in parent - child has its own FDs
	stdoutFile.Close()
	stderrFile.Close()

	// Store process in memory
	pm.mu.Lock()
	pm.processes[process.PID] = process
	pm.mu.Unlock()

	// Start file tailer for real-time log streaming
	go pm.tailLogFiles(process)

	// Monitor process completion
	go func() {
		err := cmd.Wait()

		// IMPORTANT: Release process resources immediately after Wait() to close pidfd
		// This must be done right after Wait() completes to prevent FD leaks
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}

		// Small delay to allow filesystem to sync writes from the child process
		// This is necessary on macOS where file writes may not be immediately visible
		// to readers in other goroutines due to filesystem caching
		time.Sleep(1 * time.Millisecond)

		now := time.Now()
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

			process.logLock.Lock()
			process.stdout.WriteString(restartMsg)
			process.logs.WriteString(restartMsg)

			// Append restart message to log files
			if process.StdoutFile != "" {
				if f, err := os.OpenFile(process.StdoutFile, os.O_APPEND|os.O_WRONLY, 0644); err == nil {
					f.WriteString(restartMsg)
					f.Close()
				}
			}

			// Notify log writers about the restart
			for _, w := range process.logWriters {
				_, _ = w.Write([]byte(restartMsg))
				if f, ok := w.(interface{ Flush() }); ok {
					f.Flush()
				}
			}
			process.logLock.Unlock()

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

				// Signal that the process is done
				close(process.Done)
				callback(process)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// Clean up resources
			process.logLock.Lock()
			process.logWriters = nil // Clear all log writers
			process.logLock.Unlock()

			// Signal that the process is done
			close(process.Done)
			callback(process)
		}
	}()

	return process.PID, nil
}

// tailLogFiles tails the stdout and stderr log files for real-time streaming
func (pm *ProcessManager) tailLogFiles(proc *ProcessInfo) {
	// Open files for reading
	stdoutFile, err := os.Open(proc.StdoutFile)
	if err != nil {
		return
	}
	defer stdoutFile.Close()

	stderrFile, err := os.Open(proc.StderrFile)
	if err != nil {
		return
	}
	defer stderrFile.Close()

	// Open combined log file for writing prefixed output (preserves order)
	var combinedFile *os.File
	if proc.LogFile != "" {
		combinedFile, err = os.OpenFile(proc.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			combinedFile = nil
		} else {
			defer combinedFile.Close()
		}
	}

	stdoutBuf := make([]byte, 4096)
	stderrBuf := make([]byte, 4096)

	for {
		select {
		case <-proc.Done:
			// Do final reads
			pm.readAndBroadcast(stdoutFile, stdoutBuf, proc, "stdout", combinedFile)
			pm.readAndBroadcast(stderrFile, stderrBuf, proc, "stderr", combinedFile)
			return
		default:
			// Read from stdout file
			pm.readAndBroadcast(stdoutFile, stdoutBuf, proc, "stdout", combinedFile)
			// Read from stderr file
			pm.readAndBroadcast(stderrFile, stderrBuf, proc, "stderr", combinedFile)
			// Small sleep to avoid busy loop
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// readAndBroadcast reads from a file and broadcasts to log writers
func (pm *ProcessManager) readAndBroadcast(file *os.File, buf []byte, proc *ProcessInfo, streamType string, combinedFile *os.File) {
	n, err := file.Read(buf)
	if n > 0 {
		data := buf[:n]
		proc.logLock.Lock()
		if streamType == "stdout" {
			proc.stdout.Write(data)
		} else {
			proc.stderr.Write(data)
		}
		proc.logs.Write(data)
		// Write prefixed content to combined log file (preserves interleaved order)
		if combinedFile != nil {
			// Write each line with prefix
			lines := strings.SplitAfter(string(data), "\n")
			for _, line := range lines {
				if line != "" {
					combinedFile.WriteString(streamType + ":" + line)
				}
			}
		}
		// Send to log writers for streaming
		for _, w := range proc.logWriters {
			writeToLogWriter(w, streamType, data)
		}
		proc.logLock.Unlock()
	}
	if err != nil && err != io.EOF {
		// Real error, but we'll keep trying
	}
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
		// Check if the working directory exists
		if _, err := os.Stat(workingDir); os.IsNotExist(err) {
			return "", fmt.Errorf("could not execute command '%s' because folder '%s' does not exist", command, workingDir)
		} else if err != nil {
			return "", fmt.Errorf("could not access working directory '%s': %w", workingDir, err)
		}
		cmd.Dir = workingDir
	}

	// Set up process group to ensure all child processes can be killed together
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Use the same environment as the original process
	cmd.Env = os.Environ()

	// Open log files for appending - child writes directly to files
	stdoutFile, err := os.OpenFile(oldProcess.StdoutFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to open stdout log file: %w", err)
	}

	stderrFile, err := os.OpenFile(oldProcess.StderrFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		stdoutFile.Close()
		return "", fmt.Errorf("failed to open stderr log file: %w", err)
	}

	// Redirect stdout/stderr directly to files (no pipes)
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	// Keep the existing process info but reset status
	oldProcess.Status = StatusRunning
	oldProcess.StartedAt = time.Now()
	oldProcess.CompletedAt = nil
	oldProcess.ExitCode = 0

	// Start the process
	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		return "", err
	}

	// Update only the OS process PID for kill/stop operations
	// Keep the user-facing PID (oldProcess.PID) unchanged for transparency
	oldProcess.ProcessPid = cmd.Process.Pid

	// Close write handles in parent - child has its own FDs
	stdoutFile.Close()
	stderrFile.Close()

	// Update the process in memory (same map key, just updating the entry)
	pm.mu.Lock()
	pm.processes[oldProcess.PID] = oldProcess
	pm.mu.Unlock()

	// Start file tailer for real-time log streaming
	go pm.tailLogFiles(oldProcess)

	// Monitor the restarted process
	go func() {
		err := cmd.Wait()

		// IMPORTANT: Release process resources immediately after Wait() to close pidfd
		// This must be done right after Wait() completes to prevent FD leaks
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}

		// Small delay to allow filesystem to sync writes from the child process
		// This is necessary on macOS where file writes may not be immediately visible
		// to readers in other goroutines due to filesystem caching
		time.Sleep(1 * time.Millisecond)

		now := time.Now()
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

			oldProcess.logLock.Lock()
			oldProcess.stdout.WriteString(restartMsg)
			oldProcess.logs.WriteString(restartMsg)

			// Append restart message to log file
			if oldProcess.StdoutFile != "" {
				if f, err := os.OpenFile(oldProcess.StdoutFile, os.O_APPEND|os.O_WRONLY, 0644); err == nil {
					f.WriteString(restartMsg)
					f.Close()
				}
			}

			// Notify log writers about the restart
			for _, w := range oldProcess.logWriters {
				_, _ = w.Write([]byte(restartMsg))
				if f, ok := w.(interface{ Flush() }); ok {
					f.Flush()
				}
			}
			oldProcess.logLock.Unlock()

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

				// Signal that the process is done
				close(oldProcess.Done)
				callback(oldProcess)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// Clean up resources
			oldProcess.logLock.Lock()
			oldProcess.logWriters = nil
			oldProcess.logLock.Unlock()

			// Signal that the process is done
			close(oldProcess.Done)
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
		// Acquire logLock to safely read logs (they're written under this lock)
		process.logLock.RLock()
		if process.logs != nil && process.logs.Len() > 0 {
			logs := process.logs.String()
			process.Logs = &logs
		}
		if process.stdout != nil {
			stdout := process.stdout.String()
			process.Stdout = &stdout
		}
		if process.stderr != nil {
			stderr := process.stderr.String()
			process.Stderr = &stderr
		}
		process.logLock.RUnlock()
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
		// Acquire logLock to safely read logs (they're written under this lock)
		latestProcess.logLock.RLock()
		if latestProcess.logs != nil {
			logs := latestProcess.logs.String()
			latestProcess.Logs = &logs
		}
		if latestProcess.stdout != nil {
			stdout := latestProcess.stdout.String()
			latestProcess.Stdout = &stdout
		}
		if latestProcess.stderr != nil {
			stderr := latestProcess.stderr.String()
			latestProcess.Stderr = &stderr
		}
		latestProcess.logLock.RUnlock()
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

	// Try to read from separate log files if available
	var stdout, stderr, logs string

	// Read stdout from file or memory
	if process.StdoutFile != "" {
		if content, err := os.ReadFile(process.StdoutFile); err == nil {
			stdout = string(content)
		} else {
			stdout = process.stdout.String()
		}
	} else {
		stdout = process.stdout.String()
	}

	// Read stderr from file or memory
	if process.StderrFile != "" {
		if content, err := os.ReadFile(process.StderrFile); err == nil {
			stderr = string(content)
		} else {
			stderr = process.stderr.String()
		}
	} else {
		stderr = process.stderr.String()
	}

	// Combined logs
	logs = stdout + stderr

	return ProcessLogs{
		Stdout: stdout,
		Stderr: stderr,
		Logs:   logs,
	}, nil
}

func (pm *ProcessManager) StreamProcessOutput(identifier string, w io.Writer) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	// Write current content first - read from combined log file which has prefixed, ordered content
	// The combined log file is written by tailLogFiles with "stdout:" and "stderr:" prefixes
	if process.LogFile != "" {
		if content, err := os.ReadFile(process.LogFile); err == nil && len(content) > 0 {
			// Parse prefixed lines and send as proper events
			// This ensures JSONStreamWriter receives structured stdout/stderr events
			lines := strings.Split(string(content), "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "stdout:") {
					writeToLogWriter(w, "stdout", []byte(strings.TrimPrefix(line, "stdout:")+"\n"))
				} else if strings.HasPrefix(line, "stderr:") {
					writeToLogWriter(w, "stderr", []byte(strings.TrimPrefix(line, "stderr:")+"\n"))
				} else if line != "" {
					// Fallback for unprefixed lines (shouldn't happen, but handle gracefully)
					writeToLogWriter(w, "stdout", []byte(line+"\n"))
				}
			}
		}
	}

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
