package drive

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
	"github.com/sirupsen/logrus"
)

const (
	// BlfsPath is the binary the drive mount runs, as root. It is exported so
	// the paths this API executes with privileges can be named where they have
	// to be protected from being replaced.
	BlfsPath     = "/usr/local/bin/blfs"
	pollInterval = 100 * time.Millisecond
	mountTimeout = 30 * time.Second
)

// ErrMountPathBusy indicates the mount path is already occupied by a mount that
// does not match the requested drive, so mounting would conflict.
var ErrMountPathBusy = errors.New("mount path already in use")

// normalizeDrivePath ensures the drive subpath has a leading slash and no
// trailing slash (except for the root "/").
func normalizeDrivePath(drivePath string) string {
	if !strings.HasPrefix(drivePath, "/") {
		drivePath = "/" + drivePath
	}
	if drivePath != "/" {
		drivePath = strings.TrimSuffix(drivePath, "/")
	}
	return drivePath
}

// createMountPoint makes sure mountPath is a directory, and reports whether
// this call created it.
//
// The distinction matters because the mount point is handed to the workload
// user: mountPath comes from the request, so chowning a directory that already
// existed would let a process ask for a root-owned directory such as
// /usr/local/bin and take it over. Only a directory created here is safe to
// hand over, since it holds nothing the workload did not already own.
func createMountPoint(mountPath string) (created bool, err error) {
	// Clean first: for "/mnt/data/", filepath.Dir would be "/mnt/data" and the
	// parent creation below would create the mount point itself, hiding the fact
	// that this call made it.
	mountPath = filepath.Clean(mountPath)
	if err := os.MkdirAll(filepath.Dir(mountPath), 0755); err != nil {
		return false, err
	}
	if err := os.Mkdir(mountPath, 0755); err == nil {
		return true, nil
	} else if !errors.Is(err, os.ErrExist) {
		return false, err
	}
	// Something is already there: it is only usable as a mount target if it is
	// a directory.
	info, err := os.Stat(mountPath)
	if err != nil {
		return false, err
	}
	if !info.IsDir() {
		return false, fmt.Errorf("%s exists and is not a directory", mountPath)
	}
	return false, nil
}

// chownMountPoint changes the owner of the directory at mountPath without
// following symlinks, so the target cannot be swapped for a link to a
// privileged path between its creation and this call.
func chownMountPoint(mountPath string, uid, gid int) error {
	dir, err := os.OpenFile(mountPath, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Chown(uid, gid)
}

// existingMount returns the blfs mount currently backing mountPath, or nil if
// none is managed there.
func existingMount(mountPath string) (*MountInfo, error) {
	mounts, err := ListMounts()
	if err != nil {
		return nil, err
	}
	clean := filepath.Clean(mountPath)
	for i := range mounts {
		if filepath.Clean(mounts[i].MountPath) == clean {
			return &mounts[i], nil
		}
	}
	return nil, nil
}

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
func resolveMapping(reqValue, envKey, name string, workloadID int) (string, error) {
	value := reqValue
	source := "request"
	if value == "" {
		value = os.Getenv(envKey)
		source = "env"
	}
	// Drive content belongs to filer uid/gid 0. Mapping it onto the workload
	// identity by default is what makes the mount writable by processes, which
	// no longer run as root.
	if value == "" && workloadID > 0 {
		value = strconv.Itoa(workloadID)
		source = "workload identity"
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

func crossMountCacheCoherenceFlag(value string) string {
	return "-crossMountCacheCoherence=" + strconv.FormatBool(value == "true")
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

	drivePath = normalizeDrivePath(drivePath)

	// Serialize all mount operations targeting the same path. Without this,
	// duplicate or racing mount requests each spawn a blfs process; the second
	// process recomputes the identical LevelDB cache directory, fails to flock
	// the caches the first already holds, and crashes — and later retries then
	// trip the filesystem's double-mount guard on the still-active mount point.
	lock := mountLockFor(mountPath)
	lock.Lock()
	defer lock.Unlock()

	// Resolve UID/GID mappings (request param > env var > workload identity > none).
	workloadUid, workloadGid := -1, -1
	if id := identity.Get(); id != nil {
		workloadUid, workloadGid = id.Uid, id.Gid
	}
	effectiveUidMap, err := resolveMapping(uidMap, "BLFS_UID_MAP", "uidMap", workloadUid)
	if err != nil {
		return "", "", fmt.Errorf("invalid uidMap: %w", err)
	}
	effectiveGidMap, err := resolveMapping(gidMap, "BLFS_GID_MAP", "gidMap", workloadGid)
	if err != nil {
		return "", "", fmt.Errorf("invalid gidMap: %w", err)
	}

	// If the target is already an active mount point, do not launch a second
	// blfs process for it. Return idempotently when the existing mount already
	// serves the requested drive, and report a conflict otherwise.
	if isMountPoint(mountPath) {
		existing, err := existingMount(mountPath)
		if err != nil {
			return "", "", fmt.Errorf("failed to inspect existing mounts: %w", err)
		}
		if existing == nil {
			return "", "", fmt.Errorf("%w: %s is occupied by a non-blfs mount", ErrMountPathBusy, mountPath)
		}
		if existing.DriveName != driveName || existing.DrivePath != drivePath {
			return "", "", fmt.Errorf("%w: %s (drivePath=%s) is already mounted at %s", ErrMountPathBusy, existing.DriveName, existing.DrivePath, mountPath)
		}
		logrus.WithFields(logrus.Fields{
			"drive_name": driveName,
			"mount_path": mountPath,
		}).Info("Drive already mounted at path, skipping duplicate mount")
		return effectiveUidMap, effectiveGidMap, nil
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

	// Create the mount directory if it doesn't exist, and hand it to the
	// workload user when this call created it: without that the directory is
	// unusable by processes while the drive is not mounted over it.
	created, err := createMountPoint(mountPath)
	if err != nil {
		return "", "", fmt.Errorf("failed to create mount directory: %w", err)
	}
	if id := identity.Get(); id != nil && created {
		// A failure here means the directory we just created is not what we
		// expect any more (it was replaced by a symlink, for instance), so the
		// mount must not proceed against it.
		if err := chownMountPoint(mountPath, id.Uid, id.Gid); err != nil {
			return "", "", fmt.Errorf("failed to hand mount point to the workload user: %w", err)
		}
	}

	// Build the filer path: /buckets/{infrastructureId}{drivePath}
	filerPath := fmt.Sprintf("/buckets/%s%s", infrastructureId, drivePath)

	// Build blfs mount command
	args := []string{
		"mount",
		fmt.Sprintf("-filer=%s", formatFilerServerAddress(filerAddress)),
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

	args = append(args, crossMountCacheCoherenceFlag(os.Getenv("BLFS_CROSS_MOUNT_CACHE_COHERENCE")))

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
	cmd := exec.Command(BlfsPath, args...)
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
		_ = unmountDriveLocked(mountPath)
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
	return parseFilerAddress(resolvConf)
}

func parseFilerAddress(resolvConf []byte) (string, error) {
	lines := strings.Split(string(resolvConf), "\n")
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}

		address, err := netip.ParseAddr(fields[1])
		if err == nil {
			filerAddress := address.String()
			logrus.WithField("filer_address", filerAddress).Debug("Found filer address from resolv.conf")
			return filerAddress, nil
		}
	}

	return "", fmt.Errorf("no valid nameserver found in /etc/resolv.conf")
}

// formatFilerServerAddress preserves SeaweedFS's host:http.grpc address format.
// Its parser splits on the final colon, so an IPv6 literal must remain unbracketed
// here; SeaweedFS adds brackets when constructing the HTTP and gRPC endpoints.
func formatFilerServerAddress(address string) string {
	return fmt.Sprintf("%s:49200.49201", address)
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
	_, err := os.Stat(BlfsPath)
	return err == nil
}
