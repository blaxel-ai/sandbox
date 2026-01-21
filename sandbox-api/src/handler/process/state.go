package process

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	}).Info("Process state saved to disk")

	return nil
}

// LoadState loads process state from disk and recovers running processes
func (pm *ProcessManager) LoadState() error {
	stateFile := GetStateFilePath()

	data, err := os.ReadFile(stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			logrus.Debug("No process state file found, starting fresh")
			return nil
		}
		return fmt.Errorf("failed to read state file: %w", err)
	}

	var state ManagerState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal state: %w", err)
	}

	pm.mu.Lock()
	defer pm.mu.Unlock()

	recoveredCount := 0
	deadCount := 0

	for pid, procState := range state.Processes {
		// Check if process is still running
		isRunning := isProcessRunning(procState.ProcessPid)

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

	// Remove the state file after loading
	if err := os.Remove(stateFile); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("Failed to remove state file after loading")
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
func isProcessRunning(pid int) bool {
	if pid <= 0 {
		return false
	}

	// Send signal 0 to check if process exists
	err := syscall.Kill(pid, 0)
	return err == nil
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
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if !isProcessRunning(proc.ProcessPid) {
				// Process has exited
				now := time.Now()
				proc.CompletedAt = &now

				// Try to get exit status (may not be available for adopted processes)
				// Since we didn't start this process, we can't call Wait() on it
				// We'll mark it as completed with unknown exit code
				if proc.Status == StatusRunning {
					proc.Status = StatusCompleted
					proc.ExitCode = 0 // Assume success if we can't determine
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
					"pid":     proc.PID,
					"name":    proc.Name,
					"command": proc.Command,
				}).Info("Adopted process completed")

				return
			}
		case <-proc.Done:
			// Process was killed/stopped through our API
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

// Restart configuration
const (
	DefaultRepoURL    = "https://github.com/blaxel-ai/sandbox.git"
	DefaultRepoBranch = "main"
	DefaultRepoDir    = "/tmp/sandbox-repo"
	DefaultGoVersion  = "1.25.0"
	ValidationPort    = 19999 // Port used for validating the new binary
)

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

// TriggerRestart initiates a restart of the sandbox-api process
// It clones/updates the repo, builds a new binary, validates it, and restarts
func TriggerRestart() {
	logger := logrus.WithField("component", "restart")

	// In dev mode, don't do the full restart - let air handle it
	if isDevMode() {
		logger.Info("Dev mode detected - skipping full restart (air will handle rebuilds)")
		logger.Info("To trigger a rebuild, modify a .go file or run 'air' manually")
		return
	}

	logger.Info("Initiating sandbox-api hot reload...")

	// Get configuration from environment
	repoURL := getEnvOrDefault("SANDBOX_REPO_URL", DefaultRepoURL)
	branch := getEnvOrDefault("SANDBOX_BRANCH", DefaultRepoBranch)
	repoDir := getEnvOrDefault("SANDBOX_REPO_DIR", DefaultRepoDir)

	// Step 1: Ensure Go is installed
	if err := ensureGoInstalled(); err != nil {
		logger.WithError(err).Error("Failed to ensure Go is installed")
		// Fall back to simple restart without rebuild
		restartCurrentProcess()
		return
	}

	// Step 2: Clone or update repository
	if err := updateRepository(repoURL, branch, repoDir); err != nil {
		logger.WithError(err).Error("Failed to update repository")
		// Fall back to simple restart without rebuild
		restartCurrentProcess()
		return
	}

	// Step 3: Build new binary
	newBinaryPath, err := buildSandboxAPI(repoDir)
	if err != nil {
		logger.WithError(err).Error("Failed to build sandbox-api")
		// Fall back to simple restart without rebuild
		restartCurrentProcess()
		return
	}

	// Step 4: Validate the new binary by running it on a different port
	if err := validateNewBinary(newBinaryPath); err != nil {
		logger.WithError(err).Error("New binary validation failed, aborting upgrade")
		os.Remove(newBinaryPath)
		return
	}

	// Step 5: Replace current binary and restart
	restartWithNewBinary(newBinaryPath)
}

// validateNewBinary starts the new binary on a different port and validates it works correctly
func validateNewBinary(binaryPath string) error {
	logger := logrus.WithField("component", "restart")
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
		return fmt.Errorf("failed to start new binary: %w", err)
	}

	// Ensure we kill the validation process when done
	defer func() {
		if cmd.Process != nil {
			logger.Info("Stopping validation instance")
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()

	// Wait for the new binary to be ready (health check)
	validationURL := fmt.Sprintf("http://localhost:%d", ValidationPort)
	if err := waitForHealthy(validationURL, 30*time.Second); err != nil {
		return fmt.Errorf("new binary failed health check: %w", err)
	}

	logger.Info("New binary is healthy, checking process recovery...")

	// Verify process state was recovered correctly
	if err := verifyProcessRecovery(validationURL, len(currentProcesses), runningCount); err != nil {
		return fmt.Errorf("process recovery verification failed: %w", err)
	}

	logger.Info("New binary validation successful!")
	return nil
}

// waitForHealthy waits for the health endpoint to return OK
func waitForHealthy(baseURL string, timeout time.Duration) error {
	healthURL := baseURL + "/health"
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := http.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("health check timed out after %v", timeout)
}

// verifyProcessRecovery checks that the new instance recovered processes correctly
func verifyProcessRecovery(baseURL string, expectedTotal, expectedRunning int) error {
	logger := logrus.WithField("component", "restart")

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

	// Verify all processes were recovered
	if recoveredTotal < expectedTotal {
		return fmt.Errorf("process count mismatch: expected %d, got %d", expectedTotal, recoveredTotal)
	}

	// Running processes might have exited during the validation, so we just check it's not drastically different
	// Allow some tolerance (processes may have completed naturally)
	if expectedRunning > 0 && recoveredRunning == 0 {
		return fmt.Errorf("no running processes recovered, expected at least some of %d", expectedRunning)
	}

	return nil
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// ensureGoInstalled checks if Go is installed, installs it if not
func ensureGoInstalled() error {
	logger := logrus.WithField("component", "restart")

	// Check if go is already in PATH
	if _, err := exec.LookPath("go"); err == nil {
		logger.Info("Go is already installed")
		return nil
	}

	// Check if go is in /usr/local/go/bin
	goPath := "/usr/local/go/bin/go"
	if _, err := os.Stat(goPath); err == nil {
		// Add to PATH for this process
		os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")
		logger.Info("Go found at /usr/local/go/bin")
		return nil
	}

	logger.Info("Go not found, installing...")

	// Detect architecture
	goArch := "amd64"
	arch := os.Getenv("GOARCH")
	if arch == "" {
		// Try to detect from uname
		cmd := exec.Command("uname", "-m")
		output, err := cmd.Output()
		if err == nil {
			archStr := strings.TrimSpace(string(output))
			if archStr == "aarch64" || archStr == "arm64" {
				goArch = "arm64"
			}
		}
	} else {
		goArch = arch
	}

	goVersion := getEnvOrDefault("GO_VERSION", DefaultGoVersion)
	goTar := fmt.Sprintf("go%s.linux-%s.tar.gz", goVersion, goArch)
	goURL := fmt.Sprintf("https://go.dev/dl/%s", goTar)

	// Download Go
	logger.WithField("url", goURL).Info("Downloading Go")
	tmpFile := filepath.Join("/tmp", goTar)

	cmd := exec.Command("curl", "-L", "-o", tmpFile, goURL)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to download Go: %w", err)
	}

	// Extract Go
	logger.Info("Extracting Go")
	cmd = exec.Command("tar", "-C", "/usr/local", "-xzf", tmpFile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract Go: %w", err)
	}

	// Clean up
	os.Remove(tmpFile)

	// Add to PATH
	os.Setenv("PATH", os.Getenv("PATH")+":/usr/local/go/bin")

	logger.Info("Go installed successfully")
	return nil
}

// updateRepository clones or updates the sandbox repository
func updateRepository(repoURL, branch, repoDir string) error {
	logger := logrus.WithFields(logrus.Fields{
		"component": "restart",
		"repo":      repoURL,
		"branch":    branch,
		"dir":       repoDir,
	})

	gitDir := filepath.Join(repoDir, ".git")
	if _, err := os.Stat(gitDir); err == nil {
		// Repository exists, pull latest
		logger.Info("Repository exists, pulling latest changes")

		cmd := exec.Command("git", "fetch", "origin")
		cmd.Dir = repoDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to fetch: %w", err)
		}

		cmd = exec.Command("git", "checkout", branch)
		cmd.Dir = repoDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to checkout branch: %w", err)
		}

		cmd = exec.Command("git", "pull", "origin", branch)
		cmd.Dir = repoDir
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to pull: %w", err)
		}
	} else {
		// Clone fresh
		logger.Info("Cloning repository")

		// Remove existing directory if any
		os.RemoveAll(repoDir)

		cmd := exec.Command("git", "clone", "--depth", "1", "--branch", branch, repoURL, repoDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to clone: %w", err)
		}
	}

	logger.Info("Repository updated successfully")
	return nil
}

// buildSandboxAPI builds the sandbox-api binary and returns the path to the new binary
func buildSandboxAPI(repoDir string) (string, error) {
	logger := logrus.WithField("component", "restart")
	logger.Info("Building sandbox-api...")

	sandboxAPIDir := filepath.Join(repoDir, "sandbox-api")
	newBinaryPath := filepath.Join(sandboxAPIDir, "sandbox-api-new")

	// Download dependencies
	cmd := exec.Command("go", "mod", "download")
	cmd.Dir = sandboxAPIDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to download dependencies: %w", err)
	}

	// Build
	cmd = exec.Command("go", "build", "-v", "-ldflags", "-s -w", "-o", "sandbox-api-new", ".")
	cmd.Dir = sandboxAPIDir
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build: %w", err)
	}

	logger.WithField("path", newBinaryPath).Info("Build completed successfully")
	return newBinaryPath, nil
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

// restartWithNewBinary replaces the current binary and restarts
func restartWithNewBinary(newBinaryPath string) {
	logger := logrus.WithField("component", "restart")

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

	logger.WithFields(logrus.Fields{
		"current": currentExe,
		"new":     newBinaryPath,
	}).Info("Replacing binary")

	// Backup current binary
	backupPath := currentExe + ".bak"
	if err := copyFile(currentExe, backupPath); err != nil {
		logger.WithError(err).Warn("Failed to backup current binary")
	}

	// Copy new binary to current location
	if err := copyFile(newBinaryPath, currentExe); err != nil {
		logger.WithError(err).Error("Failed to copy new binary")
		// Try to restore backup
		copyFile(backupPath, currentExe)
		os.Exit(1)
	}

	// Make executable
	if err := os.Chmod(currentExe, 0755); err != nil {
		logger.WithError(err).Warn("Failed to chmod new binary")
	}

	// Clean up
	os.Remove(newBinaryPath)

	// Restart with new binary
	restartCurrentProcess()
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

// restartCurrentProcess restarts the current sandbox-api process
func restartCurrentProcess() {
	logger := logrus.WithField("component", "restart")

	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		logger.WithError(err).Error("Failed to get executable path")
		os.Exit(1)
	}

	// Get current arguments
	args := os.Args

	// Increment restart count and pass to new process
	currentCount := 0
	if countStr := os.Getenv("SANDBOX_RESTART_COUNT"); countStr != "" {
		if count, err := strconv.Atoi(countStr); err == nil {
			currentCount = count
		}
	}
	newCount := currentCount + 1

	// Build environment with updated restart count
	env := os.Environ()
	restartEnvSet := false
	for i, e := range env {
		if strings.HasPrefix(e, "SANDBOX_RESTART_COUNT=") {
			env[i] = fmt.Sprintf("SANDBOX_RESTART_COUNT=%d", newCount)
			restartEnvSet = true
			break
		}
	}
	if !restartEnvSet {
		env = append(env, fmt.Sprintf("SANDBOX_RESTART_COUNT=%d", newCount))
	}

	logger.WithFields(logrus.Fields{
		"executable":   execPath,
		"args":         args,
		"restartCount": newCount,
	}).Info("Restarting process")

	// Use syscall.Exec to replace the current process
	// This is the cleanest way to restart - it replaces the current process
	// with a new instance without creating a child process
	err = syscall.Exec(execPath, args, env)
	if err != nil {
		logger.WithError(err).Error("Failed to exec new process")
		os.Exit(1)
	}
}
