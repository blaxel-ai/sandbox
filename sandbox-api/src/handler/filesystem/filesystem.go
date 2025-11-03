package filesystem

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Filesystem represents the root directory of the filesystem
type Filesystem struct {
	Root       string `json:"root"`
	WorkingDir string `json:"workingDir"`
} // @name Filesystem

// FileByte represents a file in the filesystem
type FileByte struct {
	Path         string      `json:"-"`
	Permissions  os.FileMode `json:"-"`
	Size         int64       `json:"size"`
	LastModified time.Time   `json:"lastModified"`
	Owner        string      `json:"owner"`
	Group        string      `json:"group"`
}

// File is a data transfer object for File with string permissions
type File struct {
	Path         string    `json:"path" binding:"required"`
	Name         string    `json:"name" binding:"required"`
	Permissions  string    `json:"permissions" binding:"required"`
	Size         int64     `json:"size" binding:"required"`
	LastModified time.Time `json:"lastModified" binding:"required"`
	Owner        string    `json:"owner" binding:"required"`
	Group        string    `json:"group" binding:"required"`
} // @name File

// MarshalJSON implements json.Marshaler for custom JSON marshaling
func (f FileByte) MarshalJSON() ([]byte, error) {
	return json.Marshal(File{
		Path:         f.Path,
		Permissions:  fmt.Sprintf("%o", f.Permissions),
		Size:         f.Size,
		LastModified: f.LastModified,
		Owner:        f.Owner,
		Group:        f.Group,
	})
}

// UnmarshalJSON implements json.Unmarshaler for custom JSON unmarshaling
func (f *FileByte) UnmarshalJSON(data []byte) error {
	var dto File
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}

	f.Path = dto.Path
	f.Size = dto.Size
	f.LastModified = dto.LastModified
	f.Owner = dto.Owner
	f.Group = dto.Group

	// Parse permissions if present
	if dto.Permissions != "" {
		// Handle formats like '-rw-r--r--' by extracting just the numeric part
		permStr := dto.Permissions
		if strings.HasPrefix(permStr, "-") || strings.HasPrefix(permStr, "d") {
			// Convert unix-style permissions like "-rw-r--r--" to octal
			var perm uint32 = 0

			if len(permStr) >= 10 {
				// Owner permissions
				if permStr[1] == 'r' {
					perm |= 0400
				}
				if permStr[2] == 'w' {
					perm |= 0200
				}
				if permStr[3] == 'x' {
					perm |= 0100
				}

				// Group permissions
				if permStr[4] == 'r' {
					perm |= 0040
				}
				if permStr[5] == 'w' {
					perm |= 0020
				}
				if permStr[6] == 'x' {
					perm |= 0010
				}

				// Others permissions
				if permStr[7] == 'r' {
					perm |= 0004
				}
				if permStr[8] == 'w' {
					perm |= 0002
				}
				if permStr[9] == 'x' {
					perm |= 0001
				}
			}

			f.Permissions = os.FileMode(perm)
		} else {
			// Try to parse as octal string
			perm, err := strconv.ParseUint(permStr, 8, 32)
			if err != nil {
				return fmt.Errorf("invalid permissions format: %s", permStr)
			}
			f.Permissions = os.FileMode(perm)
		}
	}

	return nil
}

type FileWithContentByte struct {
	FileByte
	Content []byte `json:"-"`
}

// FileWithContent is a data transfer object for FileWithContent with encoded content
type FileWithContent struct {
	File
	Content string `json:"content" binding:"required"`
} // @name FileWithContent

// MarshalJSON implements json.Marshaler for custom JSON marshaling
func (f FileWithContentByte) MarshalJSON() ([]byte, error) {
	fileDTO := File{
		Path:         f.Path,
		Permissions:  fmt.Sprintf("%o", f.Permissions),
		Size:         f.Size,
		LastModified: f.LastModified,
		Owner:        f.Owner,
		Group:        f.Group,
	}

	return json.Marshal(FileWithContent{
		File:    fileDTO,
		Content: string(f.Content),
	})
}

// UnmarshalJSON implements json.Unmarshaler for custom JSON unmarshaling
func (f *FileWithContentByte) UnmarshalJSON(data []byte) error {
	var dto FileWithContent
	if err := json.Unmarshal(data, &dto); err != nil {
		return err
	}

	// First parse the file part
	var file FileByte
	fileData, err := json.Marshal(dto.File)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(fileData, &file); err != nil {
		return err
	}

	f.FileByte = file
	f.Content = []byte(dto.Content)

	return nil
}

func NewFilesystem(root string) *Filesystem {
	return &Filesystem{Root: root, WorkingDir: root}
}

func NewFilesystemWithWorkingDir(root string, workingDir string) *Filesystem {
	return &Filesystem{Root: root, WorkingDir: workingDir}
}

// ResolveDisplayPath converts "." to the actual working directory for display purposes
func (fs *Filesystem) ResolveDisplayPath(path string) string {
	if path == "." || path == "./" {
		return fs.WorkingDir
	}
	return path
}

// GetAbsolutePath gets the absolute path, ensuring it's within the root
func (fs *Filesystem) GetAbsolutePath(path string) (string, error) {
	var absPath string

	// If path is absolute (starts with /), use it directly
	if filepath.IsAbs(path) {
		absPath = path
	} else {
		// If path is relative, resolve it from the working directory
		absPath = filepath.Join(fs.WorkingDir, path)
	}

	// Clean the path to resolve . and .. references
	absPath = filepath.Clean(absPath)

	// For absolute paths outside the root, we don't restrict access
	// This allows accessing system directories when using absolute paths
	// For relative paths, we still ensure they're within bounds
	if !filepath.IsAbs(path) {
		// Verify the path is within the root to prevent path traversal for relative paths
		if relPath, err := filepath.Rel(fs.Root, absPath); err != nil || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
			return "", errors.New("path is outside of the root directory")
		}
	}

	return absPath, nil
}

// FileExists checks if a file exists at the given path
func (fs *Filesystem) FileExists(path string) (bool, error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return false, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return !info.IsDir(), nil
}

// DirectoryExists checks if a directory exists at the given path
func (fs *Filesystem) DirectoryExists(path string) (bool, error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return false, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return info.IsDir(), nil
}

// ReadFile reads a file and returns its contents
func (fs *Filesystem) ReadFile(path string) (*FileWithContentByte, error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}

	// Get file information
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, errors.New("path points to a directory, not a file")
	}

	// Read content
	content, err := os.ReadFile(absPath)
	if err != nil {
		return nil, err
	}

	// Get owner and group
	owner, group, err := fs.getFileOwnerAndGroup(absPath)
	if err != nil {
		return nil, err
	}

	// Create and return a FileWithContent instance
	result := &FileWithContentByte{
		Content: content,
	}
	// Set File fields
	result.Path = fs.ResolveDisplayPath(path)
	result.Permissions = info.Mode()
	result.Size = info.Size()
	result.LastModified = info.ModTime()
	result.Owner = owner
	result.Group = group

	return result, nil
}

// WriteFile writes content to a file
func (fs *Filesystem) WriteFile(path string, content []byte, perm os.FileMode) error {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(absPath, content, perm)
}

// WriteFileFromReader streams content from a reader to a file on disk
func (fs *Filesystem) WriteFileFromReader(path string, r io.Reader, perm os.FileMode) error {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	if _, err := io.Copy(f, r); err != nil {
		// Clean up the partially written file on error
		_ = os.Remove(absPath)
		_ = f.Close() // Close file before attempting to remove
		return err
	}
	return nil
}

// CreateDirectory creates a directory at the given path
func (fs *Filesystem) CreateDirectory(path string, perm os.FileMode) error {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	return os.MkdirAll(absPath, perm)
}

// ListDirectory lists files and directories in the given path
func (fs *Filesystem) ListDirectory(path string) (*Directory, error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}

	// Use the resolved display path for the directory
	displayPath := fs.ResolveDisplayPath(path)
	dir := NewDirectory(displayPath)

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return nil, err
	}

	for _, entry := range entries {
		// Use displayPath for the entry paths too
		entryPath := filepath.Join(displayPath, entry.Name())
		absEntryPath := filepath.Join(absPath, entry.Name())

		// Use os.Lstat to get info about the symlink itself, not its target
		// This prevents errors when symlinks point to non-existent targets
		info, err := os.Lstat(absEntryPath)
		if err != nil {
			return nil, err
		}

		if info.IsDir() {
			dir.AddSubdirectory(&Subdirectory{Path: entryPath, Name: entry.Name()})
		} else {
			// It's a file or symlink
			owner, group, err := fs.getFileOwnerAndGroup(absEntryPath)
			if err != nil {
				return nil, err
			}

			file := &File{Path: entryPath, Name: entry.Name(), Permissions: fmt.Sprintf("%o", info.Mode()), Size: info.Size(), LastModified: info.ModTime(), Owner: owner, Group: group}
			dir.AddFile(file)
		}
	}

	return dir, nil
}

// DeleteFile deletes a file at the given path
func (fs *Filesystem) DeleteFile(path string) error {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return err
	}

	if fileInfo.IsDir() {
		return errors.New("path points to a directory, not a file")
	}

	return os.Remove(absPath)
}

// DeleteDirectory deletes a directory at the given path
func (fs *Filesystem) DeleteDirectory(path string, recursive bool) error {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	fileInfo, err := os.Stat(absPath)
	if err != nil {
		return err
	}

	if !fileInfo.IsDir() {
		return errors.New("path points to a file, not a directory")
	}

	if recursive {
		return os.RemoveAll(absPath)
	}
	return os.Remove(absPath) // This will fail if directory is not empty
}

// CopyFile copies a file from src to dst
func (fs *Filesystem) CopyFile(src, dst string) error {
	srcAbs, err := fs.GetAbsolutePath(src)
	if err != nil {
		return err
	}

	dstAbs, err := fs.GetAbsolutePath(dst)
	if err != nil {
		return err
	}

	// Read the source file
	content, err := os.ReadFile(srcAbs)
	if err != nil {
		return err
	}

	// Get source file info for permissions
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return err
	}

	// Ensure destination directory exists
	destDir := filepath.Dir(dstAbs)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	// Write to destination with same permissions
	return os.WriteFile(dstAbs, content, srcInfo.Mode())
}

// MoveFile moves a file from src to dst
func (fs *Filesystem) MoveFile(src, dst string) error {
	srcAbs, err := fs.GetAbsolutePath(src)
	if err != nil {
		return err
	}

	dstAbs, err := fs.GetAbsolutePath(dst)
	if err != nil {
		return err
	}

	// Ensure destination directory exists
	destDir := filepath.Dir(dstAbs)
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return err
	}

	return os.Rename(srcAbs, dstAbs)
}

// getFileOwnerAndGroup returns the owner and group of a file
func (fs *Filesystem) getFileOwnerAndGroup(path string) (string, string, error) {
	// Use Lstat to get info about the symlink itself, not its target
	info, err := os.Lstat(path)
	if err != nil {
		return "", "", err
	}

	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", "", errors.New("failed to get file stat")
	}

	uid := stat.Uid
	gid := stat.Gid

	// Try to get username from UID
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	ownerName := strconv.FormatUint(uint64(uid), 10)
	if err == nil {
		ownerName = u.Username
	}

	// Try to get group name from GID
	g, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10))
	groupName := strconv.FormatUint(uint64(gid), 10)
	if err == nil {
		groupName = g.Name
	}

	return ownerName, groupName, nil
}

// GetFileInfo returns file information without reading its content
func (fs *Filesystem) GetFileInfo(path string) (*FileByte, error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		return nil, err
	}

	if info.IsDir() {
		return nil, errors.New("path points to a directory, not a file")
	}

	owner, group, err := fs.getFileOwnerAndGroup(absPath)
	if err != nil {
		return nil, err
	}

	return &FileByte{Path: path, Permissions: info.Mode(), Size: info.Size(), LastModified: info.ModTime(), Owner: owner, Group: group}, nil
}

// Walk walks the file tree rooted at root, calling fn for each file or directory
func (fs *Filesystem) Walk(root string, fn filepath.WalkFunc) error {
	absRoot, err := fs.GetAbsolutePath(root)
	if err != nil {
		return err
	}

	return filepath.Walk(absRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Convert absolute path back to relative path for consistency
		relPath, err := filepath.Rel(fs.Root, path)
		if err != nil {
			return err
		}

		// Call the provided function with the relative path
		return fn(relPath, info, nil)
	})
}

// CreateOrUpdateFile creates or updates a file
func (fs *Filesystem) CreateOrUpdateFile(path string, content string, isDirectory bool, permissions string) error {
	// Parse permissions or use appropriate defaults
	var perm os.FileMode
	if permissions != "" {
		permInt, err := strconv.ParseUint(permissions, 8, 32)
		if err != nil {
			return fmt.Errorf("invalid permissions format '%s': %w", permissions, err)
		}
		perm = os.FileMode(permInt)
	} else {
		// Use appropriate defaults: 0755 for directories, 0644 for files
		if isDirectory {
			perm = 0755
		} else {
			perm = 0644
		}
	}

	if isDirectory {
		return fs.CreateDirectory(path, perm)
	}

	// Get absolute path for directory creation
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return err
	}

	// Ensure parent directory exists
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return fs.WriteFile(path, []byte(content), perm)
}
