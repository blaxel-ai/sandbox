package filesystem

import (
	"testing"
	"time"
)

// TestDirectoryMethods tests the Directory struct methods
func TestDirectoryMethods(t *testing.T) {
	dir := NewDirectory("test")

	// Test adding and getting files
	file := &File{
		Path:         "test/file.txt",
		Permissions:  "644",
		Size:         100,
		LastModified: time.Now(),
		Owner:        "user",
		Group:        "group",
	}

	dir.AddFile(file)

	if dir.CountFiles() != 1 {
		t.Errorf("Expected 1 file, got %d", dir.CountFiles())
	}

	retrievedFile := dir.GetFile("file.txt")
	if retrievedFile == nil {
		t.Fatalf("Failed to get file by name")
	}

	if retrievedFile.Path != file.Path {
		t.Errorf("Expected file path to be %s, got %s", file.Path, retrievedFile.Path)
	}

	// Test adding and getting subdirectories
	subdir := &Subdirectory{Path: "test/subdir"}
	dir.AddSubdirectory(subdir)

	if dir.CountSubdirectories() != 1 {
		t.Errorf("Expected 1 subdirectory, got %d", dir.CountSubdirectories())
	}

	retrievedSubdir := dir.GetSubdirectory("subdir")
	if retrievedSubdir == nil {
		t.Fatalf("Failed to get subdirectory by name")
	}

	if retrievedSubdir.Path != subdir.Path {
		t.Errorf("Expected subdirectory path to be %s, got %s", subdir.Path, retrievedSubdir.Path)
	}

	// Test IsEmpty
	emptyDir := NewDirectory("empty")
	if !emptyDir.IsEmpty() {
		t.Errorf("Expected directory to be empty")
	}

	if dir.IsEmpty() {
		t.Errorf("Expected directory not to be empty")
	}
}
