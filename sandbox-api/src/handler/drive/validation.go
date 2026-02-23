package drive

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// AllowedMountBase is the only directory prefix under which drives may be mounted.
// This prevents path traversal (e.g. mounting over /etc, /root, /var/run).
const AllowedMountBase = "/mnt"

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

// ValidateMountPath ensures mountPath is under AllowedMountBase and contains no path traversal.
func ValidateMountPath(mountPath string) error {
	if mountPath == "" {
		return fmt.Errorf("mount path is required")
	}
	if len(mountPath) > maxMountPathLen {
		return fmt.Errorf("mount path too long (max %d)", maxMountPathLen)
	}
	clean := filepath.Clean(mountPath)
	if strings.Contains(clean, "..") {
		return fmt.Errorf("mount path must not contain '..'")
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return fmt.Errorf("invalid mount path: %w", err)
	}
	base := filepath.Clean(AllowedMountBase)
	if abs != base && !strings.HasPrefix(abs, base+string(filepath.Separator)) {
		return fmt.Errorf("mount path must be under %s", AllowedMountBase)
	}
	return nil
}
