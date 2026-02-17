package drive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	blfsPath = "/usr/local/bin/blfs"
	authTokenPath = "/var/run/secrets/blaxel.dev/identity/token"
	pollInterval = 100 * time.Millisecond
	mountTimeout = 30 * time.Second
)

// MountDrive mounts a drive using the blfs binary
// driveName: name of the drive resource
// mountPath: local path where the drive will be mounted
// drivePath: subpath within the drive to mount (defaults to "/")
func MountDrive(driveName, mountPath, drivePath string) error {
	// Get workspace ID from environment
	workspaceID := os.Getenv("WORKSPACE_ID")
	if workspaceID == "" {
		return fmt.Errorf("WORKSPACE_ID environment variable not set")
	}

	// Construct infrastructure ID: agd-{driveName}-{workspaceID}
	infrastructureId := fmt.Sprintf("agd-%s-%s", driveName, workspaceID)

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

	// Timeout - kill the process and return error
	if err := cmd.Process.Kill(); err != nil {
		logrus.WithError(err).Warn("Failed to kill blfs mount process after timeout")
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

// isMountPoint checks if a directory is a mount point by comparing device IDs
func isMountPoint(path string) bool {
	// Get stat of the path
	pathStat, err := os.Stat(path)
	if err != nil {
		return false
	}

	// Get stat of the parent directory
	parentPath := filepath.Dir(path)
	parentStat, err := os.Stat(parentPath)
	if err != nil {
		return false
	}

	// If device IDs are different, it's a mount point
	// Note: this is a simplified check and may not work in all cases
	// For a more robust check, we could parse /proc/mounts
	pathDev := pathStat.Sys()
	parentDev := parentStat.Sys()

	return fmt.Sprintf("%v", pathDev) != fmt.Sprintf("%v", parentDev)
}

// CheckBlfsAvailable checks if the blfs binary is available
func CheckBlfsAvailable() bool {
	_, err := os.Stat(blfsPath)
	return err == nil
}
