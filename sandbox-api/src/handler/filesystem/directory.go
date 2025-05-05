package filesystem

import (
	"fmt"
	"path/filepath"
)

// Directory represents a directory in the filesystem
type Directory struct {
	Path           string       `json:"path"`
	Files          []*File      `json:"files"`
	Subdirectories []*Directory `json:"subdirectories"` // @name Subdirectories
} // @name Directory

func NewDirectory(path string) *Directory {
	return &Directory{
		Path:           path,
		Files:          []*File{},
		Subdirectories: []*Directory{},
	}
}

// AddFile adds a file to the directory
func (d *Directory) AddFile(file *File) {
	d.Files = append(d.Files, file)
}

// AddSubdirectory adds a subdirectory to the directory
func (d *Directory) AddSubdirectory(subDir *Directory) {
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
func (d *Directory) GetSubdirectory(name string) *Directory {
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
