package drive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openMountTarget opens mountPath one component at a time without following
// symlinks. blfs then mounts through this descriptor rather than the path, so
// the directory mounted on is the one the path checks were made for, even if
// the workload swaps a component for a symlink to / in the meantime.
func openMountTarget(mountPath string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, name := range strings.Split(strings.Trim(filepath.Clean(mountPath), "/"), "/") {
		// O_PATH|O_NOFOLLOW opens a symlink itself, and O_DIRECTORY then
		// refuses it with ENOTDIR.
		next, err := unix.Openat(fd, name, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		_ = unix.Close(fd)
		if errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("%w: %s", ErrMountPathSymlink, mountPath)
		}
		if err != nil {
			return nil, err
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), mountPath), nil
}

// checkMountResources refuses a mount the sandbox cannot hold. The sandbox disk
// is a tmpfs, so the read cache blfs fallocates up front is taken from guest
// memory: starting blfs without room for it gets the VM OOM-killed mid-request
// instead of failing this mount.
func checkMountResources(cacheRoot string, reservedMB uint64) error {
	var st unix.Statfs_t
	if err := unix.Statfs(cacheRoot, &st); err != nil {
		return fmt.Errorf("failed to check free space for the blfs cache: %w", err)
	}
	if free := st.Bavail * uint64(st.Bsize) >> 20; free < reservedMB+blfsCacheCapacityMB {
		return fmt.Errorf("%w: %d MB free, the drive cache needs %d MB", ErrInsufficientStorage, free, reservedMB+blfsCacheCapacityMB)
	}
	meminfo, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return fmt.Errorf("failed to check available memory: %w", err)
	}
	avail, err := parseMemAvailableMB(meminfo)
	if err != nil {
		return fmt.Errorf("failed to check available memory: %w", err)
	}
	if need := reservedMB + blfsCacheCapacityMB + blfsMemoryHeadroomMB; avail < need {
		return fmt.Errorf("%w: %d MB available, a drive mount needs %d MB", ErrInsufficientMemory, avail, need)
	}
	return nil
}
