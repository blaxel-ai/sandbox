package archive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

// procFdAvailable reports whether a descriptor can be turned back into a path.
// It is how a directory held open is written into, and it is Linux's: on
// anything else the restore falls back to plain paths, which is what the
// sandbox never runs on.
var procFdAvailable = sync.OnceValue(func() bool {
	_, err := os.Stat("/proc/self/fd")
	return err == nil
})

// parentDir is the directory an archive member is restored into, held open for
// as long as the member is being written.
//
// Checking the path and then writing to it is not enough while the sandbox
// answers: the restore runs as root, a terminal is served all along it, and the
// workload's user only has to turn one directory of the path into a symlink
// between the check and the write to have that root write land wherever it
// likes - inside the platform's own trees, which the excludes exist to keep an
// archive out of. Opening every component with O_NOFOLLOW and writing relative
// to the descriptor that came out of it takes the path out of the race: the
// descriptor names the directory that was checked, whatever the name now points
// at.
type parentDir struct {
	dir *os.File
	// base is the member's own name inside dir.
	base string
	// logical is the path as it reads, for messages and for the platforms
	// where a descriptor cannot be written through.
	logical string
}

// path is the member, reached through the directory this holds open.
func (p *parentDir) path() string {
	if !procFdAvailable() {
		return p.logical
	}
	return filepath.Join(p.dirPath(), p.base)
}

// dirPath is the directory itself, for whoever has to create a file beside the
// member rather than write the member.
func (p *parentDir) dirPath() string {
	if !procFdAvailable() {
		return filepath.Dir(p.logical)
	}
	return filepath.Join("/proc/self/fd", strconv.Itoa(int(p.dir.Fd())))
}

func (p *parentDir) Close() {
	_ = p.dir.Close()
}

// openParent opens the directory an archive member belongs in, one component at
// a time and never through a symlink. With create it makes the components the
// filesystem does not have, and reports whether it made any: a restore that
// created a directory has changed the filesystem even if it then failed.
func openParent(root, name string, create bool) (*parentDir, bool, error) {
	clean, err := memberName(name)
	if err != nil {
		return nil, false, err
	}

	dir, err := os.OpenFile(root, os.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, false, fmt.Errorf("failed to open the restore root %s: %w", root, err)
	}

	created := false
	parts := strings.Split(clean, "/")
	logical := root
	for _, part := range parts[:len(parts)-1] {
		logical = filepath.Join(logical, part)

		next, err := openDirAt(dir, part, logical)
		if errors.Is(err, os.ErrNotExist) && create {
			if err := unix.Mkdirat(int(dir.Fd()), part, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
				_ = dir.Close()
				return nil, created, fmt.Errorf("failed to create %s: %w", logical, err)
			}
			created = true
			next, err = openDirAt(dir, part, logical)
		}
		_ = dir.Close()
		if err != nil {
			return nil, created, err
		}
		dir = next
	}

	return &parentDir{dir: dir, base: parts[len(parts)-1], logical: filepath.Join(logical, parts[len(parts)-1])}, created, nil
}

// openDirAt opens one component of a member's path, refusing a symlink and
// anything that is not a directory: both mean the path the archive names is not
// the path this would write to.
func openDirAt(dir *os.File, part, logical string) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return nil, fmt.Errorf("archive member would be restored through %s, which is not a directory", logical)
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), logical), nil
}
