package drive

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	unmountTimeout = 10 * time.Second
)

// UnmountDrive unmounts a drive from the specified mount path
func UnmountDrive(mountPath string) error {
	// Check if the path exists
	if _, err := os.Stat(mountPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mount path does not exist: %s", mountPath)
		}
		return fmt.Errorf("failed to stat mount path: %w", err)
	}

	// Check if it's actually mounted
	if !isMountPoint(mountPath) {
		return fmt.Errorf("path is not a mount point: %s", mountPath)
	}

	logrus.WithField("mount_path", mountPath).Debug("Unmounting drive")

	// Try fusermount -u first (preferred for FUSE mounts)
	err := unmountWithFusermount(mountPath)
	if err == nil {
		return nil
	}

	logrus.WithError(err).Debug("fusermount failed, trying umount")

	// Fallback to umount
	err = unmountWithUmount(mountPath)
	if err != nil {
		return fmt.Errorf("failed to unmount drive: %w", err)
	}

	return nil
}

// unmountWithFusermount uses fusermount -u to unmount a FUSE filesystem
func unmountWithFusermount(mountPath string) error {
	cmd := exec.Command("fusermount", "-u", mountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fusermount failed: %w (output: %s)", err, string(output))
	}

	// Wait for unmount to complete
	startTime := time.Now()
	for time.Since(startTime) < unmountTimeout {
		if !isMountPoint(mountPath) {
			logrus.WithField("mount_path", mountPath).Info("Drive unmounted successfully with fusermount")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for unmount to complete")
}

// unmountWithUmount uses umount command as a fallback
func unmountWithUmount(mountPath string) error {
	cmd := exec.Command("umount", mountPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("umount failed: %w (output: %s)", err, string(output))
	}

	// Wait for unmount to complete
	startTime := time.Now()
	for time.Since(startTime) < unmountTimeout {
		if !isMountPoint(mountPath) {
			logrus.WithField("mount_path", mountPath).Info("Drive unmounted successfully with umount")
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timeout waiting for unmount to complete")
}
