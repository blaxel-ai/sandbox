package drive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	blfsPath      = "/usr/local/bin/blfs"
	authTokenPath = "/var/run/secrets/blaxel.dev/identity/token"
	pollInterval  = 100 * time.Millisecond
	mountTimeout  = 30 * time.Second
)

// MountDrive mounts a drive using the blfs binary
// driveName: name of the drive resource
// mountPath: local path where the drive will be mounted
// drivePath: subpath within the drive to mount (defaults to "/")
func MountDrive(driveName, mountPath, drivePath string) error {
	mountPath = NormalizeMountPath(mountPath)
	if err := ValidateDriveName(driveName); err != nil {
		return fmt.Errorf("invalid drive name: %w", err)
	}
	if err := ValidateMountPath(mountPath); err != nil {
		return fmt.Errorf("invalid mount path: %w", err)
	}

	// Get workspace ID from environment
	workspaceID := strings.ToLower(os.Getenv("BL_WORKSPACE_ID"))
	if workspaceID == "" {
		return fmt.Errorf("BL_WORKSPACE_ID environment variable not set")
	}

	// Construct infrastructure ID: drv-{driveName}-{workspaceID}
	infrastructureId := fmt.Sprintf("drv-%s-%s", driveName, workspaceID)

	// Get filer address
	filerAddress, err := getFilerAddress()
	if err != nil {
		return fmt.Errorf("failed to get filer address: %w", err)
	}

	// Create mount directory if it doesn't exist
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		return fmt.Errorf("failed to create mount directory: %w", err)
	}

	// Build the filer path: /buckets/{infrastructureId}{drivePath}
	// Ensure drivePath starts with /
	if !strings.HasPrefix(drivePath, "/") {
		drivePath = "/" + drivePath
	}
	// Remove trailing slash unless it's just "/"
	if drivePath != "/" {
		drivePath = strings.TrimSuffix(drivePath, "/")
	}
	filerPath := fmt.Sprintf("/buckets/%s%s", infrastructureId, drivePath)

	// Build blfs mount command
	args := []string{
		"mount",
		fmt.Sprintf("-filer=%s:8080", filerAddress),
		"-writebackCache=true",
		"-asyncDio=true",
		"-cacheSymlink=true",
		fmt.Sprintf("-auth.tokenFile=%s", authTokenPath),
		fmt.Sprintf("-filer.path=%s", filerPath),
		fmt.Sprintf("-dir=%s", mountPath),
		"-volumeServerAccess=filerProxy",
		"-dirAutoCreate=true",
	}

	logrus.WithFields(logrus.Fields{
		"drive_name":        driveName,
		"infrastructure_id": infrastructureId,
		"filer_address":     filerAddress,
		"filer_path":        filerPath,
		"mount_path":        mountPath,
	}).Debug("Executing blfs mount command")

	// Start the blfs mount process in the background
	cmd := exec.Command(blfsPath, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start blfs mount: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"pid":        cmd.Process.Pid,
		"mount_path": mountPath,
	}).Info("Started blfs mount process")

	// Poll until the mount point is ready or timeout
	startTime := time.Now()
	for time.Since(startTime) < mountTimeout {
		if isMountPoint(mountPath) {
			logrus.WithField("mount_path", mountPath).Info("Mount point is ready")
			return nil
		}

		// Check if the process has exited unexpectedly
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("blfs mount process exited unexpectedly: %s", cmd.ProcessState.String())
		}

		time.Sleep(pollInterval)
	}

	// Timeout - kill the process, reap it, and try to clean up any partial mount
	if err := cmd.Process.Kill(); err != nil {
		logrus.WithError(err).Warn("Failed to kill blfs mount process after timeout")
	}
	_ = cmd.Wait() // Reap the process to avoid zombie
	if isMountPoint(mountPath) {
		_ = UnmountDrive(mountPath) // Best-effort cleanup of partial mount
	}
	return fmt.Errorf("timeout waiting for mount point to be ready after %s", mountTimeout)
}

// getFilerAddress reads the filer address from /etc/resolv.conf
// The filer is the first nameserver listed in resolv.conf
func getFilerAddress() (string, error) {
	resolvConf, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return "", fmt.Errorf("failed to read /etc/resolv.conf: %w", err)
	}

	lines := strings.Split(string(resolvConf), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Look for nameserver lines
		if strings.HasPrefix(line, "nameserver") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				filerIP := fields[1]
				// Validate it's an IPv4 address
				parts := strings.Split(filerIP, ".")
				if len(parts) == 4 {
					logrus.WithField("filer_ip", filerIP).Debug("Found filer IP from resolv.conf")
					return filerIP, nil
				}
			}
		}
	}

	return "", fmt.Errorf("no valid nameserver found in /etc/resolv.conf")
}

// isMountPoint checks if a directory is a mount point by checking /proc/mounts
func isMountPoint(path string) bool {
	// Clean the path for comparison
	cleanPath := filepath.Clean(path)

	// Read /proc/mounts
	file, err := os.Open("/proc/mounts")
	if err != nil {
		logrus.WithError(err).Warn("Failed to open /proc/mounts, falling back to device ID check")
		return isMountPointByDeviceID(path)
	}
	defer file.Close()

	// Check if the path appears in /proc/mounts
	lines := strings.Split(string(mustReadAll(file)), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			mountPath := fields[1]
			if mountPath == cleanPath {
				return true
			}
		}
	}

	return false
}

// isMountPointByDeviceID checks if a directory is a mount point by comparing device IDs (fallback)
func isMountPointByDeviceID(path string) bool {
	pathStat, err := os.Stat(path)
	if err != nil {
		return false
	}
	parentPath := filepath.Dir(path)
	parentStat, err := os.Stat(parentPath)
	if err != nil {
		return false
	}
	pathSys, ok1 := pathStat.Sys().(*syscall.Stat_t)
	parentSys, ok2 := parentStat.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false
	}
	return pathSys.Dev != parentSys.Dev
}

// mustReadAll reads all data from a reader, panicking on error (for internal use only)
func mustReadAll(file *os.File) []byte {
	data, err := os.ReadFile(file.Name())
	if err != nil {
		return []byte{}
	}
	return data
}

// CheckBlfsAvailable checks if the blfs binary is available
func CheckBlfsAvailable() bool {
	_, err := os.Stat(blfsPath)
	return err == nil
}
