package filesystem

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
)

// virtioFSMountCache caches detected virtiofs mount points to avoid
// repeated /proc/mounts reads. Refreshed periodically.
var (
	virtioFSMounts     []string
	virtioFSMountsOnce sync.Once
	virtioFSMountsMu   sync.RWMutex
)

// refreshVirtioFSMounts reads /proc/mounts and caches all virtiofs mount points.
func refreshVirtioFSMounts() {
	mounts, err := parseVirtioFSMountsFromProc()
	if err != nil {
		logrus.WithError(err).Warn("[virtiofs] failed to read /proc/mounts")
		return
	}
	virtioFSMountsMu.Lock()
	virtioFSMounts = mounts
	virtioFSMountsMu.Unlock()

	if len(mounts) > 0 {
		logrus.Infof("[virtiofs] detected virtiofs mount points: %v", mounts)
	}
}

// parseVirtioFSMountsFromProc parses /proc/mounts for virtiofs entries.
func parseVirtioFSMountsFromProc() ([]string, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var mounts []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[2] == "virtiofs" {
			mounts = append(mounts, fields[1])
		}
	}
	return mounts, scanner.Err()
}

// GetVirtioFSMounts returns the cached list of virtiofs mount points.
func GetVirtioFSMounts() []string {
	virtioFSMountsOnce.Do(refreshVirtioFSMounts)
	virtioFSMountsMu.RLock()
	defer virtioFSMountsMu.RUnlock()
	result := make([]string, len(virtioFSMounts))
	copy(result, virtioFSMounts)
	return result
}

// IsOnVirtioFS returns the virtiofs mount point if the given path resides
// on a virtiofs filesystem, or "" if it does not.
func IsOnVirtioFS(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	mounts := GetVirtioFSMounts()
	// Return the longest matching mount point (most specific).
	best := ""
	for _, mp := range mounts {
		if strings.HasPrefix(abs, mp+"/") || abs == mp {
			if len(mp) > len(best) {
				best = mp
			}
		}
	}
	return best
}

// lostFoundDir returns the path to the lost+found directory for a mount point.
func lostFoundDir(mountPoint string) string {
	return filepath.Join(mountPoint, "lost+found")
}

// getInode returns the inode number and nlink count for a path using lstat.
// Returns (inode, nlink, error). Uses Lstat to avoid following symlinks.
func getInode(path string) (uint64, uint64, error) {
	var stat syscall.Stat_t
	if err := syscall.Lstat(path, &stat); err != nil {
		return 0, 0, err
	}
	return stat.Ino, stat.Nlink, nil
}

// CleanupLostFoundForInode removes any entry in lost+found that shares
// the given inode number. This addresses the virtiofs bug where unlinked
// files with hard links are relocated to lost+found instead of being
// properly removed.
func CleanupLostFoundForInode(mountPoint string, targetIno uint64) (int, error) {
	lfDir := lostFoundDir(mountPoint)
	entries, err := os.ReadDir(lfDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil // No lost+found, nothing to clean
		}
		return 0, fmt.Errorf("failed to read lost+found: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		entryPath := filepath.Join(lfDir, entry.Name())
		ino, _, err := getInode(entryPath)
		if err != nil {
			continue // Skip entries we can't stat
		}
		if ino == targetIno {
			if err := os.Remove(entryPath); err != nil {
				logrus.WithError(err).Warnf("[virtiofs] failed to remove lost+found entry %s", entryPath)
				// Try harder: if it's read-only, make it writable first
				if os.IsPermission(err) {
					if chmodErr := os.Chmod(entryPath, 0644); chmodErr == nil {
						if retryErr := os.Remove(entryPath); retryErr == nil {
							removed++
							logrus.Infof("[virtiofs] cleaned up lost+found entry %s (inode %d) after chmod", entryPath, targetIno)
						}
					}
				}
			} else {
				removed++
				logrus.Infof("[virtiofs] cleaned up lost+found entry %s (inode %d)", entryPath, targetIno)
			}
		}
	}
	return removed, nil
}

// CleanupLostFound removes all entries in the lost+found directory of the
// given virtiofs mount point. Returns the number of entries removed.
func CleanupLostFound(mountPoint string) (int, error) {
	lfDir := lostFoundDir(mountPoint)
	entries, err := os.ReadDir(lfDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to read lost+found: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		entryPath := filepath.Join(lfDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Only clean up regular files (not directories, symlinks, etc.)
		if !info.Mode().IsRegular() {
			continue
		}

		if err := os.Remove(entryPath); err != nil {
			// Try with chmod if permission denied (pack files are often 0444)
			if os.IsPermission(err) {
				if chmodErr := os.Chmod(entryPath, 0644); chmodErr == nil {
					if retryErr := os.Remove(entryPath); retryErr == nil {
						removed++
						continue
					}
				}
			}
			logrus.WithError(err).Debugf("[virtiofs] failed to remove lost+found entry %s", entryPath)
		} else {
			removed++
		}
	}

	if removed > 0 {
		logrus.Infof("[virtiofs] cleaned up %d orphaned entries from %s", removed, lfDir)
	}
	return removed, nil
}

// VirtioFSAwareUnlink removes a file and handles virtiofs-specific quirks:
//   - Before removal, records the inode number and link count
//   - After removal, if the file had hard links (nlink > 1), cleans up any
//     orphaned entry that may appear in lost+found due to the virtiofs nlink bug
//
// Falls back to standard os.Remove() for non-virtiofs paths.
func VirtioFSAwareUnlink(path string) error {
	mountPoint := IsOnVirtioFS(path)
	if mountPoint == "" {
		// Not on virtiofs — use standard removal
		return os.Remove(path)
	}

	// Record inode info before deletion
	ino, nlink, statErr := getInode(path)

	// Perform the actual removal
	if err := os.Remove(path); err != nil {
		return err
	}

	// If the file had hard links, clean up the virtiofs lost+found mess
	if statErr == nil && nlink > 1 {
		cleaned, err := CleanupLostFoundForInode(mountPoint, ino)
		if err != nil {
			logrus.WithError(err).Debugf("[virtiofs] lost+found cleanup failed for inode %d", ino)
		} else if cleaned > 0 {
			logrus.Debugf("[virtiofs] cleaned %d lost+found entries after unlinking %s (inode %d, was nlink=%d)", cleaned, path, ino, nlink)
		}
	}

	return nil
}

// VirtioFSAwareRemoveAll removes a path recursively and handles virtiofs quirks.
// After removal, cleans up any orphaned lost+found entries on the mount.
func VirtioFSAwareRemoveAll(path string) error {
	mountPoint := IsOnVirtioFS(path)

	// Use standard RemoveAll — it handles permission issues internally
	if err := os.RemoveAll(path); err != nil {
		return err
	}

	// If on virtiofs, do a full lost+found sweep since RemoveAll may have
	// unlinked many files with hard links
	if mountPoint != "" {
		if cleaned, err := CleanupLostFound(mountPoint); err != nil {
			logrus.WithError(err).Debug("[virtiofs] lost+found cleanup failed after RemoveAll")
		} else if cleaned > 0 {
			logrus.Debugf("[virtiofs] cleaned %d lost+found entries after RemoveAll of %s", cleaned, path)
		}
	}

	return nil
}

// StartLostFoundCleaner starts a background goroutine that periodically
// cleans up lost+found directories on all virtiofs mounts. This handles
// orphaned files created by direct syscalls (e.g. git clone) that bypass
// the sandbox API.
func StartLostFoundCleaner(interval time.Duration, stop <-chan struct{}) {
	// Ensure mounts are detected
	_ = GetVirtioFSMounts()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				// Refresh mount list periodically in case volumes are mounted later
				refreshVirtioFSMounts()

				mounts := GetVirtioFSMounts()
				for _, mp := range mounts {
					if cleaned, err := CleanupLostFound(mp); err != nil {
						logrus.WithError(err).Debugf("[virtiofs] periodic cleanup failed for %s", mp)
					} else if cleaned > 0 {
						logrus.Infof("[virtiofs] periodic cleanup: removed %d orphaned entries from %s/lost+found", cleaned, mp)
					}
				}
			case <-stop:
				logrus.Info("[virtiofs] lost+found cleaner stopped")
				return
			}
		}
	}()
}

// ConfigureGitForVirtioFS sets git configuration to work around the
// virtiofs hard link bug. Called once at startup.
// Uses `git config` commands to non-destructively merge settings
// instead of overwriting existing gitconfig files.
func ConfigureGitForVirtioFS() {
	mounts := GetVirtioFSMounts()
	if len(mounts) == 0 {
		return
	}

	logrus.Info("[virtiofs] configuring git to work around virtiofs hard link bug")

	// Use git config --system to non-destructively set the option.
	// This avoids clobbering any existing system or user git configuration.
	// core.supportsAtomicFileCreation=false causes git to use rename()
	// instead of link()+unlink() for atomic file operations, avoiding
	// the virtiofs nlink bug entirely.
	if out, err := exec.Command("git", "config", "--system", "core.supportsAtomicFileCreation", "false").CombinedOutput(); err != nil {
		logrus.WithError(err).Debugf("[virtiofs] failed to set system gitconfig: %s", strings.TrimSpace(string(out)))
		// Fall back to user-level config
		if out, err := exec.Command("git", "config", "--global", "core.supportsAtomicFileCreation", "false").CombinedOutput(); err != nil {
			logrus.WithError(err).Warnf("[virtiofs] failed to set global gitconfig: %s", strings.TrimSpace(string(out)))
		} else {
			logrus.Info("[virtiofs] set core.supportsAtomicFileCreation=false in ~/.gitconfig")
		}
	} else {
		logrus.Info("[virtiofs] set core.supportsAtomicFileCreation=false in /etc/gitconfig")
	}
}
