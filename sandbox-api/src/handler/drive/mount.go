package drive

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	blfsPath     = "/usr/local/bin/blfs"
	pollInterval = 100 * time.Millisecond
	mountTimeout = 30 * time.Second
)

// getAuthTokenPath returns the path to the identity token based on BL_ENV.
// Default is prod (blaxel.ai); use blaxel.dev when BL_ENV is "dev".
func getAuthTokenPath() string {
	if os.Getenv("BL_ENV") == "dev" {
		return "/var/run/secrets/blaxel.dev/identity/token"
	}
	return "/var/run/secrets/blaxel.ai/identity/token"
}

// validateLocalID validates that a UID/GID value is a non-negative integer.
func validateLocalID(value, name string) error {
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return fmt.Errorf("invalid %s %q: must be a non-negative integer", name, value)
	}
	return nil
}

// resolveMapping returns the effective local UID/GID value.
// Priority: request parameter > environment variable > empty (no mapping).
func resolveMapping(reqValue, envKey, name string) (string, error) {
	value := reqValue
	source := "request"
	if value == "" {
		value = os.Getenv(envKey)
		source = "env"
	}
	if value == "" {
		return "", nil
	}
	if err := validateLocalID(value, name); err != nil {
		return "", err
	}
	logrus.WithFields(logrus.Fields{
		"name":   name,
		"value":  value,
		"source": source,
	}).Debug("Resolved UID/GID mapping")
	return value, nil
}

// MountDrive mounts a drive using the blfs binary
// driveName: name of the drive resource
// mountPath: local path where the drive will be mounted
// drivePath: subpath within the drive to mount (defaults to "/")
// readOnly: if true, mount the drive as read-only
// uidMap: optional local UID to map to filer UID 0 (falls back to BLFS_UID_MAP env var)
// gidMap: optional local GID to map to filer GID 0 (falls back to BLFS_GID_MAP env var)
//
// Returns the effective uidMap and gidMap values that were actually applied
// (after resolving env var defaults) so the caller can report them accurately.
func MountDrive(driveName, mountPath, drivePath string, readOnly bool, uidMap, gidMap string) (effectiveUid, effectiveGid string, err error) {
	mountPath = NormalizeMountPath(mountPath)
	if err := ValidateDriveName(driveName); err != nil {
		return "", "", fmt.Errorf("invalid drive name: %w", err)
	}
	if err := ValidateMountPath(mountPath); err != nil {
		return "", "", fmt.Errorf("invalid mount path: %w", err)
	}

	// Get workspace ID from environment
	workspaceID := strings.ToLower(os.Getenv("BL_WORKSPACE_ID"))
	if workspaceID == "" {
		return "", "", fmt.Errorf("BL_WORKSPACE_ID environment variable not set")
	}

	// Construct infrastructure ID: drv-{driveName}-{workspaceID}
	infrastructureId := fmt.Sprintf("drv-%s-%s", driveName, workspaceID)

	// Get filer address
	filerAddress, err := getFilerAddress()
	if err != nil {
		return "", "", fmt.Errorf("failed to get filer address: %w", err)
	}

	// Create mount directory if it doesn't exist
	if err := os.MkdirAll(mountPath, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create mount directory: %w", err)
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
		fmt.Sprintf("-filer=%s:49200.49201", filerAddress),
		"-asyncDio=true",
		"-cacheSymlink=true",
		fmt.Sprintf("-auth.tokenFile=%s", getAuthTokenPath()),
		fmt.Sprintf("-filer.path=%s", filerPath),
		fmt.Sprintf("-dir=%s", mountPath),
		"-volumeServerAccess=filerProxy",
		"-dirAutoCreate=true",
	}

	// It's causing inconsistency issues on F_APPEND with the cache, so we're adding an environment variable to disable it
	if os.Getenv("BLFS_DISABLE_WRITEBACK_CACHE") == "true" {
		args = append(args, "-writebackCache=false")
	} else {
		args = append(args, "-writebackCache=true")
	}

	// Open read-only files with FUSE direct IO to bypass the kernel page cache. Keeps
	// memory flat during bulk reads of large files (no page cache growth), at the cost of
	// per-file caching and mmap on read-only handles. Forces writebackCache off (the two
	// are mutually exclusive). Linux only.
	if os.Getenv("BLFS_READ_DIRECT_IO") == "true" {
		args = append(args, "-readDirectIO=true")
	}

	// When a file's last handle is closed, drop its kernel page cache so clean read data
	// does not accumulate across many files until OOM. Keeps writeback caching enabled.
	// Long-open files (e.g. SQLite DB) keep their cache.
	if os.Getenv("BLFS_EVICT_PAGE_CACHE_ON_CLOSE") == "true" {
		args = append(args, "-evictPageCacheOnClose=true")
	}

	if readOnly {
		args = append(args, "-readOnly=true")
	}

	// Resolve UID/GID mappings (request param > env var > none)
	effectiveUidMap, err := resolveMapping(uidMap, "BLFS_UID_MAP", "uidMap")
	if err != nil {
		return "", "", fmt.Errorf("invalid uidMap: %w", err)
	}
	effectiveGidMap, err := resolveMapping(gidMap, "BLFS_GID_MAP", "gidMap")
	if err != nil {
		return "", "", fmt.Errorf("invalid gidMap: %w", err)
	}
	if effectiveUidMap != "" {
		args = append(args, fmt.Sprintf("-map.uid=%s:0", effectiveUidMap))
	}
	if effectiveGidMap != "" {
		args = append(args, fmt.Sprintf("-map.gid=%s:0", effectiveGidMap))
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
		return "", "", fmt.Errorf("failed to start blfs mount: %w", err)
	}

	pid := cmd.Process.Pid
	logrus.WithFields(logrus.Fields{
		"pid":        pid,
		"mount_path": mountPath,
	}).Info("Started blfs mount process")

	// Wait for the process in a goroutine so we can detect early exit.
	// cmd.ProcessState is only populated after Wait() returns.
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()

	// Poll until the mount point is ready or timeout.
	// Two-phase check: first wait for the kernel FUSE mount to appear in
	// /proc/mounts, then probe with ReadDir to confirm the server gRPC
	// stream is actually serving before we declare readiness.
	startTime := time.Now()
	mountDetected := false
	for time.Since(startTime) < mountTimeout {
		// Check if blfs exited early (e.g. ACL denied, config error)
		select {
		case waitErr := <-exitCh:
			msg := "blfs mount process exited unexpectedly"
			if waitErr != nil {
				msg = fmt.Sprintf("%s: %v", msg, waitErr)
			}
			logrus.WithFields(logrus.Fields{
				"pid":        pid,
				"mount_path": mountPath,
			}).Warn(msg)
			return "", "", fmt.Errorf("failed to mount drive: %s", msg)
		default:
		}

		if !mountDetected {
			if isMountPoint(mountPath) {
				mountDetected = true
				logrus.WithField("mount_path", mountPath).Debug("Kernel mount registered, waiting for server connection...")
			}
			time.Sleep(pollInterval)
			continue
		}

		// Phase 2: mount is registered, now probe until server gRPC is actually serving
		_, err := os.ReadDir(mountPath)
		if err == nil {
			logrus.WithField("mount_path", mountPath).Info("Mount point is ready and server connection established")
			return effectiveUidMap, effectiveGidMap, nil
		}
		logrus.WithField("mount_path", mountPath).Debug("Server connection not yet ready, retrying...")
		time.Sleep(pollInterval)
	}

	// Timeout — kill the process and clean up
	_ = syscall.Kill(pid, syscall.SIGKILL)
	<-exitCh // drain the channel to reap the process
	if isMountPoint(mountPath) {
		_ = UnmountDrive(mountPath)
	}
	return "", "", fmt.Errorf("timeout waiting for mount point to be ready after %s", mountTimeout)
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
