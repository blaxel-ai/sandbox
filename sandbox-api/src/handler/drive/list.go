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

	// First pass: log all mounts for debugging
	scanner := bufio.NewScanner(file)
	allLines := []string{}
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	logrus.WithField("mount_count", len(allLines)).Debug("Total mounts in /proc/mounts")
	
	// Reset to beginning of file
	file.Seek(0, 0)
	scanner = bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		// Check if this is a FUSE mount from blfs
		// Format: {filer_ip}:{port}:/buckets/{infrastructureId}{drivePath} {mount_point} fuse.seaweedfs {options} 0 0
		// Example: 172.16.37.66:8080:/buckets/agd-my-super-drive-hydpwa/ /mnt/test fuse.seaweedfs rw,nosuid,nodev,relatime,user_id=0,group_id=0 0 0
		source := fields[0]      // e.g., "172.16.37.66:8080:/buckets/agd-my-super-drive-hydpwa/"
		mountPath := fields[1]   // e.g., "/mnt/test"
		fsType := fields[2]

		// Only check fuse.seaweedfs mounts
		if fsType != "fuse.seaweedfs" {
			continue
		}

		// Parse the source to extract drive info
		// Expected format: {filer_ip}:{port}:/buckets/{infrastructureId}{drivePath}
		// We need to find the part after the last ":" which should be the path
		lastColonIdx := strings.LastIndex(source, ":")
		if lastColonIdx == -1 {
			continue
		}

		sourcePath := source[lastColonIdx+1:]
		if !strings.HasPrefix(sourcePath, "/buckets/") {
			continue
		}

		// Extract infrastructure ID and drive path
		pathAfterBuckets := strings.TrimPrefix(sourcePath, "/buckets/")
		// Remove trailing slash for consistent parsing
		pathAfterBuckets = strings.TrimSuffix(pathAfterBuckets, "/")
		parts := strings.SplitN(pathAfterBuckets, "/", 2)
		
		infrastructureId := parts[0]
		drivePath := "/"
		if len(parts) > 1 && parts[1] != "" {
			drivePath = "/" + parts[1]
		}

		// Try to resolve drive name from infrastructure ID
		// Infrastructure ID format: agd-{driveName}-{workspaceID}
		driveName := extractDriveNameFromInfraId(infrastructureId)
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

// extractDriveNameFromInfraId extracts the drive name from the infrastructure ID
// Infrastructure ID format: agd-{driveName}-{workspaceID}
// Example: agd-my-super-drive-hydpwa -> my-super-drive
func extractDriveNameFromInfraId(infrastructureId string) string {
	// Remove the agd- prefix
	if !strings.HasPrefix(infrastructureId, "agd-") {
		return ""
	}
	
	withoutPrefix := strings.TrimPrefix(infrastructureId, "agd-")
	
	// Get workspace ID from environment
	workspaceID := strings.ToLower(os.Getenv("BL_WORKSPACE_ID"))
	if workspaceID == "" {
		// If we can't get workspace ID, return the whole thing without prefix
		return withoutPrefix
	}
	
	// Remove the workspace ID suffix
	if strings.HasSuffix(withoutPrefix, "-"+workspaceID) {
		driveName := strings.TrimSuffix(withoutPrefix, "-"+workspaceID)
		return driveName
	}
	
	// Fallback to returning without prefix
	return withoutPrefix
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
