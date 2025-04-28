package process

import (
	"database/sql"
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

	_ "github.com/mattn/go-sqlite3"
)

// ProcessManager manages the running processes
type ProcessManager struct {
	db *sql.DB
}

// ProcessInfo stores information about a running process
type ProcessInfo struct {
	PID         string
	Name        string
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
	db, err := sql.Open("sqlite3", "processes.db")
	if err != nil {
		panic("failed to open sqlite database: " + err.Error())
	}
	// Create table if not exists
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS processes (
		pid TEXT PRIMARY KEY,
		name TEXT,
		command TEXT,
		started_at DATETIME,
		completed_at DATETIME,
		exit_code INTEGER,
		status TEXT,
		working_dir TEXT,
		stdout TEXT,
		stderr TEXT
	)`)
	if err != nil {
		panic("failed to create processes table: " + err.Error())
	}
	return &ProcessManager{db: db}
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
		return "", err
	}
	process.PID = fmt.Sprintf("%d", cmd.Process.Pid)

	// Insert process info into SQLite
	_, err = pm.db.Exec(`INSERT INTO processes (pid, name, command, started_at, completed_at, exit_code, status, working_dir, stdout, stderr) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		process.PID,
		process.Name,
		process.Command,
		process.StartedAt,
		nil, // completed_at is nil at start
		0,   // exit_code is 0 at start
		process.Status,
		process.WorkingDir,
		"", // stdout empty at start
		"", // stderr empty at start
	)
	if err != nil {
		return "", fmt.Errorf("failed to insert process into db: %w", err)
	}

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

		process.CompletedAt = &now

		// Determine exit status and create appropriate message
		if err != nil {
			process.Status = "failed"
			if exitErr, ok := err.(*exec.ExitError); ok {
				process.ExitCode = exitErr.ExitCode()
			} else {
				process.ExitCode = 1
			}
		} else {
			process.Status = "completed"
			process.ExitCode = 0
		}

		// Update process info in SQLite
		_, _ = pm.db.Exec(`UPDATE processes SET completed_at = ?, exit_code = ?, status = ?, stdout = ?, stderr = ? WHERE pid = ?`,
			now,
			process.ExitCode,
			process.Status,
			process.stdout.String(),
			process.stderr.String(),
			process.PID,
		)

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

// GetProcessByIdentifier returns a process by either PID or name
func (pm *ProcessManager) GetProcessByIdentifier(identifier string) (*ProcessInfo, bool) {
	// Try to convert identifier to int (PID)
	var process *ProcessInfo
	var exists bool
	if _, err := strconv.Atoi(identifier); err == nil {
		// If conversion successful, try to get process by PID
		process, exists = pm.GetProcessByPid(identifier)
	} else {
		process, exists = pm.GetProcessByName(identifier)
	}
	// If the process is running, try to get additional information from the OS
	if process.Status == "running" {
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

	return process, exists
}

// GetProcess returns information about a specific process
func (pm *ProcessManager) GetProcessByPid(pid string) (*ProcessInfo, bool) {
	row := pm.db.QueryRow(`SELECT * FROM processes WHERE pid = ?`, pid)
	process, err := pm.formatRowToProcess(row)
	if err != nil {
		return nil, false
	}
	return process, true
}

func (pm *ProcessManager) GetProcessByName(name string) (*ProcessInfo, bool) {
	if name == "" {
		return nil, false
	}

	row := pm.db.QueryRow(`SELECT * FROM processes WHERE name = ? LIMIT 1`, name)
	process, err := pm.formatRowToProcess(row)
	if err != nil {
		return nil, false
	}
	return process, true
}

// ListProcesses returns information about all processes
func (pm *ProcessManager) ListProcesses() []*ProcessInfo {
	rows, err := pm.db.Query(`SELECT * FROM processes`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	processes := make([]*ProcessInfo, 0)
	for rows.Next() {
		process, err := pm.formatRowsToProcess(rows)
		if err != nil {
			continue
		}
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

	if process.Status != "running" {
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

	return process.Cmd.Process.Signal(syscall.SIGTERM)
}

// KillProcess forcefully kills a process
func (pm *ProcessManager) KillProcess(identifier string) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	if process.Status != "running" {
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
		return fmt.Errorf("failed to kill process with Identifier %s: %w", identifier, err)
	}

	// Remove the process from the SQLite database
	_, err = pm.db.Exec(
		"DELETE FROM processes WHERE pid = ?",
		process.PID,
	)
	if err != nil {
		return fmt.Errorf("failed to remove process with Identifier %s from database: %w", identifier, err)
	}
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

func (pm *ProcessManager) formatRowToProcess(row *sql.Row) (*ProcessInfo, error) {
	var p ProcessInfo
	var startedAt, completedAt sql.NullTime
	var completedAtPtr *time.Time
	var stdout, stderr string
	if err := row.Scan(&p.PID, &p.Name, &p.Command, &startedAt, &completedAt, &p.ExitCode, &p.Status, &p.WorkingDir, &stdout, &stderr); err != nil {
		return nil, err
	}
	if startedAt.Valid {
		p.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		completedAtPtr = &completedAt.Time
	}
	p.CompletedAt = completedAtPtr
	p.stdout = &strings.Builder{}
	p.stdout.WriteString(stdout)
	p.stderr = &strings.Builder{}
	p.stderr.WriteString(stderr)
	return &p, nil
}

func (pm *ProcessManager) formatRowsToProcess(rows *sql.Rows) (*ProcessInfo, error) {
	var p ProcessInfo
	var startedAt, completedAt sql.NullTime
	var completedAtPtr *time.Time
	var stdout, stderr string
	if err := rows.Scan(&p.PID, &p.Name, &p.Command, &startedAt, &completedAt, &p.ExitCode, &p.Status, &p.WorkingDir, &stdout, &stderr); err != nil {
		return nil, err
	}
	if startedAt.Valid {
		p.StartedAt = startedAt.Time
	}
	if completedAt.Valid {
		completedAtPtr = &completedAt.Time
	}
	p.CompletedAt = completedAtPtr
	p.stdout = &strings.Builder{}
	p.stdout.WriteString(stdout)
	p.stderr = &strings.Builder{}
	p.stderr.WriteString(stderr)
	return &p, nil
}
