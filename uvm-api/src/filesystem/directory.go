package filesystem

import (
	"path/filepath"
)

type Directory struct {
	Path           string
	Files          []*File
	Subdirectories []*Directory
}

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
