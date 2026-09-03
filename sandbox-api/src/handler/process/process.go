package process

import (
	"bufio"
	"bytes"
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
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
	"github.com/blaxel-ai/sandbox-api/src/lib/oom"
	"github.com/sirupsen/logrus"
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

// writeToLogWriter sends data that begins a line, tagging it for text writers.
// Callers with a partial line must use writeChunkToLogWriter instead.
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

// writeChunkToLogWriter sends one read of process output. raw is the untagged
// bytes, which JSON writers carry with the stream in the event type; prefixed
// is the same bytes already tagged at their line starts, for text writers.
//
// A read is a fixed-size window, not a line, so the tag cannot be applied here:
// doing so put it in the middle of any line longer than the buffer.
func writeChunkToLogWriter(w io.Writer, eventType string, raw, prefixed []byte) {
	// IsJSONStreamWriter, not just the type assertion: a pendingWriter accepts
	// WriteEvent whatever its target is, and queueing raw bytes as an event
	// would have it re-tag them per chunk on release, undoing the work above.
	if jw, ok := w.(JSONStreamWriter); ok && jw.IsJSONStreamWriter() {
		jw.WriteEvent(eventType, string(raw))
	} else {
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
	Env              map[string]string       `json:"-"` // Custom env vars provided at start, reused (re-merged with os.Environ()) on restart
	Logs             *string                 `json:"logs"`
	Stdout           *string                 `json:"stdout"`
	Stderr           *string                 `json:"stderr"`
	RestartOnFailure bool                    `json:"restartOnFailure"`
	MaxRestarts      int                     `json:"maxRestarts"`
	RestartCount     int                     `json:"restartCount"`
	KeepAlive        bool                    `json:"keepAlive"`
	Stdin            bool                    `json:"stdin"` // Whether the process was started with a writable stdin pipe
	Timeout          int                     `json:"-"`     // Internal: timeout in seconds for keepAlive processes
	LogFile          string                  `json:"-"`     // Path to combined log file
	StdoutFile       string                  `json:"-"`     // Path to stdout log file
	StderrFile       string                  `json:"-"`     // Path to stderr log file
	Done             chan struct{}
	TailDone         chan struct{} // Closed when tailLogFiles finishes its final reads
	// Finished is closed once the process is over for good, with no restart to
	// come. Done is per-run: a restart closes it and installs a fresh one, so a
	// consumer waiting on Done sees "finished" every time the process merely
	// bounces. Anything user-facing that means "this process is over" waits on
	// Finished instead.
	Finished   chan struct{}
	finishOnce sync.Once
	stdout     *logBuffer
	stderr     *logBuffer
	logs       *logBuffer
	logWriters []io.Writer
	// Whether each stream is part-way through a line. A read is a fixed-size
	// window, not a line, so a long line spans several reads and must be tagged
	// only at its start. Phrased as "mid-line" so the zero value means "at a
	// line start", which is what a fresh process is.
	stdoutMidLine bool
	stderrMidLine bool
	// Reused across reads so tagging a chunk costs no allocation.
	prefixBuf       []byte
	logLock         sync.RWMutex
	stopTimeout     chan struct{} // Channel to signal timeout goroutine to stop
	stopTimeoutOnce sync.Once     // Protects stopTimeout channel from double-close
	stdin           stdinPipe     // Write end of stdin when Stdin is true; nil once closed or after a sandbox-api restart
}

// ProcessLogDir is the directory where process logs are stored
// Can be configured via SANDBOX_LOG_DIR environment variable
var ProcessLogDir = "/var/log/sandbox-api"

// disableProcessLogging controls whether process output is exported to
// structured telemetry logs. Set SANDBOX_DISABLE_PROCESS_LOGGING=true to
// suppress logrus output while keeping file-based logs and streaming.
var disableProcessLogging = false

func init() {
	if dir := os.Getenv("SANDBOX_LOG_DIR"); dir != "" {
		ProcessLogDir = dir
	}
	if v := os.Getenv("SANDBOX_DISABLE_PROCESS_LOGGING"); v == "true" || v == "1" {
		disableProcessLogging = true
	}
}

// shouldRestart reports whether a failed process is eligible for another
// restart attempt. A negative MaxRestarts means unlimited restarts.
// markFinished closes Finished exactly once, so every terminal path can call it.
func (p *ProcessInfo) markFinished() {
	p.finishOnce.Do(func() { close(p.Finished) })
}

func shouldRestart(p *ProcessInfo) bool {
	return p.Status == StatusFailed && p.RestartOnFailure &&
		(p.MaxRestarts < 0 || p.RestartCount < p.MaxRestarts)
}

// restartLimitLabel renders the max-restarts part of a log message,
// showing "unlimited" when MaxRestarts is negative.
func restartLimitLabel(maxRestarts int) string {
	if maxRestarts < 0 {
		return "unlimited"
	}
	return strconv.Itoa(maxRestarts)
}

// buildProcessEnv merges the current system environment with the caller's
// custom environment variables, letting custom vars override system ones.
// The system environment is read fresh from os.Environ() so only the custom
// vars need to be stored/persisted for a later restart.
func buildProcessEnv(custom map[string]string) []string {
	systemEnv := os.Environ()

	// Track which keys the custom env overrides.
	overrides := make(map[string]bool, len(custom))
	for k := range custom {
		overrides[k] = true
	}

	finalEnv := make([]string, 0, len(systemEnv)+len(custom))

	// Keep system vars that are not being overridden.
	for _, envVar := range systemEnv {
		if idx := strings.IndexByte(envVar, '='); idx > 0 {
			if !overrides[envVar[:idx]] {
				finalEnv = append(finalEnv, envVar)
			}
		}
	}

	// Custom vars take priority.
	for k, v := range custom {
		finalEnv = append(finalEnv, k+"="+v)
	}

	return finalEnv
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
		go processManager.sweepLogFiles()
	})
	return processManager
}

// logCapInterval is how often the log files of processes nobody is tailing are
// brought back inside the shared budget.
const logCapInterval = 5 * time.Second

func (pm *ProcessManager) sweepLogFiles() {
	ticker := time.NewTicker(logCapInterval)
	defer ticker.Stop()
	for range ticker.C {
		pm.CapLogFiles()
	}
}

func (pm *ProcessManager) StartProcess(command string, workingDir string, env map[string]string, restartOnFailure bool, maxRestarts int, keepAlive bool, timeout int, stdin bool, callback func(process *ProcessInfo)) (string, error) {
	name := GenerateRandomName(8)
	return pm.StartProcessWithName(command, workingDir, name, env, restartOnFailure, maxRestarts, keepAlive, timeout, stdin, callback)
}

func (pm *ProcessManager) StartProcessWithName(command string, workingDir string, name string, env map[string]string, restartOnFailure bool, maxRestarts int, keepAlive bool, timeout int, stdin bool, callback func(process *ProcessInfo)) (string, error) {
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
		Setpgid:    true,
		Credential: identity.Get().Credential(),
	}

	cmd.Env = identity.Get().DecorateEnv(buildProcessEnv(env))

	// Ensure log directory exists
	if err := ensureLogDir(); err != nil {
		return "", fmt.Errorf("failed to create log directory: %w", err)
	}

	// Set up in-memory buffers
	stdout := newLogBuffer()
	stderr := newLogBuffer()
	logs := newLogBuffer()

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
		Env:              env,
		RestartOnFailure: restartOnFailure,
		MaxRestarts:      maxRestarts,
		RestartCount:     0,
		KeepAlive:        keepAlive,
		Stdin:            stdin,
		Timeout:          timeout,
		LogFile:          combinedPath,
		StdoutFile:       stdoutPath,
		StderrFile:       stderrPath,
		Done:             make(chan struct{}),
		Finished:         make(chan struct{}),
		TailDone:         make(chan struct{}),
		stdout:           stdout,
		stderr:           stderr,
		logs:             logs,
		logWriters:       make([]io.Writer, 0),
		stopTimeout:      make(chan struct{}),
	}

	// Redirect stdout/stderr directly to files
	// This is crucial - child writes to files, not pipes
	// So child survives sandbox-api restart without blocking
	cmd.Stdout = stdoutFile
	cmd.Stderr = stderrFile

	if err := attachStdin(cmd, process); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		os.Remove(stdoutPath)
		os.Remove(stderrPath)
		return "", err
	}

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
	oom.PreferAsVictim(process.ProcessPid)

	// Close the write handles in parent - child has its own FDs
	stdoutFile.Close()
	stderrFile.Close()

	// If keepAlive is enabled, disable scale-to-zero and log the event
	if keepAlive {
		keepAliveLog := logrus.WithFields(logrus.Fields{
			"process_pid":  process.PID,
			"process_name": name,
			"command":      command,
		})
		if err := blaxel.ScaleDisable(); err != nil {
			keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to disable scale-to-zero")
		}
		if timeout > 0 {
			keepAliveLog.WithField("timeout", timeout).Info("[KeepAlive] Started process with timeout")
		} else {
			keepAliveLog.Info("[KeepAlive] Started process with infinite timeout")
		}
	}

	// Store process in memory
	pm.mu.Lock()
	pm.processes[process.PID] = process
	pm.mu.Unlock()

	// Start file tailer for real-time log streaming
	go pm.tailLogFiles(process)
	// If keepAlive is enabled with a timeout > 0, start a goroutine to kill the process after timeout
	// Timeout of 0 means infinite (no auto-kill)
	if keepAlive && timeout > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeout) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				logrus.WithFields(logrus.Fields{
					"process_pid":  process.PID,
					"process_name": process.Name,
					"timeout":      timeout,
				}).Info("[KeepAlive] Timeout expired, killing process")
				_ = pm.KillProcess(process.PID)
			case <-process.stopTimeout:
				// Process completed before timeout
			}
		}()
	}

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

		// Signal the timeout goroutine to stop (if any)
		if process.stopTimeout != nil {
			process.stopTimeoutOnce.Do(func() { close(process.stopTimeout) })
		}

		// Check if we should restart on failure
		if shouldRestart(process) {
			// Log the failure and restart attempt
			restartMsg := fmt.Sprintf("\n[Process failed with exit code %d. Attempting restart %d/%s...]\n",
				process.ExitCode, process.RestartCount+1, restartLimitLabel(process.MaxRestarts))

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

			// Let the current tailLogFiles goroutine finish before restarting
			close(process.Done)
			<-process.TailDone

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

				// If keepAlive was enabled, re-enable scale-to-zero now that process truly ended
				pm.mu.RLock()
				stillKeepAlive := process.KeepAlive
				pm.mu.RUnlock()
				if stillKeepAlive {
					keepAliveLog := logrus.WithFields(logrus.Fields{
						"process_pid":  process.PID,
						"process_name": process.Name,
						"status":       process.Status,
						"exit_code":    process.ExitCode,
					})
					keepAliveLog.Info("[KeepAlive] Stopped process - restart failed")
					if err := blaxel.ScaleEnable(); err != nil {
						keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero")
					}
				}

				// Clean up resources
				// Signal tailLogFiles to do final reads, then wait for it to finish
				close(process.Done)
				<-process.TailDone

				process.logLock.Lock()
				process.logWriters = nil
				process.logLock.Unlock()

				process.markFinished()
				callback(process)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// If keepAlive was enabled, re-enable scale-to-zero now that process ended
			pm.mu.RLock()
			stillKeepAlive := process.KeepAlive
			pm.mu.RUnlock()
			if stillKeepAlive {
				keepAliveLog := logrus.WithFields(logrus.Fields{
					"process_pid":  process.PID,
					"process_name": process.Name,
					"status":       process.Status,
					"exit_code":    process.ExitCode,
				})
				keepAliveLog.Info("[KeepAlive] Stopped process")
				if err := blaxel.ScaleEnable(); err != nil {
					keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero")
				}
			}

			// Clean up resources
			// Signal tailLogFiles to do final reads, then wait for it to finish
			close(process.Done)
			<-process.TailDone

			process.logLock.Lock()
			process.logWriters = nil
			process.logLock.Unlock()

			process.markFinished()
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

	// The workload writes to its log files itself, so the tailer is what keeps
	// them from filling the guest's tmpfs.
	capCheck := time.NewTicker(5 * time.Second)
	defer capCheck.Stop()
	capFiles := func() {
		// Every process' log files share one budget, so what this one may keep
		// depends on how many others are keeping output too.
		processes := pm.countProcesses()
		capLogFile(proc.StdoutFile, processes)
		capLogFile(proc.StderrFile, processes)
		capLogFile(proc.LogFile, processes)
	}

	for {
		select {
		case <-capCheck.C:
			capFiles()
		case <-proc.Done:
			// Drain all remaining data from both files before returning.
			// Loop until both files return 0 bytes to handle data larger than the buffer.
			for {
				n1 := pm.drainStream(stdoutFile, stdoutBuf, proc, "stdout", combinedFile)
				n2 := pm.drainStream(stderrFile, stderrBuf, proc, "stderr", combinedFile)
				if n1 == 0 && n2 == 0 {
					break
				}
			}
			capFiles()
			close(proc.TailDone)
			return
		default:
			pm.drainStream(stdoutFile, stdoutBuf, proc, "stdout", combinedFile)
			pm.drainStream(stderrFile, stderrBuf, proc, "stderr", combinedFile)
			time.Sleep(50 * time.Millisecond)
		}
	}
}

// drainStream reads one stream until it sits at a line boundary or has no more
// data ready. The two streams are read in turns, one buffer at a time, so
// without this a stderr line landed inside any stdout line longer than the
// buffer: the text stream carried "stdout:<4KB>stderr:...\n<rest>" and no
// line-oriented consumer could parse either half. A child that itself pauses
// mid-line to write stderr can still interleave; that is its own doing.
// Returns the number of bytes read.
func (pm *ProcessManager) drainStream(file *os.File, buf []byte, proc *ProcessInfo, streamType string, combinedFile *os.File) int {
	total := 0
	for {
		n := pm.readAndBroadcast(file, buf, proc, streamType, combinedFile)
		total += n
		if n == 0 || !proc.midLine(streamType) {
			return total
		}
	}
}

// midLine reports whether the stream's last read ended part-way through a line.
func (p *ProcessInfo) midLine(streamType string) bool {
	p.logLock.RLock()
	defer p.logLock.RUnlock()
	if streamType == "stderr" {
		return p.stderrMidLine
	}
	return p.stdoutMidLine
}

// readAndBroadcast reads from a file and broadcasts to log writers.
// Returns the number of bytes read.
func (pm *ProcessManager) readAndBroadcast(file *os.File, buf []byte, proc *ProcessInfo, streamType string, combinedFile *os.File) int {
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

		// Write the combined log file and the telemetry entries in a single pass
		// over the bytes just read. This runs on every chunk of every process'
		// output, so it stays on []byte: converting the chunk to a string and
		// splitting it into a slice of lines used to copy it twice per chunk, and
		// a workload that writes fast enough grew the heap doing so until the
		// kernel OOM-killed the API for it.
		var logEntry *logrus.Entry
		if !disableProcessLogging {
			logEntry = logrus.WithFields(logrus.Fields{
				"source":       "process",
				"process-name": proc.Name,
				"process-pid":  proc.PID,
				"stream":       streamType,
			})
		}
		// Tag only real line starts. Tagging the chunk instead put the tag in
		// the middle of any line longer than the read buffer, which corrupted
		// it for every consumer: a >4KB JSON line came out with "stdout:"
		// spliced in at byte 4096 and no longer parsed. atLineStart carries
		// that across reads so a continuation resumes untagged.
		atLineStart := !proc.stdoutMidLine
		if streamType == "stderr" {
			atLineStart = !proc.stderrMidLine
		}
		prefix := []byte(streamType + ":")
		proc.prefixBuf = proc.prefixBuf[:0]
		for rest := data; len(rest) > 0; {
			line := rest
			if end := bytes.IndexByte(rest, '\n'); end >= 0 {
				line, rest = rest[:end+1], rest[end+1:]
			} else {
				rest = nil
			}

			if atLineStart {
				proc.prefixBuf = append(proc.prefixBuf, prefix...)
			}
			proc.prefixBuf = append(proc.prefixBuf, line...)
			atLineStart = line[len(line)-1] == '\n'

			// Structured attributes let the telemetry collector tell process
			// logs from access logs.
			if logEntry != nil {
				logProcessLine(logEntry, streamType, line)
			}
		}
		if streamType == "stderr" {
			proc.stderrMidLine = !atLineStart
		} else {
			proc.stdoutMidLine = !atLineStart
		}

		// Preserves the interleaved order of the two streams.
		if combinedFile != nil {
			_, _ = combinedFile.Write(proc.prefixBuf)
		}
		// Send to log writers for streaming
		for _, w := range proc.logWriters {
			writeChunkToLogWriter(w, streamType, data, proc.prefixBuf)
		}
		proc.logLock.Unlock()
	}
	if err != nil && err != io.EOF {
		// Real error, but we'll keep trying
	}
	return n
}

// maxLoggedLineBytes caps what one line of a process' output contributes to
// telemetry. Output with no newlines in it is one line however long it is, and
// logging it whole would mean holding the whole of it - as a string, then again
// JSON-encoded - in the API's heap.
const maxLoggedLineBytes = 8 * 1024

// logProcessLine exports one line of a process' output for telemetry.
func logProcessLine(entry *logrus.Entry, streamType string, line []byte) {
	line = bytes.TrimSuffix(line, []byte("\n"))
	if len(line) == 0 {
		return
	}
	if len(line) > maxLoggedLineBytes {
		line = line[:maxLoggedLineBytes]
	}
	if streamType == "stderr" {
		entry.Error(string(line))
	} else {
		entry.Info(string(line))
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

	// Swap in the new run's channels before anything that can fail. The caller
	// closed the previous Done before calling us, and closes Done again if we
	// return an error; leaving the old channel in place until after the
	// working-dir and log-file checks made that second close panic on an
	// already-closed channel.
	oldProcess.Done = make(chan struct{})
	oldProcess.TailDone = make(chan struct{})

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
		Setpgid:    true,
		Credential: identity.Get().Credential(),
	}

	// Re-merge the custom env vars provided at the original start with the
	// current system environment. Using os.Environ() alone here would drop any
	// custom env vars the caller passed when first starting the process.
	cmd.Env = identity.Get().DecorateEnv(buildProcessEnv(oldProcess.Env))

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
	oldProcess.stopTimeout = make(chan struct{})
	oldProcess.stopTimeoutOnce = sync.Once{}
	// Fresh run, fresh line state: a run that died mid-line must not leave the
	// next one's first line untagged. The Done/TailDone swap happens earlier,
	// before anything that can fail.
	oldProcess.logLock.Lock()
	oldProcess.stdoutMidLine = false
	oldProcess.stderrMidLine = false
	oldProcess.logLock.Unlock()

	if err := attachStdin(cmd, oldProcess); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		return "", err
	}

	// Start the process
	if err := cmd.Start(); err != nil {
		stdoutFile.Close()
		stderrFile.Close()
		return "", err
	}

	// Update only the OS process PID for kill/stop operations
	// Keep the user-facing PID (oldProcess.PID) unchanged for transparency
	oldProcess.ProcessPid = cmd.Process.Pid
	oom.PreferAsVictim(oldProcess.ProcessPid)

	// Close write handles in parent - child has its own FDs
	stdoutFile.Close()
	stderrFile.Close()

	// Update the process in memory (same map key, just updating the entry)
	pm.mu.Lock()
	pm.processes[oldProcess.PID] = oldProcess
	pm.mu.Unlock()

	// Start file tailer for real-time log streaming
	go pm.tailLogFiles(oldProcess)
	// If keepAlive is enabled, start timeout goroutine for the restarted process
	pm.mu.RLock()
	keepAlive := oldProcess.KeepAlive
	timeout := oldProcess.Timeout
	pm.mu.RUnlock()
	if keepAlive && timeout > 0 {
		go func() {
			timer := time.NewTimer(time.Duration(timeout) * time.Second)
			defer timer.Stop()
			select {
			case <-timer.C:
				logrus.WithFields(logrus.Fields{
					"process_pid":  oldProcess.PID,
					"process_name": oldProcess.Name,
					"timeout":      timeout,
				}).Info("[KeepAlive] Timeout expired, killing process")
				_ = pm.KillProcess(oldProcess.PID)
			case <-oldProcess.stopTimeout:
				// Process completed before timeout
			}
		}()
	}

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

		// Signal the timeout goroutine to stop (if any)
		if oldProcess.stopTimeout != nil {
			oldProcess.stopTimeoutOnce.Do(func() { close(oldProcess.stopTimeout) })
		}

		// Check if we should restart again on failure
		if shouldRestart(oldProcess) {
			// Log the failure and restart attempt
			restartMsg := fmt.Sprintf("\n[Process failed with exit code %d. Attempting restart %d/%s...]\n",
				oldProcess.ExitCode, oldProcess.RestartCount+1, restartLimitLabel(oldProcess.MaxRestarts))

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

			// Let the current tailLogFiles goroutine finish before restarting
			close(oldProcess.Done)
			<-oldProcess.TailDone

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

				// If keepAlive was enabled, re-enable scale-to-zero now that process truly ended
				pm.mu.RLock()
				stillKeepAlive := oldProcess.KeepAlive
				pm.mu.RUnlock()
				if stillKeepAlive {
					keepAliveLog := logrus.WithFields(logrus.Fields{
						"process_pid":  oldProcess.PID,
						"process_name": oldProcess.Name,
						"status":       oldProcess.Status,
						"exit_code":    oldProcess.ExitCode,
					})
					keepAliveLog.Info("[KeepAlive] Stopped process - restart failed")
					if err := blaxel.ScaleEnable(); err != nil {
						keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero")
					}
				}

				// Clean up resources
				// Signal tailLogFiles to do final reads, then wait for it to finish
				close(oldProcess.Done)
				<-oldProcess.TailDone

				oldProcess.logLock.Lock()
				oldProcess.logWriters = nil
				oldProcess.logLock.Unlock()

				oldProcess.markFinished()
				callback(oldProcess)
			}
			// If restart succeeds, the callback will be called when that process completes
		} else {
			// If keepAlive was enabled, re-enable scale-to-zero now that process ended
			pm.mu.RLock()
			stillKeepAlive := oldProcess.KeepAlive
			pm.mu.RUnlock()
			if stillKeepAlive {
				keepAliveLog := logrus.WithFields(logrus.Fields{
					"process_pid":  oldProcess.PID,
					"process_name": oldProcess.Name,
					"status":       oldProcess.Status,
					"exit_code":    oldProcess.ExitCode,
				})
				keepAliveLog.Info("[KeepAlive] Stopped process")
				if err := blaxel.ScaleEnable(); err != nil {
					keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero")
				}
			}

			// Clean up resources
			// Signal tailLogFiles to do final reads, then wait for it to finish
			close(oldProcess.Done)
			<-oldProcess.TailDone

			oldProcess.logLock.Lock()
			oldProcess.logWriters = nil
			oldProcess.logLock.Unlock()

			oldProcess.markFinished()
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

// countProcesses is how many processes the manager knows about, running or not:
// they all hold log files, so they all share the log budget.
func (pm *ProcessManager) countProcesses() int {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	return len(pm.processes)
}

// CapLogFiles keeps every process' log files inside the shared budget. A
// process' own tailer stops capping when the process exits, but its output stays
// on the tmpfs and still costs the guest memory, so something has to keep
// looking at the ones nobody is tailing any more.
func (pm *ProcessManager) CapLogFiles() {
	processes := pm.ListProcesses()
	for _, proc := range processes {
		capLogFile(proc.StdoutFile, len(processes))
		capLogFile(proc.StderrFile, len(processes))
		capLogFile(proc.LogFile, len(processes))
	}
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

	// Clear KeepAlive BEFORE the signal under lock: the completion goroutine
	// reads KeepAlive after cmd.Wait() returns, so we must ensure it sees false
	// to prevent a double ScaleEnable() call.
	pm.mu.Lock()
	wasKeepAlive := process.KeepAlive
	process.KeepAlive = false
	pm.mu.Unlock()

	// Try to gracefully terminate the entire process group first
	pid := process.ProcessPid

	// Send SIGTERM to the process group (negative PID targets the process group)
	err := syscall.Kill(-pid, syscall.SIGTERM)
	if err != nil {
		// If process group termination fails, fall back to terminating just the process
		err = syscall.Kill(pid, syscall.SIGTERM)
		if err != nil {
			if err.Error() != "os: process already finished" {
				pm.mu.Lock()
				process.KeepAlive = wasKeepAlive
				pm.mu.Unlock()
				return fmt.Errorf("failed to send SIGTERM to process with Identifier %s: %w", identifier, err)
			}
		}
	}

	process.Status = StatusStopped

	if wasKeepAlive {
		if process.stopTimeout != nil {
			process.stopTimeoutOnce.Do(func() { close(process.stopTimeout) })
		}

		keepAliveLog := logrus.WithFields(logrus.Fields{
			"process_pid":  process.PID,
			"process_name": process.Name,
			"status":       "stopped",
		})
		if err := blaxel.ScaleEnable(); err != nil {
			keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero after stopping process")
		}
		keepAliveLog.Info("[KeepAlive] Stopped process")
	}

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

	// Clear KeepAlive BEFORE the kill under lock: the completion goroutine
	// reads KeepAlive under the same lock after cmd.Wait() returns, so it will
	// observe KeepAlive==false. This prevents a double ScaleEnable() call that
	// would make the counter go negative and permanently disable auto-hibernation.
	pm.mu.Lock()
	wasKeepAlive := process.KeepAlive
	process.KeepAlive = false
	pm.mu.Unlock()

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
				pm.mu.Lock()
				process.KeepAlive = wasKeepAlive
				pm.mu.Unlock()
				return fmt.Errorf("failed to kill process with Identifier %s: %w", identifier, err)
			}
		}
	}

	process.Status = StatusKilled

	if wasKeepAlive {
		if process.stopTimeout != nil {
			process.stopTimeoutOnce.Do(func() { close(process.stopTimeout) })
		}

		keepAliveLog := logrus.WithFields(logrus.Fields{
			"process_pid":  process.PID,
			"process_name": process.Name,
			"status":       "killed",
			"exit_code":    -1,
		})
		if err := blaxel.ScaleEnable(); err != nil {
			keepAliveLog.WithError(err).Warn("[KeepAlive] Failed to enable scale-to-zero after killing process")
		}
		keepAliveLog.Info("[KeepAlive] Stopped process")
	}

	return nil
}

// GetProcessOutput returns the stdout and stderr output of a process, read from
// its log files, up to what one response inlines. Use StreamProcessOutput for
// the whole of a log file that is larger than that.
func (pm *ProcessManager) GetProcessOutput(identifier string) (ProcessLogs, error) {
	return pm.getProcessOutput(identifier, maxLogsResponse())
}

// GetProcessOutputTail is GetProcessOutput limited to the last max bytes of each
// stream. Listing every process inlines all of their output in one response, so
// that path reads a tail rather than whole log files.
func (pm *ProcessManager) GetProcessOutputTail(identifier string, max int64) (ProcessLogs, error) {
	return pm.getProcessOutput(identifier, max)
}

func (pm *ProcessManager) getProcessOutput(identifier string, max int64) (ProcessLogs, error) {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return ProcessLogs{}, fmt.Errorf("process with PID %s not found", identifier)
	}

	// The log files hold the whole output; the in-memory buffers are the
	// fallback for when a file is gone or unreadable.
	stdout, ok := readLogTail(process.StdoutFile, max)
	if !ok {
		process.logLock.RLock()
		stdout = process.stdout.String()
		process.logLock.RUnlock()
	}

	stderr, ok := readLogTail(process.StderrFile, max)
	if !ok {
		process.logLock.RLock()
		stderr = process.stderr.String()
		process.logLock.RUnlock()
	}

	return ProcessLogs{
		Stdout: stdout,
		Stderr: stderr,
		Logs:   stdout + stderr,
	}, nil
}

// endsWithNewline reports whether the first size bytes of path end a line.
// A backlog cut mid-line must not be given a terminator of its own.
func endsWithNewline(path string, size int64) bool {
	if size <= 0 {
		return true
	}
	file, err := os.Open(path)
	if err != nil {
		return true
	}
	defer file.Close()
	var last [1]byte
	if _, err := file.ReadAt(last[:], size-1); err != nil {
		return true
	}
	return last[0] == '\n'
}

func (pm *ProcessManager) StreamProcessOutput(identifier string, w io.Writer) error {
	process, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}

	// Attach the writer before the backlog is replayed: it queues what the
	// process writes meanwhile, so that output is neither lost nor sent out of
	// order. The combined log file is written under the same lock, so its size
	// here is exactly where the queue starts - the backlog is replayed up to it
	// and no line is sent twice.
	pending := newPendingWriter(w)
	process.logLock.Lock()
	backlogEnd := int64(-1)
	if info, err := os.Stat(process.LogFile); err == nil {
		backlogEnd = info.Size()
	}
	process.logWriters = append(process.logWriters, pending)
	process.logLock.Unlock()

	// Write current content first - read from combined log file which has prefixed, ordered content
	// The combined log file is written by tailLogFiles with "stdout:" and "stderr:" prefixes
	if process.LogFile != "" && backlogEnd > 0 {
		// Parse prefixed lines and send as proper events, a line at a time so a
		// long-running process' backlog is not held in memory all at once.
		// This ensures JSONStreamWriter receives structured stdout/stderr events
		if file, truncated, err := openLogTail(process.LogFile, maxLogFile()); err == nil {
			if truncated {
				writeToLogWriter(w, "stdout", []byte(truncationMarker))
			}
			// backlogEnd is wherever the file happened to be when this writer
			// attached, which can be mid-line. Ending that fragment with a
			// newline would split one line in two, and the queued live output
			// carrying its remainder would look like a second line.
			backlogEndsMidLine := !endsWithNewline(process.LogFile, backlogEnd)
			scanner := bufio.NewScanner(backlogReader(file, backlogEnd))
			scanner.Buffer(make([]byte, 0, 64*1024), maxLogLineBytes)
			pendingLine := ""
			havePending := false
			emit := func(line string, last bool) {
				terminator := "\n"
				if last && backlogEndsMidLine {
					terminator = ""
				}
				if strings.HasPrefix(line, "stdout:") {
					writeToLogWriter(w, "stdout", []byte(strings.TrimPrefix(line, "stdout:")+terminator))
				} else if strings.HasPrefix(line, "stderr:") {
					writeToLogWriter(w, "stderr", []byte(strings.TrimPrefix(line, "stderr:")+terminator))
				} else if line != "" {
					// Fallback for unprefixed lines (shouldn't happen, but handle gracefully)
					writeToLogWriter(w, "stdout", []byte(line+terminator))
				}
			}
			for scanner.Scan() {
				if havePending {
					emit(pendingLine, false)
				}
				pendingLine = strings.TrimLeft(scanner.Text(), "\x00")
				havePending = true
			}
			if havePending {
				emit(pendingLine, true)
			}
			file.Close()
		}
	}

	pending.release()

	// Keep the connection warm for as long as this writer is attached.
	//
	// Tied to Finished rather than to a "status is running" check: during a
	// restart the status is briefly Failed, and stopping on that left the
	// stream with no traffic for the rest of its life. Idle connections are
	// reaped upstream after five minutes, so a quiet process would then lose
	// its stream for no reason. A failing write means the client is gone.
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		keepaliveMsg := []byte("[keepalive]\n")
		for {
			select {
			case <-process.Finished:
				return
			case <-ticker.C:
				if _, stillTracked := pm.GetProcessByIdentifier(identifier); !stillTracked {
					return
				}
				if _, err := w.Write(keepaliveMsg); err != nil {
					return
				}
				if f, ok := w.(interface{ Flush() }); ok {
					f.Flush()
				}
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
		if unwrapWriter(writer) == w {
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
