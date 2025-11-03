package filesystem

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// Subdirectory represents a subdirectory in the filesystem
type Subdirectory struct {
	Path string `json:"path" binding:"required"`
	Name string `json:"name" binding:"required"`
} // @name Subdirectory

// Directory represents a directory in the filesystem
type Directory struct {
	Path           string          `json:"path" binding:"required"`
	Name           string          `json:"name" binding:"required"`
	Files          []*File         `json:"files" binding:"required"`
	Subdirectories []*Subdirectory `json:"subdirectories" binding:"required"` // @name Subdirectories
} // @name Directory

func NewDirectory(path string) *Directory {
	return &Directory{
		Path:           path,
		Name:           filepath.Base(path),
		Files:          []*File{},
		Subdirectories: []*Subdirectory{},
	}
}

// AddFile adds a file to the directory
func (d *Directory) AddFile(file *File) {
	d.Files = append(d.Files, file)
}

// AddSubdirectory adds a subdirectory to the directory
func (d *Directory) AddSubdirectory(subDir *Subdirectory) {
	d.Subdirectories = append(d.Subdirectories, subDir)
}

// GetFile returns a file by name if it exists in this directory
func (d *Directory) GetFile(name string) *File {
	for _, file := range d.Files {
		if filepath.Base(file.Path) == name {
			return file
		}
	}
	return nil
}

// GetSubdirectory returns a subdirectory by name if it exists in this directory
func (d *Directory) GetSubdirectory(name string) *Subdirectory {
	for _, subDir := range d.Subdirectories {
		if filepath.Base(subDir.Path) == name {
			return subDir
		}
	}
	return nil
}

// CountFiles returns the total number of files in this directory (excluding subdirectories)
func (d *Directory) CountFiles() int {
	return len(d.Files)
}

// CountSubdirectories returns the total number of subdirectories in this directory
func (d *Directory) CountSubdirectories() int {
	return len(d.Subdirectories)
}

// IsEmpty returns true if the directory has no files and no subdirectories
func (d *Directory) IsEmpty() bool {
	return len(d.Files) == 0 && len(d.Subdirectories) == 0
}

func (fs *Filesystem) CreateOrUpdateTree(rootPath string, files map[string]string) error {
	// Check if root path exists, create it if not
	isDir, err := fs.DirectoryExists(rootPath)
	if err != nil || !isDir {
		// Create the root directory if it doesn't exist or is not a directory
		err := fs.CreateDirectory(rootPath, 0755)
		if err != nil {
			return fmt.Errorf("error creating root directory: %w", err)
		}
	}

	// Process each file in the request
	for relativePath, content := range files {
		// Combine root path with relative path, ensuring there's only one slash between them
		fullPath := rootPath
		if rootPath != "/" {
			fullPath += "/"
		}
		fullPath += relativePath

		// Get the parent directory path - we need to ensure it exists
		dir := filepath.Dir(fullPath)
		if dir != "/" {
			// Create parent directories
			err := fs.CreateDirectory(dir, 0755)
			if err != nil {
				return fmt.Errorf("error creating parent directory: %w", err)
			}
		}

		// Write the file
		err := fs.WriteFile(fullPath, []byte(content), 0644)
		if err != nil {
			return fmt.Errorf("error writing file: %w", err)
		}
	}

	return nil
}

// WatchDirectory watches a specific directory for changes.
// The callback is called with the event when a change occurs.
func (fs *Filesystem) WatchDirectory(path string, callback func(event fsnotify.Event)) (func(), error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	err = watcher.Add(absPath)
	if err != nil {
		return nil, err
	}

	stopChan := make(chan struct{})
	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				callback(event)
				// If the watched directory itself is removed or renamed, notify and stop watching
				if (event.Op&fsnotify.Remove != 0 || event.Op&fsnotify.Rename != 0) && event.Name == absPath {
					return
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logrus.Error("error:", err)
			}
		}
	}()

	stop := func() {
		close(stopChan)
		_ = watcher.Close()
	}
	return stop, nil
}

// WatchDirectoryRecursive watches a directory and all its subdirectories for changes.
// The callback is called with the event when a change occurs.
func (fs *Filesystem) WatchDirectoryRecursive(path string, callback func(event fsnotify.Event)) (func(), error) {
	absPath, err := fs.GetAbsolutePath(path)
	if err != nil {
		return nil, err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	// Helper to add all subdirectories
	addDirs := func(root string) error {
		return filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return watcher.Add(p)
			}
			return nil
		})
	}

	if err := addDirs(absPath); err != nil {
		_ = watcher.Close()
		return nil, err
	}

	stopChan := make(chan struct{})

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				callback(event)
				// If a new directory is created, add it (and its subdirs)
				if event.Op&fsnotify.Create != 0 {
					info, err := os.Stat(event.Name)
					if err == nil && info.IsDir() {
						_ = addDirs(event.Name)
					}
				}
				// If a directory is removed, watcher will error on it, but fsnotify cleans up
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				logrus.Error("error:", err)
			}
		}
	}()

	stop := func() {
		close(stopChan)
		_ = watcher.Close()
	}
	return stop, nil
}
