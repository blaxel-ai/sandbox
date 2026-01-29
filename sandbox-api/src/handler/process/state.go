package process

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
	"github.com/sirupsen/logrus"
)

const (
	// DefaultStateFilePath is the default path for the process state file
	DefaultStateFilePath = "/tmp/sandbox-api-process-state.json"
)

// ProcessState represents the serializable state of a process
type ProcessState struct {
	PID              string                  `json:"pid"`
	Name             string                  `json:"name"`
	Command          string                  `json:"command"`
	ProcessPid       int                     `json:"processPid"`
	StartedAt        time.Time               `json:"startedAt"`
	CompletedAt      *time.Time              `json:"completedAt,omitempty"`
	ExitCode         int                     `json:"exitCode"`
	Status           constants.ProcessStatus `json:"status"`
	WorkingDir       string                  `json:"workingDir"`
	LogFile          string                  `json:"logFile,omitempty"`
	StdoutFile       string                  `json:"stdoutFile,omitempty"`
	StderrFile       string                  `json:"stderrFile,omitempty"`
	Logs             string                  `json:"logs,omitempty"`
	Stdout           string                  `json:"stdout,omitempty"`
	Stderr           string                  `json:"stderr,omitempty"`
	RestartOnFailure bool                    `json:"restartOnFailure"`
	MaxRestarts      int                     `json:"maxRestarts"`
	RestartCount     int                     `json:"restartCount"`
	Env              map[string]string       `json:"env,omitempty"`
}

// ManagerState represents the full state of the process manager
type ManagerState struct {
	Version   int                     `json:"version"`
	SavedAt   time.Time               `json:"savedAt"`
	Processes map[string]ProcessState `json:"processes"`
}

// GetStateFilePath returns the path to the state file
func GetStateFilePath() string {
	if path := os.Getenv("SANDBOX_STATE_FILE"); path != "" {
		return path
	}
	return DefaultStateFilePath
}

// SaveState persists the current process state to disk
func (pm *ProcessManager) SaveState() error {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	state := ManagerState{
		Version:   1,
		SavedAt:   time.Now(),
		Processes: make(map[string]ProcessState),
	}

	logrus.WithField("totalInMemory", len(pm.processes)).Info("SaveState: starting to save processes")

	for pid, proc := range pm.processes {
		// Safely read logs under lock
		proc.logLock.RLock()
		var logs, stdout, stderr string
		if proc.logs != nil {
			logs = proc.logs.String()
		}
		if proc.stdout != nil {
			stdout = proc.stdout.String()
		}
		if proc.stderr != nil {
			stderr = proc.stderr.String()
		}
		proc.logLock.RUnlock()

		state.Processes[pid] = ProcessState{
			PID:              proc.PID,
			Name:             proc.Name,
			Command:          proc.Command,
			ProcessPid:       proc.ProcessPid,
			StartedAt:        proc.StartedAt,
			CompletedAt:      proc.CompletedAt,
			ExitCode:         proc.ExitCode,
			Status:           proc.Status,
			WorkingDir:       proc.WorkingDir,
			LogFile:          proc.LogFile,
			StdoutFile:       proc.StdoutFile,
			StderrFile:       proc.StderrFile,
			Logs:             logs,
			Stdout:           stdout,
			Stderr:           stderr,
			RestartOnFailure: proc.RestartOnFailure,
			MaxRestarts:      proc.MaxRestarts,
			RestartCount:     proc.RestartCount,
		}

		logrus.WithFields(logrus.Fields{
			"pid":        proc.PID,
			"name":       proc.Name,
			"command":    proc.Command,
			"processPid": proc.ProcessPid,
			"status":     proc.Status,
		}).Info("SaveState: saving process")
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	stateFile := GetStateFilePath()
	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(stateFile), 0755); err != nil {
		return fmt.Errorf("failed to create state directory: %w", err)
	}

	// Write to temp file first, then rename for atomicity
	tmpFile := stateFile + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write state file: %w", err)
	}

	if err := os.Rename(tmpFile, stateFile); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("failed to rename state file: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"path":         stateFile,
		"processCount": len(state.Processes),
		"fileSize":     len(data),
	}).Info("SaveState: process state saved to disk")

	return nil
}

// LoadState loads process state from disk and recovers running processes
func (pm *ProcessManager) LoadState() error {
	stateFile := GetStateFilePath()

	logrus.WithField("path", stateFile).Info("LoadState: attempting to load state file")

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			logrus.WithField("path", stateFile).Warn("LoadState: No process state file found, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"path":     stateFile,
		"fileSize": len(data),
	}).Info("LoadState: state file read successfully")

	var state ManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"version":      state.Version,
		"savedAt":      state.SavedAt,
		"processCount": len(state.Processes),
	}).Info("LoadState: state file parsed")

	pm.mu.Lock()
	defer pm.mu.Unlock()

	recoveredCount := 0
	deadCount := 0

	for pid, procState := range state.Processes {
		// Check if process is still running
		isRunning := isProcessRunning(procState.ProcessPid)

		logrus.WithFields(logrus.Fields{
			"pid":        procState.PID,
			"name":       procState.Name,
			"command":    procState.Command,
			"processPid": procState.ProcessPid,
			"status":     procState.Status,
			"isRunning":  isRunning,
		}).Info("LoadState: processing saved process")

		// Create ProcessInfo from saved state
		proc := &ProcessInfo{
			PID:              procState.PID,
			Name:             procState.Name,
			Command:          procState.Command,
			ProcessPid:       procState.ProcessPid,
			StartedAt:        procState.StartedAt,
			CompletedAt:      procState.CompletedAt,
			ExitCode:         procState.ExitCode,
			Status:           procState.Status,
			WorkingDir:       procState.WorkingDir,
			LogFile:          procState.LogFile,
			StdoutFile:       procState.StdoutFile,
			StderrFile:       procState.StderrFile,
			RestartOnFailure: procState.RestartOnFailure,
			MaxRestarts:      procState.MaxRestarts,
			RestartCount:     procState.RestartCount,
			Done:             make(chan struct{}),
			stdout:           &strings.Builder{},
			stderr:           &strings.Builder{},
			logs:             &strings.Builder{},
			logWriters:       make([]io.Writer, 0),
		}

		// Restore accumulated logs from saved state
		if procState.Logs != "" {
			proc.logs.WriteString(procState.Logs)
		}
		if procState.Stdout != "" {
			proc.stdout.WriteString(procState.Stdout)
		}
		if procState.Stderr != "" {
			proc.stderr.WriteString(procState.Stderr)
		}

		// Also read any new logs from the separate log files since state was saved
		// Use atomic read with bounds checking to avoid TOCTOU issues
		if procState.StdoutFile != "" {
			if newContent := readLogsSince(procState.StdoutFile, len(procState.Stdout)); len(newContent) > 0 {
				proc.stdout.Write(newContent)
				proc.logs.Write(newContent)
			}
		}
		if procState.StderrFile != "" {
			if newContent := readLogsSince(procState.StderrFile, len(procState.Stderr)); len(newContent) > 0 {
				proc.stderr.Write(newContent)
				proc.logs.Write(newContent)
			}
		}

		// Legacy: Also read from combined log file if separate files don't exist
		if procState.StdoutFile == "" && procState.LogFile != "" {
			if newContent := readLogsSince(procState.LogFile, len(procState.Logs)); len(newContent) > 0 {
				proc.logs.Write(newContent)
				proc.stdout.Write(newContent)
			}
		}

		if isRunning && procState.Status == StatusRunning {
			// Verify the process command matches what we expect
			// This prevents adopting arbitrary processes that happen to have the same PID
			if !verifyProcessCommand(proc.ProcessPid, proc.Command) {
				logrus.WithFields(logrus.Fields{
					"pid":        proc.PID,
					"name":       proc.Name,
					"command":    proc.Command,
					"processPid": proc.ProcessPid,
				}).Warn("Process command mismatch, marking as failed (PID may have been reused)")
				proc.Status = StatusFailed
				now := time.Now()
				proc.CompletedAt = &now
				proc.ExitCode = -1
				close(proc.Done)
				deadCount++
				pm.processes[pid] = proc
				continue
			}

			// Process is still running - adopt it
			proc.Status = StatusRunning
			recoveredCount++

			// Verify the process is actually responsive (can receive signals)
			// and check if it's still listening on expected ports
			if !verifyProcessHealth(proc.ProcessPid) {
				logrus.WithFields(logrus.Fields{
					"pid":     proc.PID,
					"name":    proc.Name,
					"command": proc.Command,
				}).Warn("Process exists but may not be healthy")
			}

			// Note: Log file will be read on-demand when logs are requested
			// We don't need to keep a file handle open for tailing since
			// the child process writes directly to the log file

			// Start a goroutine to monitor the adopted process
			go pm.monitorAdoptedProcess(proc)

			logrus.WithFields(logrus.Fields{
				"pid":     proc.PID,
				"name":    proc.Name,
				"command": proc.Command,
				"logFile": proc.LogFile,
			}).Info("Adopted running process")
		} else if procState.Status == StatusRunning {
			// Process was running but is now dead
			proc.Status = StatusFailed
			now := time.Now()
			proc.CompletedAt = &now
			proc.ExitCode = -1 // Unknown exit code
			deadCount++

			// Close the Done channel since process is no longer running
			close(proc.Done)

			logrus.WithFields(logrus.Fields{
				"pid":     proc.PID,
				"name":    proc.Name,
				"command": proc.Command,
			}).Warn("Process died during restart")
		} else {
			// Process was already completed/failed/stopped - just restore state
			if proc.CompletedAt != nil {
				close(proc.Done)
			}
		}

		pm.processes[pid] = proc
	}

	logrus.WithFields(logrus.Fields{
		"totalProcesses":    len(state.Processes),
		"recoveredRunning":  recoveredCount,
		"diedDuringRestart": deadCount,
		"alreadyCompleted":  len(state.Processes) - recoveredCount - deadCount,
	}).Info("Process state loaded from disk")

	return nil
}

// isProcessRunning checks if a process with the given PID is still running
// Returns false for zombie processes (which exist but are not actually running)
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Send signal 0 to check if process exists
	err := syscall.Kill(pid, 0)
	if err != nil {
		return false
	}

	// Check if the process is a zombie by reading /proc/[pid]/stat
	// The third field in stat is the state: R=running, S=sleeping, Z=zombie, etc.
	statPath := fmt.Sprintf("/proc/%d/stat", pid)
	data, err := os.ReadFile(statPath)
	if err != nil {
		// If we can't read the stat file, the process may have just exited
		return false
	}

	// Parse the stat file - format is: pid (comm) state ...
	// We need to find the state after the closing parenthesis
	statStr := string(data)
	closeParenIdx := strings.LastIndex(statStr, ")")
	if closeParenIdx == -1 || closeParenIdx+2 >= len(statStr) {
		return false
	}

	// State is the character after ") "
	state := statStr[closeParenIdx+2]

	// Z = zombie, X = dead - these are not running
	if state == 'Z' || state == 'X' {
		return false
	}

	return true
}

// reapZombieProcess attempts to reap a zombie process and get its exit code
// Returns the exit code if available, or 0 if we couldn't determine it
func reapZombieProcess(pid int) int {
	if pid <= 0 {
		return 0
	}

	// Try to wait on the process to reap it
	// We use WNOHANG to not block if the process isn't our child
	var wstatus syscall.WaitStatus
	wpid, err := syscall.Wait4(pid, &wstatus, syscall.WNOHANG, nil)
	if err != nil {
		// ECHILD means the process is not our child - we can't reap it
		// This is expected for adopted processes
		logrus.WithFields(logrus.Fields{
			"pid":   pid,
			"error": err,
		}).Debug("Could not reap process (not our child)")
		return 0
	}

	if wpid == pid {
		// Successfully reaped
		exitCode := 0
		if wstatus.Exited() {
			exitCode = wstatus.ExitStatus()
		} else if wstatus.Signaled() {
			// Process was killed by a signal
			exitCode = 128 + int(wstatus.Signal())
		}
		logrus.WithFields(logrus.Fields{
			"pid":      pid,
			"exitCode": exitCode,
		}).Debug("Successfully reaped zombie process")
		return exitCode
	}

	return 0
}

// readLogsSince safely reads log content from a file starting at a given offset.
// It handles TOCTOU issues by reading the file atomically and validating bounds.
// Returns nil if the file cannot be read or if offset is invalid.
func readLogsSince(filePath string, offset int) []byte {
	if filePath == "" || offset < 0 {
		return nil
	}

	// Open file for reading
	file, err := os.Open(filePath)
	if err != nil {
		return nil
	}
	defer file.Close()

	// Get file size atomically with the file handle
	stat, err := file.Stat()
	if err != nil {
		return nil
	}

	fileSize := stat.Size()

	// Bounds checking: if offset is beyond file size, the file was likely truncated/rotated
	// In this case, read from the beginning to avoid missing logs
	if int64(offset) > fileSize {
		logrus.WithFields(logrus.Fields{
			"file":     filePath,
			"offset":   offset,
			"fileSize": fileSize,
		}).Warn("Log file appears truncated, reading from beginning")
		offset = 0
	}

	// Nothing new to read
	if int64(offset) >= fileSize {
		return nil
	}

	// Seek to offset
	if _, err := file.Seek(int64(offset), 0); err != nil {
		return nil
	}

	// Read remaining content
	content, err := io.ReadAll(file)
	if err != nil {
		return nil
	}

	return content
}

// verifyProcessCommand checks if the running process matches the expected command.
// This provides basic ownership verification when adopting processes.
func verifyProcessCommand(pid int, expectedCommand string) bool {
	if pid <= 0 || expectedCommand == "" {
		return false
	}

	// Read the process command line from /proc
	cmdlinePath := fmt.Sprintf("/proc/%d/cmdline", pid)
	data, err := os.ReadFile(cmdlinePath)
	if err != nil {
		// If we can't read /proc, assume it matches (non-Linux or permission issues)
		return true
	}

	// cmdline is null-separated, convert to space-separated and compare
	actualCmd := strings.ReplaceAll(string(data), "\x00", " ")
	actualCmd = strings.TrimSpace(actualCmd)

	// Check if the expected command is contained in the actual command
	// This allows for argument variations while ensuring the base command matches
	return strings.Contains(actualCmd, expectedCommand) || strings.Contains(expectedCommand, strings.Split(actualCmd, " ")[0])
}

// verifyProcessHealth does additional checks beyond just process existence
func verifyProcessHealth(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Check if process exists
	if !isProcessRunning(pid) {
		return false
	}

	// Check /proc/<pid>/status for process state (Linux only)
	statusPath := fmt.Sprintf("/proc/%d/status", pid)
	data, err := os.ReadFile(statusPath)
	if err != nil {
		// If we can't read /proc, assume healthy if process exists
		return true
	}

	// Look for process state
	statusStr := string(data)
	for _, line := range strings.Split(statusStr, "\n") {
		if strings.HasPrefix(line, "State:") {
			// States: R (running), S (sleeping), D (disk sleep), Z (zombie), T (stopped)
			// Z (zombie) and T (stopped) are problematic
			if strings.Contains(line, "Z") || strings.Contains(line, "(zombie)") {
				logrus.WithField("pid", pid).Warn("Process is a zombie")
				return false
			}
			if strings.Contains(line, "T") && strings.Contains(line, "(stopped)") {
				logrus.WithField("pid", pid).Warn("Process is stopped")
				return false
			}
			break
		}
	}

	return true
}

// monitorAdoptedProcess monitors an adopted process for completion
func (pm *ProcessManager) monitorAdoptedProcess(proc *ProcessInfo) {
	logrus.WithFields(logrus.Fields{
		"pid":        proc.PID,
		"name":       proc.Name,
		"command":    proc.Command,
		"processPid": proc.ProcessPid,
	}).Info("Starting monitoring for adopted process")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	checkCount := 0
	for {
		select {
		case <-ticker.C:
			checkCount++
			isRunning := isProcessRunning(proc.ProcessPid)

			if checkCount <= 3 || checkCount%10 == 0 {
				logrus.WithFields(logrus.Fields{
					"pid":        proc.PID,
					"name":       proc.Name,
					"processPid": proc.ProcessPid,
					"isRunning":  isRunning,
					"checkCount": checkCount,
				}).Debug("Monitoring adopted process")
			}

			if !isRunning {
				// Process has exited (or is a zombie)
				now := time.Now()
				proc.CompletedAt = &now

				// Try to reap the zombie process to clean it up
				exitCode := reapZombieProcess(proc.ProcessPid)

				// Update status
				if proc.Status == StatusRunning {
					proc.Status = StatusCompleted
					proc.ExitCode = exitCode
				}

				// Update process in memory
				pm.mu.Lock()
				pm.processes[proc.PID] = proc
				pm.mu.Unlock()

				// Clean up resources
				proc.logLock.Lock()
				proc.logWriters = nil
				proc.logLock.Unlock()

				// Signal that the process is done
				close(proc.Done)

				logrus.WithFields(logrus.Fields{
					"pid":        proc.PID,
					"name":       proc.Name,
					"command":    proc.Command,
					"processPid": proc.ProcessPid,
					"checkCount": checkCount,
				}).Info("Adopted process completed")

				return
			}
		case <-proc.Done:
			// Process was killed/stopped through our API
			logrus.WithFields(logrus.Fields{
				"pid":  proc.PID,
				"name": proc.Name,
			}).Info("Adopted process monitoring stopped (Done channel closed)")
			return
		}
	}
}

// ClearState removes the state file from disk
func ClearState() error {
	stateFile := GetStateFilePath()
	err := os.Remove(stateFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove state file: %w", err)
	}
	return nil
}

// Upgrade configuration
const (
	DefaultReleaseURL = "https://github.com/blaxel-ai/sandbox/releases"
	ValidationPort    = 19999 // Port used for validating the new binary
)

// UpgradeState represents the current state of an upgrade
type UpgradeState string

const (
	UpgradeStateIdle      UpgradeState = "idle"      // No upgrade in progress
	UpgradeStateRunning   UpgradeState = "running"   // Upgrade is currently running
	UpgradeStateCompleted UpgradeState = "completed" // Upgrade completed successfully
	UpgradeStateFailed    UpgradeState = "failed"    // Upgrade failed
)

// UpgradeStatus represents the status of the last upgrade attempt
type UpgradeStatus struct {
	Status          UpgradeState `json:"status" binding:"required" example:"running"`            // Current state (idle, running, completed, failed)
	Step            string       `json:"step" binding:"required" example:"download"`             // Current/last step (none, starting, download, validate, replace, completed, skipped)
	Version         string       `json:"version" binding:"required" example:"latest"`            // Version being upgraded to
	LastAttempt     *time.Time   `json:"lastAttempt,omitempty"`                                  // When the upgrade was attempted
	Error           string       `json:"error,omitempty" example:"Failed to download binary"`    // Error message if failed
	DownloadURL     string       `json:"downloadUrl,omitempty" example:"https://github.com/..."` // URL used for download
	BinaryPath      string       `json:"binaryPath,omitempty" example:"/tmp/sandbox-api-new"`    // Path to downloaded binary
	BytesDownloaded int64        `json:"bytesDownloaded,omitempty" example:"25034936"`           // Bytes downloaded
} // @name UpgradeStatus

var (
	lastUpgradeStatus = UpgradeStatus{
		Status:  UpgradeStateIdle,
		Step:    "none",
		Version: "",
	}
	upgradeStatusMu sync.RWMutex
)

// GetLastUpgradeStatus returns the status of the last upgrade attempt
func GetLastUpgradeStatus() UpgradeStatus {
	upgradeStatusMu.RLock()
	defer upgradeStatusMu.RUnlock()
	return lastUpgradeStatus
}

// setUpgradeStatus updates the last upgrade status
func setUpgradeStatus(status UpgradeStatus) {
	upgradeStatusMu.Lock()
	defer upgradeStatusMu.Unlock()
	now := time.Now()
	status.LastAttempt = &now
	lastUpgradeStatus = status
}

// setUpgradeError records an upgrade error and marks status as failed
func setUpgradeError(step, errorMsg string, status *UpgradeStatus) {
	status.Status = UpgradeStateFailed
	status.Step = step
	status.Error = errorMsg
	setUpgradeStatus(*status)
}

// isDevMode checks if running in development mode (e.g., with air)
func isDevMode() bool {
	// Check for explicit dev mode env var
	if os.Getenv("SANDBOX_DEV_MODE") == "true" || os.Getenv("SANDBOX_DEV_MODE") == "1" {
		return true
	}

	// Check if running under air (air sets this)
	if os.Getenv("AIR_TMP_DIR") != "" {
		return true
	}

	// Check if the binary path contains "tmp" (air builds to tmp directory)
	if exe, err := os.Executable(); err == nil {
		if strings.Contains(exe, "/tmp/") || strings.Contains(exe, "\\tmp\\") {
			return true
		}
	}

	return false
}

// TriggerUpgrade initiates an upgrade of the sandbox-api process
// It downloads the specified binary from GitHub releases, validates it, and restarts
// Pre-built binaries are available at https://github.com/blaxel-ai/sandbox/releases
// Available versions: "develop", "main", "latest", or specific tag like "v1.0.0"
// baseURL can be customized for forks (e.g., https://github.com/your-org/sandbox/releases)
func TriggerUpgrade(version, baseURL string) {
	logger := logrus.WithField("component", "upgrade")

	// Initialize upgrade status
	status := UpgradeStatus{
		Status:  UpgradeStateRunning,
		Step:    "starting",
		Version: version,
	}
	setUpgradeStatus(status)

	// In dev mode, don't do the full upgrade - let air handle it
	if isDevMode() {
		logger.Info("Dev mode detected - skipping full upgrade (air will handle rebuilds)")
		logger.Info("To trigger a rebuild, modify a .go file or run 'air' manually")
		status.Status = UpgradeStateFailed
		status.Step = "skipped"
		status.Error = "dev mode detected - use air for rebuilds"
		setUpgradeStatus(status)
		return
	}

	logger.Info("Initiating sandbox-api hot upgrade...")

	// Step 1: Download the latest binary from GitHub releases
	status.Step = "download"
	setUpgradeStatus(status)

	newBinaryPath, downloadURL, bytesDownloaded, err := downloadReleaseWithDetails(version, baseURL)
	status.DownloadURL = downloadURL
	status.BytesDownloaded = bytesDownloaded

	if err != nil {
		errMsg := fmt.Sprintf("Failed to download release from %s: %v", downloadURL, err)
		logger.WithError(err).WithField("url", downloadURL).Error("Failed to download release")
		setUpgradeError("download", errMsg, &status)
		return
	}

	status.BinaryPath = newBinaryPath
	logger.WithFields(logrus.Fields{
		"path":  newBinaryPath,
		"bytes": bytesDownloaded,
		"url":   downloadURL,
	}).Info("Download completed successfully")

	// Step 2: Validate the new binary by running it on a different port
	status.Step = "validate"
	setUpgradeStatus(status)

	if err := validateNewBinary(newBinaryPath); err != nil {
		errMsg := fmt.Sprintf("Binary validation failed: %v", err)
		logger.WithError(err).WithFields(logrus.Fields{
			"binaryPath":     newBinaryPath,
			"validationPort": ValidationPort,
		}).Error("New binary validation failed, aborting upgrade")
		setUpgradeError("validate", errMsg, &status)
		os.Remove(newBinaryPath)
		return
	}

	logger.Info("Validation completed successfully")

	// Step 3: Replace current binary and upgrade
	status.Step = "replace"
	setUpgradeStatus(status)

	// Note: upgradeWithNewBinary will exec into the new process, so we won't return here on success
	// If it fails, it will call os.Exit, so we also won't return
	// Mark as successful before attempting (the new process won't have this state)
	status.Status = UpgradeStateCompleted
	status.Step = "completed"
	setUpgradeStatus(status)

	upgradeWithNewBinary(newBinaryPath)
}

// downloadReleaseWithDetails downloads the sandbox-api binary from GitHub releases
// version can be: "develop", "main", "latest", or a specific tag like "v1.0.0"
// baseURL is the releases URL (e.g., https://github.com/blaxel-ai/sandbox/releases)
// Returns: binaryPath, downloadURL, bytesDownloaded, error
func downloadReleaseWithDetails(version, baseURL string) (string, string, int64, error) {
	logger := logrus.WithField("component", "upgrade")

	// Detect OS and architecture
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	// Build the asset name based on OS and architecture
	assetName := fmt.Sprintf("sandbox-api-%s-%s", goos, goarch)
	logger.WithFields(logrus.Fields{
		"os":        goos,
		"arch":      goarch,
		"assetName": assetName,
		"baseURL":   baseURL,
	}).Info("Detecting platform for binary download")

	// Build the download URL
	var downloadURL string
	if version == "latest" {
		// "latest" is a special GitHub alias that points to the most recent non-prerelease
		downloadURL = fmt.Sprintf("%s/latest/download/%s", baseURL, assetName)
	} else {
		// For branch releases (develop, main) or specific tags (v1.0.0)
		downloadURL = fmt.Sprintf("%s/download/%s/%s", baseURL, version, assetName)
	}

	logger.WithFields(logrus.Fields{
		"version": version,
		"url":     downloadURL,
	}).Info("Downloading binary from GitHub releases")

	// Create temp file for download
	tmpFile := filepath.Join("/tmp", fmt.Sprintf("sandbox-api-new-%d", time.Now().UnixNano()))

	// Download the binary
	resp, err := http.Get(downloadURL)
	if err != nil {
		return "", downloadURL, 0, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Try to read error body for more details
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", downloadURL, 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Create the output file
	out, err := os.Create(tmpFile)
	if err != nil {
		return "", downloadURL, 0, fmt.Errorf("failed to create temp file %s: %w", tmpFile, err)
	}
	defer out.Close()

	// Copy the response body to the file
	written, err := io.Copy(out, resp.Body)
	if err != nil {
		os.Remove(tmpFile)
		return "", downloadURL, written, fmt.Errorf("failed to write binary after %d bytes: %w", written, err)
	}

	// Make executable
	if err := os.Chmod(tmpFile, 0755); err != nil {
		os.Remove(tmpFile)
		return "", downloadURL, written, fmt.Errorf("failed to chmod binary: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"path":  tmpFile,
		"bytes": written,
	}).Info("Binary downloaded successfully")

	return tmpFile, downloadURL, written, nil
}

// validateNewBinary starts the new binary on a different port and validates it works correctly
func validateNewBinary(binaryPath string) error {
	logger := logrus.WithField("component", "upgrade")
	logger.Info("Validating new binary before replacement...")

	// Get the process count from current instance for comparison
	pm := GetProcessManager()
	currentProcesses := pm.ListProcesses()
	runningCount := 0
	for _, p := range currentProcesses {
		if p.Status == StatusRunning {
			runningCount++
		}
	}

	logger.WithFields(logrus.Fields{
		"port":           ValidationPort,
		"currentRunning": runningCount,
		"totalProcesses": len(currentProcesses),
	}).Info("Starting new binary for validation")

	// Start the new binary on the validation port
	cmd := exec.Command(binaryPath, "-p", strconv.Itoa(ValidationPort))
	cmd.Env = os.Environ()
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		logger.WithError(err).Error("Failed to start new binary")
		return fmt.Errorf("failed to start new binary: %w", err)
	}
	logger.WithField("pid", cmd.Process.Pid).Info("Validation binary started")

	// Ensure we kill the validation process when done
	defer func() {
		if cmd.Process != nil {
			logger.WithField("pid", cmd.Process.Pid).Info("Stopping validation instance")
			cmd.Process.Kill()
			cmd.Wait()
			logger.Info("Validation instance stopped")
		}
	}()

	// Wait for the new binary to be ready (health check)
	// Use 127.0.0.1 instead of localhost - localhost may not resolve on all base images
	validationURL := fmt.Sprintf("http://127.0.0.1:%d", ValidationPort)
	logger.WithField("url", validationURL).Info("Waiting for validation instance to be healthy...")
	if err := waitForHealthy(validationURL, 30*time.Second); err != nil {
		logger.WithError(err).Error("Validation instance failed health check")
		return fmt.Errorf("new binary failed health check: %w", err)
	}

	logger.Info("Validation instance is healthy, checking process recovery...")

	// Verify process state was recovered correctly
	if err := verifyProcessRecovery(validationURL, len(currentProcesses), runningCount); err != nil {
		logger.WithError(err).Error("Process recovery verification failed")
		return fmt.Errorf("process recovery verification failed: %w", err)
	}

	logger.Info("New binary validation successful, proceeding with upgrade...")
	return nil
}

// waitForHealthy waits for the health endpoint to return OK
func waitForHealthy(baseURL string, timeout time.Duration) error {
	logger := logrus.WithField("component", "upgrade")
	healthURL := baseURL + "/health"
	deadline := time.Now().Add(timeout)
	attempts := 0
	var lastErr error

	for time.Now().Before(deadline) {
		attempts++
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				logger.WithFields(logrus.Fields{
					"attempts": attempts,
					"url":      healthURL,
				}).Info("Health check passed")
				return nil
			}
			logger.WithFields(logrus.Fields{
				"attempt": attempts,
				"status":  resp.StatusCode,
				"url":     healthURL,
			}).Warn("Health check returned non-OK status")
		} else {
			lastErr = err
			if attempts == 1 || attempts%10 == 0 {
				logger.Warnf("Health check attempt %d failed for %s: %v", attempts, healthURL, err)
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if lastErr != nil {
		return fmt.Errorf("health check timed out after %d attempts, last error: %w", attempts, lastErr)
	}

	return fmt.Errorf("health check timed out after %v", timeout)
}

// verifyProcessRecovery checks that the new instance recovered processes correctly
func verifyProcessRecovery(baseURL string, expectedTotal, expectedRunning int) error {
	logger := logrus.WithField("component", "upgrade")

	processURL := baseURL + "/process"
	resp, err := http.Get(processURL)
	if err != nil {
		return fmt.Errorf("failed to get process list: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("process list returned status %d", resp.StatusCode)
	}

	var processes []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&processes); err != nil {
		return fmt.Errorf("failed to decode process list: %w", err)
	}

	recoveredTotal := len(processes)
	recoveredRunning := 0
	for _, p := range processes {
		if status, ok := p["status"].(string); ok && status == string(StatusRunning) {
			recoveredRunning++
		}
	}

	logger.WithFields(logrus.Fields{
		"expectedTotal":    expectedTotal,
		"recoveredTotal":   recoveredTotal,
		"expectedRunning":  expectedRunning,
		"recoveredRunning": recoveredRunning,
	}).Info("Process recovery comparison")

	// Process recovery is mandatory - if we can't recover processes, fail the upgrade
	if recoveredTotal != expectedTotal {
		return fmt.Errorf("process count mismatch: expected %d, got %d - upgrade aborted to preserve running processes", expectedTotal, recoveredTotal)
	}

	// Verify running processes were recovered
	if recoveredRunning != expectedRunning {
		return fmt.Errorf("running process count mismatch: expected %d running, got %d - upgrade aborted", expectedRunning, recoveredRunning)
	}

	logger.Info("All processes recovered successfully")
	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// validateBinaryFormat performs basic validation on a binary file to ensure it's a valid executable.
// It checks for ELF (Linux) or Mach-O (macOS) magic bytes and verifies the file is executable.
func validateBinaryFormat(binaryPath string) error {
	// Check file exists and is readable
	stat, err := os.Stat(binaryPath)
	if err != nil {
		return fmt.Errorf("cannot stat binary: %w", err)
	}

	// Verify it's a regular file
	if !stat.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}

	// Check file size is reasonable (at least 1KB, at most 500MB)
	if stat.Size() < 1024 {
		return fmt.Errorf("binary too small (%d bytes)", stat.Size())
	}
	if stat.Size() > 500*1024*1024 {
		return fmt.Errorf("binary too large (%d bytes)", stat.Size())
	}

	// Read the first few bytes to check magic number
	file, err := os.Open(binaryPath)
	if err != nil {
		return fmt.Errorf("cannot open binary: %w", err)
	}
	defer file.Close()

	magic := make([]byte, 4)
	if _, err := file.Read(magic); err != nil {
		return fmt.Errorf("cannot read binary header: %w", err)
	}

	// Check for valid executable formats
	isELF := magic[0] == 0x7f && magic[1] == 'E' && magic[2] == 'L' && magic[3] == 'F'
	isMachO := (magic[0] == 0xfe && magic[1] == 0xed && magic[2] == 0xfa && magic[3] == 0xce) || // 32-bit
		(magic[0] == 0xfe && magic[1] == 0xed && magic[2] == 0xfa && magic[3] == 0xcf) || // 64-bit
		(magic[0] == 0xce && magic[1] == 0xfa && magic[2] == 0xed && magic[3] == 0xfe) || // 32-bit reverse
		(magic[0] == 0xcf && magic[1] == 0xfa && magic[2] == 0xed && magic[3] == 0xfe) // 64-bit reverse

	if !isELF && !isMachO {
		return fmt.Errorf("invalid executable format (magic: %x)", magic)
	}

	return nil
}

// upgradeWithNewBinary moves the new binary to a permanent location and execs into it
// We can't overwrite the running binary ("text file busy"), so we exec into a new file
func upgradeWithNewBinary(newBinaryPath string) {
	logger := logrus.WithField("component", "upgrade")

	// Validate the new binary before replacing
	if err := validateBinaryFormat(newBinaryPath); err != nil {
		logger.WithError(err).Error("New binary validation failed")
		os.Remove(newBinaryPath)
		return
	}

	// Get current executable path
	currentExe, err := os.Executable()
	if err != nil {
		logger.WithError(err).Error("Failed to get current executable path")
		os.Exit(1)
	}

	// Resolve symlinks to get the real path
	currentExe, err = filepath.EvalSymlinks(currentExe)
	if err != nil {
		logger.WithError(err).Warn("Failed to resolve symlinks, using original path")
	}

	// Determine the permanent path for the new binary
	// We place it in the same directory as the current binary
	currentDir := filepath.Dir(currentExe)
	permanentPath := filepath.Join(currentDir, "sandbox-api-upgraded")

	logger.WithFields(logrus.Fields{
		"current":       currentExe,
		"new":           newBinaryPath,
		"permanentPath": permanentPath,
	}).Info("Moving new binary to permanent location")

	// Remove old upgraded binary if it exists
	os.Remove(permanentPath)

	// Move (or copy) new binary to permanent location
	if err := os.Rename(newBinaryPath, permanentPath); err != nil {
		// If rename fails (e.g., cross-device), try copy
		if err := copyFile(newBinaryPath, permanentPath); err != nil {
			logger.WithError(err).Error("Failed to move new binary to permanent location")
			os.Remove(newBinaryPath)
			os.Exit(1)
		}
		os.Remove(newBinaryPath)
	}

	// Make executable
	if err := os.Chmod(permanentPath, 0755); err != nil {
		logger.WithError(err).Warn("Failed to chmod new binary")
	}

	logger.Info("Executing new binary...")

	// Exec into the new binary (this replaces the current process)
	execIntoNewBinary(permanentPath)
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Copy permissions
	sourceInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, sourceInfo.Mode())
}

// execIntoNewBinary execs into the new binary at the given path
func execIntoNewBinary(newBinaryPath string) {
	logger := logrus.WithField("component", "upgrade")

	// Get current arguments (use the new binary path as args[0])
	args := os.Args
	args[0] = newBinaryPath

	// Increment upgrade count and pass to new process
	currentCount := 0
	if countStr := os.Getenv("SANDBOX_UPGRADE_COUNT"); countStr != "" {
		if count, err := strconv.Atoi(countStr); err == nil {
			currentCount = count
		}
	}
	newCount := currentCount + 1

	// Build environment with updated upgrade count
	env := os.Environ()
	upgradeEnvSet := false
	for i, e := range env {
		if strings.HasPrefix(e, "SANDBOX_UPGRADE_COUNT=") {
			env[i] = fmt.Sprintf("SANDBOX_UPGRADE_COUNT=%d", newCount)
			upgradeEnvSet = true
			break
		}
	}
	if !upgradeEnvSet {
		env = append(env, fmt.Sprintf("SANDBOX_UPGRADE_COUNT=%d", newCount))
	}

	logger.WithFields(logrus.Fields{
		"executable":   newBinaryPath,
		"args":         args,
		"upgradeCount": newCount,
	}).Info("Executing new binary")

	// Use syscall.Exec to replace the current process
	// This is the cleanest way to upgrade - it replaces the current process
	// with the new binary without creating a child process
	err := syscall.Exec(newBinaryPath, args, env)
	if err != nil {
		logger.WithError(err).Error("Failed to exec new binary")
		os.Exit(1)
	}
}
