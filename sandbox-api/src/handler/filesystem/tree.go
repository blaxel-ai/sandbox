package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
)

// TreeOptions selects what a tree read returns. The zero value lists the
// directory's direct children, exactly like ListDirectory.
type TreeOptions struct {
	// Recursive lists every file and directory below the root, not only its
	// direct children.
	Recursive bool
	// Content includes the content of each regular file (symlinks are followed).
	Content bool
	// Patterns keeps only files whose base name matches one of these globs.
	Patterns []string
	// ExcludeDirs skips directories with these base names and everything below.
	ExcludeDirs []string
	// ExcludeHidden skips entries whose name starts with a dot.
	ExcludeHidden bool
	// MaxFiles fails the read when more files match. Zero means no limit.
	MaxFiles int
	// MaxBytes fails the read when the files to return hold more bytes.
	// Only applies with Content. Zero means no limit.
	MaxBytes int64
}

// ErrTreeLimit reports a tree read that exceeds MaxFiles or MaxBytes.
var ErrTreeLimit = errors.New("tree read limit exceeded")

// ReadTree lists the directory at path, with options, as the workload identity.
// It returns either the whole result or an error, never a partial tree.
func (f *Filesystem) ReadTree(path string, opts TreeOptions) (*Directory, error) {
	var dir *Directory
	err := identity.Do(func() error {
		var err error
		dir, err = f.readTree(path, opts)
		return err
	})
	return dir, err
}

func (f *Filesystem) readTree(path string, opts TreeOptions) (*Directory, error) {
	absRoot, err := f.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}
	displayRoot := f.ResolveDisplayPath(path)
	dir := NewDirectory(displayRoot)
	dir.Recursive = opts.Recursive

	excluded := make(map[string]bool, len(opts.ExcludeDirs))
	for _, name := range opts.ExcludeDirs {
		if name != "" {
			excluded[name] = true
		}
	}
	names := ownerNames{users: map[uint32]string{}, groups: map[uint32]string{}}
	var contentPaths []string
	var totalBytes int64

	err = filepath.WalkDir(absRoot, func(absPath string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if absPath == absRoot {
			return nil
		}
		name := entry.Name()
		if opts.ExcludeHidden && name != "" && name[0] == '.' {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(absRoot, absPath)
		if err != nil {
			return err
		}
		displayPath := filepath.Join(displayRoot, rel)

		if entry.IsDir() {
			if excluded[name] {
				return filepath.SkipDir
			}
			dir.AddSubdirectory(&Subdirectory{Path: displayPath, Name: name})
			if !opts.Recursive {
				return filepath.SkipDir
			}
			return nil
		}

		if len(opts.Patterns) > 0 && !matchesAny(opts.Patterns, name) {
			return nil
		}
		if opts.MaxFiles > 0 && len(dir.Files) >= opts.MaxFiles {
			return fmt.Errorf("%w: more than %d files match under %s", ErrTreeLimit, opts.MaxFiles, displayRoot)
		}

		// Lstat: a symlink is listed as itself, like a directory listing does.
		info, err := entry.Info()
		if err != nil {
			return err
		}
		owner, group := names.lookup(info)
		file := &File{
			Path:         displayPath,
			Name:         name,
			Permissions:  fmt.Sprintf("%o", info.Mode()),
			Size:         info.Size(),
			LastModified: info.ModTime(),
			Owner:        owner,
			Group:        group,
		}
		dir.AddFile(file)

		if opts.Content {
			// Follow symlinks, but only read regular files: opening a FIFO or a
			// device could block or never end.
			target, err := os.Stat(absPath)
			if err == nil && target.Mode().IsRegular() {
				totalBytes += target.Size()
				if opts.MaxBytes > 0 && totalBytes > opts.MaxBytes {
					return fmt.Errorf("%w: files under %s hold more than %d bytes", ErrTreeLimit, displayRoot, opts.MaxBytes)
				}
				contentPaths = append(contentPaths, absPath)
			} else {
				contentPaths = append(contentPaths, "")
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Read contents only once the whole selection is known to fit the limits.
	if opts.Content {
		for i, absPath := range contentPaths {
			if absPath == "" {
				continue
			}
			data, err := os.ReadFile(absPath)
			if err != nil {
				return nil, err
			}
			content := string(data)
			dir.Files[i].Content = &content
		}
	}
	return dir, nil
}

func matchesAny(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if match, _ := filepath.Match(pattern, name); match {
			return true
		}
	}
	return false
}

// ownerNames resolves owner and group names once per id for the whole read,
// instead of once per file.
type ownerNames struct {
	users  map[uint32]string
	groups map[uint32]string
}

func (n ownerNames) lookup(info os.FileInfo) (string, string) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", ""
	}
	uid, gid := uint32(stat.Uid), uint32(stat.Gid)
	owner, ok := n.users[uid]
	if !ok {
		owner = strconv.FormatUint(uint64(uid), 10)
		if u, err := user.LookupId(owner); err == nil {
			owner = u.Username
		}
		n.users[uid] = owner
	}
	group, ok := n.groups[gid]
	if !ok {
		group = strconv.FormatUint(uint64(gid), 10)
		if g, err := user.LookupGroupId(group); err == nil {
			group = g.Name
		}
		n.groups[gid] = group
	}
	return owner, group
}
