package filesystem

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
)

// Copy recursively copies source to destination like `cp -r`, without shell
// interpretation: when destination is an existing directory (or a symlink to
// one), the copy goes inside it, named after source. Existing directories merge
// only in the default overwrite mode; with NoOverwrite every entry is created
// exclusively, so an existing final target is an os.ErrExist error.
// Traversal does not follow source symlinks.
func (f *Filesystem) Copy(source, destination string, options ...WriteOptions) error {
	return identity.Do(func() error {
		src, err := f.GetAbsolutePath(source)
		if err != nil {
			return err
		}
		dst, err := f.GetAbsolutePath(destination)
		if err != nil {
			return err
		}
		info, err := os.Lstat(src)
		if err != nil {
			return err
		}
		if destInfo, err := os.Stat(dst); err == nil && destInfo.IsDir() {
			dst = filepath.Join(dst, filepath.Base(src))
		}
		opts := writeOptions(options)
		if !info.IsDir() {
			return f.copyEntry(src, dst, info, opts)
		}

		// Refuse recursive self-copies, also when an existing ancestor of the
		// destination is a symlink into the source.
		resolvedSrc, err := filepath.EvalSymlinks(src)
		if err != nil {
			return err
		}
		resolvedDst, err := resolveCopyDestination(dst)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(resolvedSrc, resolvedDst)
		if err != nil {
			return err
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			if opts.NoOverwrite {
				// An existing destination still reports a conflict, even for
				// self-copies. This check only classifies an already-invalid
				// copy; valid copies always use exclusive creation below.
				if _, err := os.Lstat(dst); err == nil {
					return &os.PathError{Op: "copy", Path: dst, Err: os.ErrExist}
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			return fmt.Errorf("cannot copy a directory into itself: %s", destination)
		}

		return f.copyDirectory(src, dst, info, opts)
	})
}

func (f *Filesystem) copyDirectory(src, dst string, info os.FileInfo, opts WriteOptions) (result error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	// Reserve the exact destination with mkdir, not a check followed by MkdirAll.
	// Keep new directories writable while filling them, even for read-only sources.
	if err := os.Mkdir(dst, info.Mode().Perm()|0700); err != nil {
		if opts.NoOverwrite || !errors.Is(err, os.ErrExist) {
			return err
		}
		destInfo, err := os.Stat(dst)
		if err != nil {
			return err
		}
		if !destInfo.IsDir() {
			return fmt.Errorf("destination is not a directory: %s", dst)
		}
	} else {
		defer func() {
			if err := os.Chmod(dst, info.Mode().Perm()); result == nil {
				result = err
			}
		}()
	}
	directory, err := os.Open(src)
	if err != nil {
		return err
	}
	defer directory.Close()

	// Bound memory by a batch rather than reading millions of entries at once.
	for {
		entries, err := directory.ReadDir(100)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			source := filepath.Join(src, entry.Name())
			target := filepath.Join(dst, entry.Name())
			if entry.IsDir() {
				err = f.copyDirectory(source, target, info, opts)
			} else {
				err = f.copyEntry(source, target, info, opts)
			}
			if err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

func (f *Filesystem) copyEntry(src, dst string, info os.FileInfo, opts WriteOptions) error {
	if !opts.NoOverwrite {
		if destInfo, err := os.Lstat(dst); err == nil && os.SameFile(info, destInfo) {
			return fmt.Errorf("source and destination are the same file: %s", src)
		}
	}
	if info.Mode()&os.ModeSymlink != 0 {
		link, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return err
		}
		if !opts.NoOverwrite {
			if destInfo, err := os.Lstat(dst); err == nil && destInfo.IsDir() {
				return fmt.Errorf("cannot replace a directory with a symlink: %s", dst)
			}
			if err := os.Remove(dst); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return os.Symlink(link, dst)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cannot copy special file: %s", src)
	}
	if !opts.NoOverwrite {
		if destInfo, err := os.Stat(dst); err == nil && os.SameFile(info, destInfo) {
			return fmt.Errorf("source and destination are the same file: %s", src)
		}
	}
	return f.copyFile(src, dst, opts)
}

// Resolve symlinks in the existing prefix without requiring the destination to exist.
func resolveCopyDestination(path string) (string, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = resolveCopyDestination(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
