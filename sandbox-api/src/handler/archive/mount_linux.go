//go:build linux

package archive

import (
	"fmt"
	"os"
	"syscall"
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

// syncFilesystem flushes pending writes before the filesystem is read.
func syncFilesystem() {
	syscall.Sync()
}

// mounted reports whether mountpoint holds a mount, so an export can reuse one
// left behind by a previous run instead of failing.
func mounted(mountpoint string) bool {
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
