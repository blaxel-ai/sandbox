package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

// setupTestEnvironment creates a temporary directory for testing
func setupTestEnvironment(t *testing.T) (string, *Filesystem, func()) {
	t.Helper()

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "filesystem-test-*")
	if err != nil {
		t.Fatalf("Failed to create temporary directory: %v", err)
	}

	// Create a filesystem instance
	fs := NewFilesystem(tempDir)

	// Return a cleanup function
	cleanup := func() {
		_ = os.RemoveAll(tempDir)
	}

	return tempDir, fs, cleanup
}

// TestNewFilesystem tests the creation of a new filesystem
func TestNewFilesystem(t *testing.T) {
	root := "/tmp/test-fs"
	fs := NewFilesystem(root)

	if fs.Root != root {
		t.Errorf("Expected root to be %s, got %s", root, fs.Root)
	}
}

// TestGetAbsolutePath tests the path validation functionality
func TestGetAbsolutePath(t *testing.T) {
	tempDir, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()

	testCases := []struct {
		path      string
		shouldErr bool
	}{
		{"file.txt", false},
		{"dir/file.txt", false},
		{"../file.txt", true},
		{"../../file.txt", true},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			absPath, err := fs.GetAbsolutePath(tc.path)

			if tc.shouldErr {
				if err == nil {
					t.Errorf("Expected error for path %s, but got none", tc.path)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %s: %v", tc.path, err)
				}

				expected := filepath.Join(tempDir, tc.path)
				if absPath != expected {
					t.Errorf("Expected absolute path to be %s, got %s", expected, absPath)
				}
			}
		})
	}
}

// TestFileOperations tests basic file operations
func TestFileOperations(t *testing.T) {
	_, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test file creation
	content := []byte("Hello, world!")
	err := fs.WriteFile("test.txt", content, 0644)
	if err != nil {
		t.Fatalf("Failed to write file: %v", err)
	}

	// Test file exists
	exists, err := fs.FileExists("test.txt")
	if err != nil {
		t.Fatalf("Failed to check if file exists: %v", err)
	}
	if !exists {
		t.Errorf("Expected file to exist, but it doesn't")
	}

	// Test reading file
	readFile, err := fs.ReadFile("test.txt")
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}
	if string(readFile.Content) != string(content) {
		t.Errorf("Expected content to be %s, got %s", string(content), string(readFile.Content))
	}

	// Test copying file
	err = fs.CopyFile("test.txt", "test-copy.txt")
	if err != nil {
		t.Fatalf("Failed to copy file: %v", err)
	}

	copyExists, err := fs.FileExists("test-copy.txt")
	if err != nil {
		t.Fatalf("Failed to check if copied file exists: %v", err)
	}
	if !copyExists {
		t.Errorf("Expected copied file to exist, but it doesn't")
	}

	// Test moving file
	err = fs.MoveFile("test-copy.txt", "test-moved.txt")
	if err != nil {
		t.Fatalf("Failed to move file: %v", err)
	}

	movedExists, err := fs.FileExists("test-moved.txt")
	if err != nil {
		t.Fatalf("Failed to check if moved file exists: %v", err)
	}
	if !movedExists {
		t.Errorf("Expected moved file to exist, but it doesn't")
	}

	copyExists, err = fs.FileExists("test-copy.txt")
	if err != nil {
		t.Fatalf("Failed to check if copied file exists: %v", err)
	}
	if copyExists {
		t.Errorf("Expected copied file to not exist after move, but it does")
	}

	// Test getting file info
	fileInfo, err := fs.GetFileInfo("test.txt")
	if err != nil {
		t.Fatalf("Failed to get file info: %v", err)
	}
	if fileInfo.Size != int64(len(content)) {
		t.Errorf("Expected file size to be %d, got %d", len(content), fileInfo.Size)
	}

	// Test deleting file
	err = fs.DeleteFile("test.txt")
	if err != nil {
		t.Fatalf("Failed to delete file: %v", err)
	}

	exists, err = fs.FileExists("test.txt")
	if err != nil {
		t.Fatalf("Failed to check if file exists: %v", err)
	}
	if exists {
		t.Errorf("Expected file to not exist after deletion, but it does")
	}
}

// TestDirectoryOperations tests basic directory operations
func TestDirectoryOperations(t *testing.T) {
	_, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test directory creation
	err := fs.CreateDirectory("testdir", 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	// Test directory exists
	exists, err := fs.DirectoryExists("testdir")
	if err != nil {
		t.Fatalf("Failed to check if directory exists: %v", err)
	}
	if !exists {
		t.Errorf("Expected directory to exist, but it doesn't")
	}

	// Create a file inside the directory
	err = fs.WriteFile("testdir/file.txt", []byte("Content"), 0644)
	if err != nil {
		t.Fatalf("Failed to write file in directory: %v", err)
	}

	// Test listing directory
	dir, err := fs.ListDirectory("testdir")
	if err != nil {
		t.Fatalf("Failed to list directory: %v", err)
	}
	if len(dir.Files) != 1 {
		t.Errorf("Expected 1 file in directory, got %d", len(dir.Files))
	}
	if dir.Files[0].Path != filepath.Join("testdir", "file.txt") {
		t.Errorf("Expected file path to be %s, got %s", filepath.Join("testdir", "file.txt"), dir.Files[0].Path)
	}

	// Test creating subdirectory
	err = fs.CreateDirectory("testdir/subdir", 0755)
	if err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	// Test listing directory again
	dir, err = fs.ListDirectory("testdir")
	if err != nil {
		t.Fatalf("Failed to list directory: %v", err)
	}
	if len(dir.Subdirectories) != 1 {
		t.Errorf("Expected 1 subdirectory, got %d", len(dir.Subdirectories))
	}

	// Test delete non-recursive (should fail with non-empty dir)
	err = fs.DeleteDirectory("testdir", false)
	if err == nil {
		t.Errorf("Expected error when deleting non-empty directory without recursive flag")
	}

	// Test recursive delete
	err = fs.DeleteDirectory("testdir", true)
	if err != nil {
		t.Fatalf("Failed to delete directory recursively: %v", err)
	}

	exists, err = fs.DirectoryExists("testdir")
	if err != nil {
		t.Fatalf("Failed to check if directory exists: %v", err)
	}
	if exists {
		t.Errorf("Expected directory to not exist after deletion, but it does")
	}
}

// TestWalk tests the filesystem walking functionality
func TestWalk(t *testing.T) {
	_, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Create a directory structure for testing
	err := fs.CreateDirectory("walktest/dir1", 0755)
	if err != nil {
		t.Fatalf("Failed to create directory structure: %v", err)
	}

	err = fs.CreateDirectory("walktest/dir2", 0755)
	if err != nil {
		t.Fatalf("Failed to create directory structure: %v", err)
	}

	err = fs.WriteFile("walktest/file1.txt", []byte("Content 1"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	err = fs.WriteFile("walktest/dir1/file2.txt", []byte("Content 2"), 0644)
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test walking the filesystem
	paths := make([]string, 0)
	err = fs.Walk("walktest", func(path string, info os.FileInfo, err error) error {
		paths = append(paths, path)
		return nil
	})

	if err != nil {
		t.Fatalf("Failed to walk filesystem: %v", err)
	}

	// Expected paths (note: order may vary depending on filesystem implementation)
	expectedPaths := []string{
		"walktest",
		filepath.Join("walktest", "dir1"),
		filepath.Join("walktest", "dir1", "file2.txt"),
		filepath.Join("walktest", "dir2"),
		filepath.Join("walktest", "file1.txt"),
	}

	if len(paths) != len(expectedPaths) {
		t.Errorf("Expected %d paths, got %d", len(expectedPaths), len(paths))
	}

	// Check that all expected paths are found
	pathMap := make(map[string]bool)
	for _, path := range paths {
		pathMap[path] = true
	}

	for _, expectedPath := range expectedPaths {
		if !pathMap[expectedPath] {
			t.Errorf("Expected path %s was not found in walk result", expectedPath)
		}
	}
}

// TestErrorHandling tests error conditions
func TestErrorHandling(t *testing.T) {
	_, fs, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test reading non-existent file
	_, err := fs.ReadFile("nonexistent.txt")
	if err == nil {
		t.Errorf("Expected error when reading non-existent file, got none")
	}

	// Test deleting non-existent file
	err = fs.DeleteFile("nonexistent.txt")
	if err == nil {
		t.Errorf("Expected error when deleting non-existent file, got none")
	}

	// Test getting info for non-existent file
	_, err = fs.GetFileInfo("nonexistent.txt")
	if err == nil {
		t.Errorf("Expected error when getting info for non-existent file, got none")
	}

	// Test trying to read a directory as a file
	err = fs.CreateDirectory("testdir", 0755)
	if err != nil {
		t.Fatalf("Failed to create directory: %v", err)
	}

	_, err = fs.ReadFile("testdir")
	if err == nil {
		t.Errorf("Expected error when reading directory as file, got none")
	}

	// Test trying to delete a directory as a file
	err = fs.DeleteFile("testdir")
	if err == nil {
		t.Errorf("Expected error when deleting directory as file, got none")
	}

	// Test trying to get file info for a directory
	_, err = fs.GetFileInfo("testdir")
	if err == nil {
		t.Errorf("Expected error when getting file info for directory, got none")
	}
}
