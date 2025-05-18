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

// ProcessInfo stores information about a running process
type ProcessInfo struct {
	PID         string     `json:"pid"`
	Name        string     `json:"name"`
	Command     string     `json:"command"`
	Cmd         *exec.Cmd  `json:"cmd"`
	StartedAt   time.Time  `json:"startedAt"`
	CompletedAt *time.Time `json:"completedAt"`
	ExitCode    int        `json:"exitCode"`
	Status      string     `json:"status"`
	WorkingDir  string     `json:"workingDir"`
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

func (pm *ProcessManager) StartProcess(command string, workingDir string, callback func(process *ProcessInfo)) (string, error) {
	name := GenerateRandomName(8)
	return pm.StartProcessWithName(command, workingDir, name, callback)
}

func (pm *ProcessManager) StartProcessWithName(command string, workingDir string, name string, callback func(process *ProcessInfo)) (string, error) {
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
			return "", fmt.Errorf("empty command")
		}
		cmd = exec.Command(args[0], args[1:]...)
	}

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Set up stdout and stderr pipes
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Set up stdout and stderr capture
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}

	process := &ProcessInfo{
		Name:        name,
		Command:     command,
		Cmd:         cmd,
		StartedAt:   time.Now(),
		CompletedAt: nil,
		Status:      StatusRunning,
		WorkingDir:  workingDir,
		stdout:      stdout,
		stderr:      stderr,
		stdoutPipe:  stdoutPipe,
		stderrPipe:  stderrPipe,
		logWriters:  make([]io.Writer, 0),
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		return "", err
	}
	process.PID = fmt.Sprintf("%d", cmd.Process.Pid)

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
				process.stdout.Write(data)

				// Send to any attached log writers, prefix with stdout:
				process.logLock.RLock()
				for _, w := range process.logWriters {
					fullMsg := append([]byte("stdout:"), data...)
					w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
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

				// Send to any attached log writers, prefix with stderr:
				process.logLock.RLock()
				for _, w := range process.logWriters {
					fullMsg := append([]byte("stderr:"), data...)
					w.Write(fullMsg)
					if f, ok := w.(interface{ Flush() }); ok {
						f.Flush()
					}
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

		// Clean up resources
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
		return latestProcess, true
	}

	return nil, false
}

// GetProcessByPid returns information about a specific process
func (pm *ProcessManager) GetProcessByPid(pid string) (*ProcessInfo, bool) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	process, exists := pm.processes[pid]
	return process, exists
}

func (pm *ProcessManager) GetProcessByName(name string) (*ProcessInfo, bool) {
	if name == "" {
		return nil, false
	}

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// Find the most recent process with the given name
	var latestProcess *ProcessInfo
	for _, process := range pm.processes {
		if process.Name == name {
			if latestProcess == nil || process.StartedAt.After(latestProcess.StartedAt) {
				latestProcess = process
			}
		}
	}

	if latestProcess == nil {
		return nil, false
	}
	return latestProcess, true
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

	// Notify log writers about termination
	process.logLock.RLock()
	terminationMsg := []byte("\n[Process is being gracefully terminated]\n")
	for _, w := range process.logWriters {
		w.Write(terminationMsg)
	}
	process.logLock.RUnlock()

	// Add termination message to output buffers
	process.stdout.Write(terminationMsg)
	err := process.Cmd.Process.Signal(syscall.SIGTERM)
	if err != nil {
		if err.Error() != "os: process already finished" {
			return fmt.Errorf("failed to send SIGTERM to process with Identifier %s: %w", identifier, err)
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

	if process.Status != StatusRunning {
		return fmt.Errorf("process with Identifier %s is not running", identifier)
	}

	if process.Cmd == nil || process.Cmd.Process == nil {
		return fmt.Errorf("process with Identifier %s has no OS process", identifier)
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

	err := process.Cmd.Process.Kill()
	if err != nil {
		if err.Error() != "os: process already finished" {
			return fmt.Errorf("failed to kill process with Identifier %s: %w", identifier, err)
		}
	}

	// Remove the process from memory
	process.Status = StatusKilled
	return nil
}

// GetProcessOutput returns the stdout and stderr output of a process
func (pm *ProcessManager) GetProcessOutput(identifier string) (string, string, error) {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return "", "", fmt.Errorf("process with PID %s not found", identifier)
	}

	return process.stdout.String(), process.stderr.String(), nil
}

func (pm *ProcessManager) StreamProcessOutput(identifier string, w io.Writer) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
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
