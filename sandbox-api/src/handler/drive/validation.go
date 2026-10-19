package drive

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// driveNameRegex allows only safe characters for drive names: alphanumeric, hyphen, underscore.
// Prevents injection into infrastructure IDs and command arguments.
var driveNameRegex = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

const (
	maxDriveNameLen = 128
	maxMountPathLen = 512
)

// ValidateDriveName ensures driveName is safe to use in infrastructure IDs and CLI args.
func ValidateDriveName(driveName string) error {
	if driveName == "" {
		return fmt.Errorf("drive name is required")
	}
	if len(driveName) > maxDriveNameLen {
		return fmt.Errorf("drive name too long (max %d)", maxDriveNameLen)
	}
	if !driveNameRegex.MatchString(driveName) {
		return fmt.Errorf("drive name must contain only letters, numbers, hyphens and underscores")
	}
	return nil
}

// NormalizeMountPath ensures mountPath has a leading / (added if missing).
func NormalizeMountPath(mountPath string) string {
	if mountPath == "" || strings.HasPrefix(mountPath, "/") {
		return mountPath
	}
	return "/" + mountPath
}

// ValidateMountPath ensures mountPath is non-empty, has no path traversal, and length is within limit.
// Call NormalizeMountPath first if the path may lack a leading /.
func ValidateMountPath(mountPath string) error {
	if mountPath == "" {
		return fmt.Errorf("mount path is required")
	}
	if len(mountPath) > maxMountPathLen {
		return fmt.Errorf("mount path too long (max %d)", maxMountPathLen)
	}
	if strings.Contains(mountPath, "..") {
		return fmt.Errorf("mount path must not contain '..'")
	}
	if filepath.Clean(mountPath) == "/" {
		return fmt.Errorf("mount path must not be the sandbox root")
	}
	return nil
}

// ValidateMountTarget refuses mounting a drive at, inside or above the blfs
// cache root: that would put the cache on the drive instead of the sandbox
// disk. Unmount does not apply it, so such a mount can still be removed.
func ValidateMountTarget(mountPath string) error {
	clean := filepath.Clean(mountPath)
	if clean == BlfsCacheRoot || strings.HasPrefix(clean, BlfsCacheRoot+"/") || strings.HasPrefix(BlfsCacheRoot, strings.TrimSuffix(clean, "/")+"/") {
		return fmt.Errorf("mount path must not contain or be inside %s", BlfsCacheRoot)
	}
	return nil
}
