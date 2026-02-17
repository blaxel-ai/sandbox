package drive

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// MountInfo contains information about a mounted drive
type MountInfo struct {
	DriveName string
	MountPath string
	DrivePath string
}

// ListMounts returns a list of all currently mounted drives managed by blfs
func ListMounts() ([]MountInfo, error) {
	mounts := []MountInfo{}

	// Parse /proc/mounts to find blfs FUSE mounts
	file, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/mounts: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Check if this is a FUSE mount from blfs
		// Format: seaweedfs:{source} {mount_point} fuse {options} 0 0
		// Example: seaweedfs:/buckets/agd-myname-ws123/subfolder /mnt/data fuse rw,nosuid,nodev,relatime,user_id=0,group_id=0 0 0
		source := fields[0]      // e.g., "seaweedfs:/buckets/agd-myname-ws123/subfolder"
		mountPath := fields[1]   // e.g., "/mnt/data"
		fsType := fields[2]

		// Only check FUSE mounts with seaweedfs source
		if !strings.HasPrefix(fsType, "fuse") {
			continue
		}

		// Parse the source to extract drive info
		// Expected format: seaweedfs:/buckets/{infrastructureId}{drivePath}
		if !strings.HasPrefix(source, "seaweedfs:") {
			continue
		}

		sourcePath := strings.TrimPrefix(source, "seaweedfs:")
		if !strings.HasPrefix(sourcePath, "/buckets/") {
			continue
		}

		// Extract infrastructure ID and drive path
		pathAfterBuckets := strings.TrimPrefix(sourcePath, "/buckets/")
		parts := strings.SplitN(pathAfterBuckets, "/", 2)
		
		infrastructureId := parts[0]
		drivePath := "/"
		if len(parts) > 1 {
			drivePath = "/" + parts[1]
		}

		// Try to resolve drive name from infrastructure ID
		// Look through environment variables for BL_DRIVE_*_NAME matching this infrastructure ID
		driveName := resolveDriveName(infrastructureId)
		if driveName == "" {
			driveName = infrastructureId // Fallback to infrastructure ID
		}

		mounts = append(mounts, MountInfo{
			DriveName: driveName,
			MountPath: mountPath,
			DrivePath: drivePath,
		})
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading /proc/mounts: %w", err)
	}

	logrus.WithField("count", len(mounts)).Debug("Listed mounted drives")
	return mounts, nil
}

// resolveDriveName tries to find the drive name from environment variables
// by looking for BL_DRIVE_*_NAME that matches the infrastructure ID
func resolveDriveName(infrastructureId string) string {
	environ := os.Environ()
	for _, env := range environ {
		if !strings.HasPrefix(env, "BL_DRIVE_") {
			continue
		}

		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			continue
		}

		key := parts[0]
		value := parts[1]

		// Check if this is a _NAME env var and if the value matches our infrastructure ID
		if strings.HasSuffix(key, "_NAME") && value == infrastructureId {
			// Extract the drive name from the env key
			// BL_DRIVE_{UPPER_NAME}_NAME -> {UPPER_NAME}
			drivePart := strings.TrimPrefix(key, "BL_DRIVE_")
			drivePart = strings.TrimSuffix(drivePart, "_NAME")
			
			// Convert back from UPPER_CASE to lower-case with dashes
			driveName := strings.ToLower(strings.ReplaceAll(drivePart, "_", "-"))
			return driveName
		}
	}

	return ""
}
