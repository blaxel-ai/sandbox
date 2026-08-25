//go:build linux

package archive

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// DefaultImageDevice is where the pristine EROFS image is attached. mk3.1
// attaches it as the first virtio drive; mk3.0 boots Unikraft and exposes the
// same image as a ROM, so the device has to be overridden there.
const DefaultImageDevice = "/dev/vda"

// mountImage mounts the pristine image read-only at mountpoint. Mounting the
// image a second time is safe: it is read-only for the guest either way, and it
// is already the lower layer of the root overlay.
func mountImage(device, mountpoint string) error {
	if _, err := os.Stat(device); err != nil {
		return fmt.Errorf("image device %s is not available: %w", device, err)
	}
	if err := os.MkdirAll(mountpoint, 0o755); err != nil {
		return fmt.Errorf("failed to create %s: %w", mountpoint, err)
	}
	if err := syscall.Mount(device, mountpoint, "erofs", syscall.MS_RDONLY, ""); err != nil {
		return fmt.Errorf("failed to mount %s as erofs on %s: %w", device, mountpoint, err)
	}
	return nil
}

// unmountImage releases the comparison mount.
func unmountImage(mountpoint string) error {
	if err := syscall.Unmount(mountpoint, 0); err != nil {
		return fmt.Errorf("failed to unmount %s: %w", mountpoint, err)
	}
	return nil
}

// setRootReadOnly flips the read-only flag of the root mount, so every write to
// it fails with EROFS whoever attempts it — a terminal session, a process that
// survived the stop, a request that was already in flight.
//
// The flag is set on the mount rather than on the superblock (MS_BIND, as in
// `mount -o remount,bind,ro /`): a read-only superblock is refused with EBUSY
// while any file is open for writing, which the API's own log files always are,
// and it would also revoke the writes of those already open descriptors.
func setRootReadOnly(root string, readOnly bool) error {
	flags := uintptr(syscall.MS_BIND | syscall.MS_REMOUNT)
	if readOnly {
		flags |= syscall.MS_RDONLY
	}
	if err := syscall.Mount("", root, "", flags, ""); err != nil {
		return fmt.Errorf("failed to remount %s (readOnly=%t): %w", root, readOnly, err)
	}
	return nil
}

// rootReadOnly reports whether root is currently mounted read-only, which is
// the only durable trace an interrupted archive leaves: the quiesce state lives
// in this process's memory and the mount outlives it.
func rootReadOnly(root string) (bool, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(root, &stat); err != nil {
		return false, fmt.Errorf("failed to read the mount flags of %s: %w", root, err)
	}
	return stat.Flags&unix.ST_RDONLY != 0, nil
}

// syncFilesystem flushes pending writes before the filesystem is read.
func syncFilesystem() {
	syscall.Sync()
}

// mounted reports whether mountpoint holds a mount, so an export can reuse one
// left behind by a previous run instead of failing.
func mounted(mountpoint string) bool {
	// The root is always a mount, and it is the one case the comparison below
	// cannot see: / and /.. are the same directory, hence the same device.
	if filepath.Clean(mountpoint) == "/" {
		return true
	}
	info, err := os.Stat(mountpoint)
	if err != nil {
		return false
	}
	parent, err := os.Stat(mountpoint + "/..")
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	parentStat, parentOK := parent.Sys().(*syscall.Stat_t)
	if !ok || !parentOK {
		return false
	}
	return stat.Dev != parentStat.Dev
}
