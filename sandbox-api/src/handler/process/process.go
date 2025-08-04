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
	Cmd              *exec.Cmd               `json:"cmd"`
	StartedAt        time.Time               `json:"startedAt"`
	CompletedAt      *time.Time              `json:"completedAt"`
	ExitCode         int                     `json:"exitCode"`
	Status           constants.ProcessStatus `json:"status"`
	WorkingDir       string                  `json:"workingDir"`
	Logs             *string                 `json:"logs"`
	RestartOnFailure bool                    `json:"restartOnFailure"`
	MaxRestarts      int                     `json:"maxRestarts"`
	CurrentRestarts  int                     `json:"currentRestarts"`
	Env              map[string]string       `json:"env"`
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

func (pm *ProcessManager) StartProcess(command string, workingDir string, env map[string]string, callback func(process *ProcessInfo)) (string, error) {
	name := GenerateRandomName(8)
	return pm.StartProcessWithName(command, workingDir, name, env, false, 0, callback)
}

func (pm *ProcessManager) StartProcessWithName(command string, workingDir string, name string, env map[string]string, restartOnFailure bool, maxRestarts int, callback func(process *ProcessInfo)) (string, error) {
	// Validate maxRestarts limit
	if maxRestarts > 25 {
		return "", fmt.Errorf("maxRestarts cannot exceed 25, got %d", maxRestarts)
	}

	// Convert maxRestarts = 0 (unlimited) to maxRestarts = 25 (our max limit)
	if maxRestarts == 0 && restartOnFailure {
		maxRestarts = 25
	}

	process := &ProcessInfo{
		Name:             name,
		Command:          command,
		WorkingDir:       workingDir,
		RestartOnFailure: restartOnFailure,
		MaxRestarts:      maxRestarts,
		CurrentRestarts:  0,
		Env:              env,
		stdout:           &strings.Builder{},
		stderr:           &strings.Builder{},
		logs:             &strings.Builder{},
		logWriters:       make([]io.Writer, 0),
	}

	err := pm.startProcess(process, callback)
	if err != nil {
		return "", err
	}

	return process.PID, nil
}

// startProcess handles the common process startup logic
func (pm *ProcessManager) startProcess(process *ProcessInfo, callback func(process *ProcessInfo)) error {
	// Create and configure the command
	cmd, err := pm.createCommand(process.Command, process.WorkingDir, process.Env)
	if err != nil {
		return err
	}

	// Set up pipes
	stdoutPipe, stderrPipe, err := pm.setupPipes(cmd)
	if err != nil {
		return err
	}

	// Update process info
	process.Cmd = cmd
	process.StartedAt = time.Now()
	process.CompletedAt = nil
	process.Status = StatusRunning
	process.ExitCode = 0
	process.stdoutPipe = stdoutPipe
	process.stderrPipe = stderrPipe

	// Start the process
	if err := cmd.Start(); err != nil {
		return err
	}

	// Update PID and store in memory
	oldPID := process.PID
	process.PID = fmt.Sprintf("%d", cmd.Process.Pid)

	pm.mu.Lock()
	if oldPID != "" {
		delete(pm.processes, oldPID) // Remove old PID entry for restarts
	}
	pm.processes[process.PID] = process
	pm.mu.Unlock()

	// Handle output streams
	pm.handleProcessOutput(process)

	// Handle process completion
	go pm.handleProcessCompletion(process, callback)

	return nil
}

// createCommand creates and configures an exec.Cmd
func (pm *ProcessManager) createCommand(command, workingDir string, env map[string]string) (*exec.Cmd, error) {
	var cmd *exec.Cmd

	// Check if the command needs a shell
	if pm.needsShell(command) {
		cmd = exec.Command("sh", "-c", command)
	} else {
		args := parseCommand(command)
		if len(args) == 0 {
			return nil, fmt.Errorf("empty command")
		}
		cmd = exec.Command(args[0], args[1:]...)
	}

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Set up process group
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Set up environment
	cmd.Env = pm.buildEnvironment(env)

	return cmd, nil
}

// needsShell determines if a command needs to be executed through a shell
func (pm *ProcessManager) needsShell(command string) bool {
	shellChars := []string{"&&", "|", ">", "<", ";", "$"}
	for _, char := range shellChars {
		if strings.Contains(command, char) {
			return true
		}
	}
	return false
}

// buildEnvironment creates the final environment variables list
func (pm *ProcessManager) buildEnvironment(env map[string]string) []string {
	systemEnv := os.Environ()
	envOverrides := make(map[string]bool)

	for k := range env {
		envOverrides[k] = true
	}

	finalEnv := make([]string, 0, len(systemEnv)+len(env))

	// Add system environment variables that are not being overridden
	for _, envVar := range systemEnv {
		if idx := strings.IndexByte(envVar, '='); idx > 0 {
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

	return finalEnv
}

// setupPipes creates stdout and stderr pipes for the command
func (pm *ProcessManager) setupPipes(cmd *exec.Cmd) (io.ReadCloser, io.ReadCloser, error) {
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	return stdoutPipe, stderrPipe, nil
}

// handleProcessOutput sets up goroutines to handle stdout and stderr
func (pm *ProcessManager) handleProcessOutput(process *ProcessInfo) {
	// Handle stdout
	go pm.readPipe(process.stdoutPipe, process, "stdout")
	// Handle stderr
	go pm.readPipe(process.stderrPipe, process, "stderr")
}

// readPipe reads from a pipe and writes to the appropriate buffers
func (pm *ProcessManager) readPipe(pipe io.ReadCloser, process *ProcessInfo, streamType string) {
	buf := make([]byte, 4096)
	for {
		n, err := pipe.Read(buf)
		if n > 0 {
			data := buf[:n]

			// Write to logs buffer (always)
			process.logs.Write(data)

			// Write to specific stream buffer
			if streamType == "stdout" {
				process.stdout.Write(data)
			} else {
				process.stderr.Write(data)
			}

			// Send to log writers
			pm.writeToLogWriters(process, data, streamType)
		}
		if err != nil {
			break
		}
	}
}

// writeToLogWriters sends data to all attached log writers
func (pm *ProcessManager) writeToLogWriters(process *ProcessInfo, data []byte, streamType string) {
	process.logLock.RLock()
	defer process.logLock.RUnlock()

	var fullMsg []byte
	if streamType != "" {
		fullMsg = append([]byte(streamType+":"), data...)
	} else {
		fullMsg = data
	}

	for _, w := range process.logWriters {
		w.Write(fullMsg)
		if f, ok := w.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
}

// handleProcessCompletion handles process completion and restart logic
func (pm *ProcessManager) handleProcessCompletion(process *ProcessInfo, callback func(process *ProcessInfo)) {
	err := process.Cmd.Wait()
	now := time.Now()
	process.CompletedAt = &now

	shouldRestart := pm.updateProcessStatus(process, err)

	// Update process in memory
	pm.mu.Lock()
	pm.processes[process.PID] = process
	pm.mu.Unlock()

	if shouldRestart {
		pm.handleRestart(process, callback)
	} else {
		pm.cleanupProcess(process)
		callback(process)
	}
}

// updateProcessStatus updates the process status based on completion error
func (pm *ProcessManager) updateProcessStatus(process *ProcessInfo, err error) bool {
	if err != nil {
		if process.Status != StatusStopped && process.Status != StatusKilled {
			process.Status = StatusFailed

			// Set exit code
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.ExitCode = exitErr.ExitCode()
			} else {
				process.ExitCode = 1
			}

			// Log failure if restart is enabled
			if process.RestartOnFailure {
				failureMsg := fmt.Sprintf("--- Process failed with exit code %d ---\n", process.ExitCode)
				process.logs.WriteString(failureMsg)
				pm.writeToLogWriters(process, []byte(failureMsg), "")
			}

			// Check if we should restart
			return process.RestartOnFailure && (process.MaxRestarts == 0 || process.CurrentRestarts < process.MaxRestarts)
		} else {
			// For stopped/killed processes, still set exit code
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.ExitCode = exitErr.ExitCode()
			} else {
				process.ExitCode = 1
			}
		}
	} else {
		process.Status = StatusCompleted
		process.ExitCode = 0
	}

	return false
}

// handleRestart manages the restart logic with delay and logging
func (pm *ProcessManager) handleRestart(process *ProcessInfo, callback func(process *ProcessInfo)) {
	// Add delay before restarting
	time.Sleep(10 * time.Millisecond)

	// Increment restart counter
	process.CurrentRestarts++

	// Log restart attempt
	var restartMsg string
	if process.MaxRestarts == 0 {
		restartMsg = fmt.Sprintf("--- Process restarting (attempt %d/unlimited) ---\n", process.CurrentRestarts)
	} else {
		restartMsg = fmt.Sprintf("--- Process restarting (attempt %d/%d) ---\n", process.CurrentRestarts, process.MaxRestarts)
	}

	process.logs.WriteString(restartMsg)
	pm.writeToLogWriters(process, []byte(restartMsg), "")

	// Restart the process
	go func() {
		if err := pm.startProcess(process, callback); err != nil {
			errorMsg := fmt.Sprintf("--- Failed to restart process: %v ---\n", err)
			process.logs.WriteString(errorMsg)
			process.Status = StatusFailed
			callback(process)
		}
	}()
}

// cleanupProcess cleans up resources for processes that won't restart
func (pm *ProcessManager) cleanupProcess(process *ProcessInfo) {
	process.logLock.Lock()
	process.logWriters = nil
	process.logLock.Unlock()
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
				// Get process from OS
				osProcess, err := os.FindProcess(pidInt)
				if err == nil {
					// Create a new exec.Cmd with the process
					cmd := &exec.Cmd{
						Process: osProcess,
					}
					process.Cmd = cmd
				}
			}
		}
		if process.logs != nil {
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

	if process.Cmd == nil || process.Cmd.Process == nil {
		return fmt.Errorf("process with Identifier %s has no OS process", identifier)
	}

	// Notify about termination
	terminationMsg := []byte("\n[Process is being gracefully terminated]\n")
	pm.writeToLogWriters(process, terminationMsg, "")
	process.stdout.Write(terminationMsg)

	// Try to gracefully terminate the entire process group first
	pid := process.Cmd.Process.Pid

	// Send SIGTERM to the process group (negative PID targets the process group)
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if err != nil {
		// If process group termination fails, fall back to terminating just the process
		err = process.Cmd.Process.Signal(syscall.SIGTERM)
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

	if process.Cmd == nil || process.Cmd.Process == nil {
		return fmt.Errorf("process with Identifier %s has no OS process", identifier)
	}

	// Notify about forceful termination
	terminationMsg := []byte("\n[Process is being forcefully killed]\n")
	pm.writeToLogWriters(process, terminationMsg, "")
	process.stdout.Write(terminationMsg)

	// Kill the entire process group to ensure all child processes are terminated
	pid := process.Cmd.Process.Pid

	// First try to kill the process group (negative PID kills the process group)
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if err != nil {
		// If process group kill fails, fall back to killing just the process
		err = process.Cmd.Process.Kill()
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

	// Write current complete logs first (which includes stdout, stderr, and restart messages in chronological order)
	w.Write([]byte(process.logs.String()))

	// Attach writer for future output
	process.logLock.Lock()
	process.logWriters = append(process.logWriters, w)
	process.logLock.Unlock()

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

	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate random string
	for i := 0; i < length; i++ {
		randomIndex := rand.Intn(len(charset))
		randomName.WriteByte(charset[randomIndex])
	}

	return randomName.String()
}
