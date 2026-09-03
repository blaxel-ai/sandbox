//go:build linux

package archive

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// DefaultImageDevice is where the pristine EROFS image is attached on most
// sandboxes: the first virtio drive. It is only the last resort of
// detectImageDevice, which reads where the image actually is instead of
// assuming — the sandbox knows nothing of the platform it boots on.
const DefaultImageDevice = "/dev/vda"

// devDir is where the block devices are, and the only place an image device is
// looked for.
const devDir = "/dev"

// erofsSuperOffset and erofsMagic locate the signature an EROFS filesystem
// starts with, which is how a device is told to hold an image rather than
// anything else mountable.
const (
	erofsSuperOffset = 1024
	erofsMagic       = 0xE0F5E1E2
)

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

// deviceHoldsImage reports whether device holds an EROFS filesystem, which the
// pristine image is and a drive the workload attached is not: comparing the
// root against a filesystem it shares nothing with reports every path as added
// and turns the export into a copy of the whole root, streamed to a URL the
// caller chose.
func deviceHoldsImage(device string) bool {
	file, err := os.Open(device)
	if err != nil {
		return false
	}
	defer file.Close()
	signature := make([]byte, 4)
	if _, err := file.ReadAt(signature, erofsSuperOffset); err != nil {
		return false
	}
	return binary.LittleEndian.Uint32(signature) == erofsMagic
}

// detectImageDevice finds the device the pristine image is attached to, so the
// export compares the filesystem against the right thing wherever the platform
// puts the image and whatever it calls it.
//
// The image is already mounted, as the lower layer of the root overlay, so the
// mount table names the device it comes from. Should the root not be an overlay
// — a sandbox booted some other way, a test — the block devices are read
// instead, and the one carrying an EROFS filesystem is the image.
func detectImageDevice() string {
	if device := imageDeviceFromMounts(); device != "" {
		return device
	}
	if deviceHoldsImage(DefaultImageDevice) {
		return DefaultImageDevice
	}
	if device := imageDeviceFromBlockDevices(); device != "" {
		return device
	}
	// Nothing was found, so the export is about to fail; naming the usual device
	// makes it fail with "/dev/vda is not available" rather than with a blank.
	return DefaultImageDevice
}

// imageDeviceFromMounts answers detectImageDevice from the mount table.
func imageDeviceFromMounts() string {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	defer file.Close()
	device := imageDeviceFromMountinfo(file)
	if device == "" || !deviceHoldsImage(device) {
		return ""
	}
	return device
}

// imageDeviceFromMountinfo reads the image device out of a mount table.
//
// The lower layers of the root overlay are what the sandbox booted from, and
// the image is the one of them that is an EROFS mount: a drive or an image the
// workload mounted is not a layer of the root, so it cannot be mistaken for the
// image however it is mounted. When the root is not an overlay the only EROFS
// mount coming from a device answers, and an ambiguous table answers nothing.
func imageDeviceFromMountinfo(mountinfo io.Reader) string {
	type erofsMount struct{ mountpoint, device string }
	var erofsMounts []erofsMount
	var lowerDirs []string

	scanner := bufio.NewScanner(mountinfo)
	for scanner.Scan() {
		// "36 35 253:0 / /mnt/x rw,relatime - erofs /dev/vda ro": the mount point
		// is the fifth field of the left half, the filesystem type, its source and
		// its super options the three of the right half.
		line := scanner.Text()
		separator := strings.Index(line, " - ")
		if separator < 0 {
			continue
		}
		left := strings.Fields(line[:separator])
		right := strings.Fields(line[separator+len(" - "):])
		if len(left) < 5 || len(right) < 2 {
			continue
		}
		mountpoint, fstype, source := left[4], right[0], right[1]
		switch {
		case fstype == "erofs" && strings.HasPrefix(source, devDir+"/"):
			erofsMounts = append(erofsMounts, erofsMount{mountpoint, source})
		case fstype == "overlay" && filepath.Clean(mountpoint) == DefaultRoot && len(right) > 2:
			// A remount of the root leaves a second line for it, so the layers of
			// the last one are the ones in force.
			lowerDirs = overlayLowerDirs(right[2])
		}
	}

	if len(lowerDirs) > 0 {
		for _, mount := range erofsMounts {
			for _, lower := range lowerDirs {
				if underMountPoint(lower, mount.mountpoint) {
					return mount.device
				}
			}
		}
		return ""
	}
	if len(erofsMounts) == 1 {
		return erofsMounts[0].device
	}
	return ""
}

// overlayLowerDirs reads the lower layers out of the super options of an
// overlay mount, "lowerdir=/a:/b,upperdir=/c,workdir=/d".
func overlayLowerDirs(superOptions string) []string {
	for _, option := range strings.Split(superOptions, ",") {
		value, found := strings.CutPrefix(option, "lowerdir=")
		if !found {
			continue
		}
		return strings.Split(value, ":")
	}
	return nil
}

// underMountPoint reports whether path is served by the mount at mountpoint,
// which is how a lower layer names a directory inside the image mount rather
// than the mount point itself.
func underMountPoint(path, mountpoint string) bool {
	path, mountpoint = filepath.Clean(path), filepath.Clean(mountpoint)
	if path == mountpoint {
		return true
	}
	if mountpoint == DefaultRoot {
		return false
	}
	return strings.HasPrefix(path, mountpoint+"/")
}

// imageDeviceFromBlockDevices answers detectImageDevice by reading the devices
// themselves, for a sandbox whose mount table does not say where the image is.
// Only one device carries an EROFS filesystem, since a workload attaches its
// own images through loop devices, which are left out.
func imageDeviceFromBlockDevices() string {
	entries, err := os.ReadDir(devDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "loop") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		// A block device, and nothing else: reading a character device - a tty, a
		// tape - is a side effect rather than a look.
		if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
			continue
		}
		device := filepath.Join(devDir, entry.Name())
		if deviceHoldsImage(device) {
			return device
		}
	}
	return ""
}

// mountedFromImage reports whether mountpoint holds the image device itself,
// which being a mount does not say: anything the workload can mount - a drive it
// attached, an image it built - shares nothing with the root, so comparing
// against it reports the whole root as added. The mount table names both the
// filesystem and the device it comes from, and only the image device, mounted
// as erofs, is the image.
func mountedFromImage(mountpoint, device string) bool {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	defer file.Close()
	return mountHoldsImage(file, mountpoint, device)
}

// mountHoldsImage answers mountedFromImage from the mount table it is given.
func mountHoldsImage(mountinfo io.Reader, mountpoint, device string) bool {
	wanted := filepath.Clean(mountpoint)
	matched := false
	scanner := bufio.NewScanner(mountinfo)
	for scanner.Scan() {
		// "36 35 253:0 / /mnt/x rw,relatime - erofs /dev/vda ro": the mount point
		// is the fifth field of the left half, the filesystem type and its source
		// the first two of the right half.
		line := scanner.Text()
		separator := strings.Index(line, " - ")
		if separator < 0 {
			continue
		}
		left := strings.Fields(line[:separator])
		right := strings.Fields(line[separator+len(" - "):])
		if len(left) < 5 || len(right) < 2 {
			continue
		}
		// Octal escapes are how the kernel writes a path holding a space or a tab.
		// The mount points compared here hold neither, so an escaped path simply
		// does not match.
		if left[4] != wanted {
			continue
		}
		// The last mount of a path is the one seen through it, so the answer is
		// whatever the last matching line says rather than the first.
		matched = right[0] == "erofs" && right[1] == device
	}
	return matched
}
