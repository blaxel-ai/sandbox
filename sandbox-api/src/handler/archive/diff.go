package archive

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

// ChangeKind classifies a path with respect to the pristine image.
type ChangeKind string

const (
	// ChangeAdded is a path the image does not have.
	ChangeAdded ChangeKind = "added"
	// ChangeModified is a path the image has with different content or metadata.
	ChangeModified ChangeKind = "modified"
	// ChangeDeleted is a path the image has and the live filesystem does not.
	// Overlay records those as whiteouts in the upper layer, which the merged
	// root does not expose, so they travel as an explicit list instead of as
	// archive entries.
	ChangeDeleted ChangeKind = "deleted"
)

// Change is one difference between the live root and the pristine image.
type Change struct {
	// Path is relative to the root, without a leading slash.
	Path string     `json:"path" binding:"required" example:"usr/bin/curl"`
	Kind ChangeKind `json:"kind" binding:"required" example:"added"`
	// Size is the file content size in bytes, 0 for anything but a regular file.
	Size int64 `json:"size" example:"256216"`

	// info is the live entry, kept so the tar pass does not lstat again.
	info os.FileInfo
}

// DefaultExcludes are paths never archived, relative to the root and matched on
// the path itself or any path below it.
//
// Other mounts are pruned by the walk itself, which reads them from the mount
// table; these are named anyway because a sandbox that lost a mount must still
// never archive the runtime directories, and because the first group's content
// is real but belongs to this specific sandbox rather than to the workload: the
// network configuration and identity the host injects, and this API's own
// runtime state. Restoring those over a fresh sandbox would hand it the
// archived sandbox's DNS resolver, hostname and credentials.
var DefaultExcludes = []string{
	"proc",
	"sys",
	"dev",
	"run",
	"tmp",
	"mnt",
	// The unikraft filer mountpoint on mk3.0.
	"uk",
	"etc/resolv.conf",
	"etc/hostname",
	"etc/hosts",
	"run/secrets",
	"var/run/secrets",
	"bl",
	// This API's own logs and state: process output belongs to the sandbox being
	// archived, not to the one restoring it.
	"var/log/sandbox-api",
	// The marker saying this sandbox already imported an archive: it describes
	// this sandbox's history, and carrying it into the next archive would
	// overwrite the marker of whichever sandbox restores that one.
	strings.TrimPrefix(DefaultImportMarker, "/"),
}

// scanner walks the live root and the pristine image and reports the difference.
type scanner struct {
	root     string
	lower    string
	excludes []string

	// mounts are the mountpoints below the root, pruned from both walks: their
	// content belongs to another filesystem (/proc, /sys, an attached drive, the
	// image mounted for this comparison), not to the sandbox's own writes.
	//
	// The device of an entry cannot be used for this: overlayfs reports the
	// device of the layer a file actually lives on, so on a sandbox without
	// inode mapping every file written since boot looks like it belongs to
	// another filesystem - which silently emptied the archive of exactly the
	// changes it exists to carry.
	mounts map[string]bool
}

// mountPointsBelow reads the mount table and returns the mountpoints strictly
// below path, cleaned and absolute. A system without a mount table (a test, a
// non-linux build) reports none, which is correct for a plain directory tree.
func mountPointsBelow(path string) (map[string]bool, error) {
	info, err := os.ReadFile("/proc/self/mountinfo")
	if os.IsNotExist(err) {
		return map[string]bool{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read the mount table: %w", err)
	}
	return parseMountPoints(info, path), nil
}

// parseMountPoints reads mountinfo content and keeps the mountpoints strictly
// below path.
func parseMountPoints(info []byte, path string) map[string]bool {
	mounts := map[string]bool{}

	prefix := filepath.Clean(path)
	for _, line := range strings.Split(string(info), "\n") {
		// mountinfo: id parent major:minor rootOfMount mountPoint ...
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		// Mountpoints are recorded with the usual escapes for spaces and tabs.
		point := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(fields[4])
		point = filepath.Clean(point)
		if point == prefix {
			continue
		}
		if rel, err := filepath.Rel(prefix, point); err == nil && rel != "." && !strings.HasPrefix(rel, "..") {
			mounts[point] = true
		}
	}
	return mounts
}

// Diff compares the live root against the pristine image mounted at lower and
// returns the changes, sorted by path. Content is not read unless metadata alone
// cannot decide, so an unchanged image costs one lstat per path.
func Diff(root, lower string, excludes []string) ([]Change, error) {
	mounts, err := mountPointsBelow("/")
	if err != nil {
		return nil, err
	}

	s := &scanner{
		root:     filepath.Clean(root),
		lower:    filepath.Clean(lower),
		excludes: excludes,
		mounts:   mounts,
	}

	changes, err := s.scanLive()
	if err != nil {
		return nil, err
	}
	deleted, err := s.scanDeleted()
	if err != nil {
		return nil, err
	}
	changes = append(changes, deleted...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}

// excluded reports whether rel is an excluded path or below one.
func (s *scanner) excluded(rel string) bool {
	return excludedPath(rel, s.excludes)
}

// excludedPath reports whether rel is one of the excluded paths or below one.
func excludedPath(rel string, excludes []string) bool {
	for _, exclude := range excludes {
		exclude = strings.Trim(exclude, "/")
		if exclude == "" {
			continue
		}
		if rel == exclude || strings.HasPrefix(rel, exclude+"/") {
			return true
		}
	}
	return false
}

// scanLive walks the live root and reports added and modified paths.
func (s *scanner) scanLive() ([]Change, error) {
	var changes []Change

	err := filepath.Walk(s.root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A path that vanished mid-walk is not an error: the sandbox is
			// quiesced, but the kernel and this API still have their own
			// short-lived files.
			if os.IsNotExist(err) {
				return nil
			}
			if os.IsPermission(err) {
				return nil
			}
			return err
		}

		rel, err := filepath.Rel(s.root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if s.excluded(rel) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		// Another filesystem mounted below the root is not overlay content.
		if s.mounts[filepath.Clean(path)] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		kind, err := s.classify(rel, info)
		if err != nil {
			return err
		}
		if kind == "" {
			return nil
		}

		change := Change{Path: rel, Kind: kind, info: info}
		if info.Mode().IsRegular() {
			change.Size = info.Size()
		}
		changes = append(changes, change)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk %s: %w", s.root, err)
	}
	return changes, nil
}

// classify decides how a live entry differs from the image. An empty kind means
// the image has the same entry, so it does not belong in the archive.
func (s *scanner) classify(rel string, live os.FileInfo) (ChangeKind, error) {
	lowerInfo, err := os.Lstat(filepath.Join(s.lower, rel))
	if os.IsNotExist(err) {
		return ChangeAdded, nil
	}
	if err != nil {
		return "", fmt.Errorf("failed to stat %s in image: %w", rel, err)
	}

	if live.Mode().Type() != lowerInfo.Mode().Type() {
		return ChangeModified, nil
	}
	if live.Mode().Perm() != lowerInfo.Mode().Perm() {
		return ChangeModified, nil
	}
	liveStat, liveOK := live.Sys().(*syscall.Stat_t)
	lowerStat, lowerOK := lowerInfo.Sys().(*syscall.Stat_t)
	if liveOK && lowerOK && (liveStat.Uid != lowerStat.Uid || liveStat.Gid != lowerStat.Gid) {
		return ChangeModified, nil
	}

	switch {
	case live.Mode()&os.ModeSymlink != 0:
		liveTarget, err := os.Readlink(filepath.Join(s.root, rel))
		if err != nil {
			return "", fmt.Errorf("failed to read symlink %s: %w", rel, err)
		}
		lowerTarget, err := os.Readlink(filepath.Join(s.lower, rel))
		if err != nil {
			return "", fmt.Errorf("failed to read symlink %s in image: %w", rel, err)
		}
		if liveTarget != lowerTarget {
			return ChangeModified, nil
		}
		return "", nil

	case live.IsDir():
		// Same type, permissions and ownership: the directory itself carries no
		// other content, and its entries are visited on their own.
		return "", nil

	case live.Mode().IsRegular():
		if live.Size() != lowerInfo.Size() {
			return ChangeModified, nil
		}
		if !live.ModTime().Equal(lowerInfo.ModTime()) {
			return ChangeModified, nil
		}
		// Metadata matches. If the live file is the image's own inode, nothing
		// ever wrote to it. A different inode means overlay copied it up, which
		// happens on a mere open for write, so compare the content rather than
		// archive every file the workload happened to open.
		if liveOK && lowerOK && liveStat.Ino == lowerStat.Ino {
			return "", nil
		}
		same, err := sameContent(filepath.Join(s.root, rel), filepath.Join(s.lower, rel))
		if err != nil {
			return "", err
		}
		if same {
			return "", nil
		}
		return ChangeModified, nil

	default:
		// Device nodes, fifos and sockets: same type, mode and ownership is all
		// there is to compare.
		return "", nil
	}
}

// scanDeleted walks the image and reports the paths the live root no longer has.
func (s *scanner) scanDeleted() ([]Change, error) {
	var changes []Change

	err := filepath.Walk(s.lower, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if os.IsNotExist(err) || os.IsPermission(err) {
				return nil
			}
			return err
		}
		rel, err := filepath.Rel(s.lower, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// The mountpoints are live paths, so a path of the image is pruned by
		// where it would be in the live root, not by where it is in the image:
		// a directory the image has and a mount now covers is not a deletion,
		// and reporting it as one would have the restore remove it.
		live := filepath.Join(s.root, rel)
		if s.excluded(rel) || s.mounts[filepath.Clean(live)] {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if _, err := os.Lstat(live); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat %s: %w", rel, err)
		}

		changes = append(changes, Change{Path: rel, Kind: ChangeDeleted})
		// The whole subtree is gone; reporting the top of it is enough.
		if info.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk image %s: %w", s.lower, err)
	}
	return changes, nil
}

// sameContent compares two files of equal size.
func sameContent(a, b string) (bool, error) {
	hashA, err := hashFile(a)
	if err != nil {
		return false, err
	}
	hashB, err := hashFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(hashA, hashB), nil
}

func hashFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open %s: %w", path, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return hash.Sum(nil), nil
}
