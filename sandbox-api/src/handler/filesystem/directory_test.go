package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
)

func TestWatchDirectoryRecursiveReportsChangesWithoutInitialSnapshot(t *testing.T) {
	root := t.TempDir()
	subdir := filepath.Join(root, "existing-directory")
	if err := os.Mkdir(subdir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(subdir, "existing.txt")
	if err := os.WriteFile(existing, []byte("before watch"), 0644); err != nil {
		t.Fatal(err)
	}

	events := make(chan fsnotify.Event, 32)
	done := make(chan struct{})
	stop, err := NewFilesystem(root).WatchDirectoryRecursive(root, func(event fsnotify.Event) {
		select {
		case events <- event:
		case <-done:
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(done)
		stop()
	})

	// Registration completes before WatchDirectoryRecursive returns. A subsequent
	// event proves the watcher works and provides a boundary for initial events.
	created := filepath.Join(subdir, "after-watch.txt")
	if err := os.WriteFile(created, []byte("after watch"), 0644); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Op&fsnotify.Create == 0 {
				continue
			}
			if event.Name != created {
				t.Fatalf("unexpected initial CREATE event: %v", event)
			}
			return
		case <-timer.C:
			t.Fatal("timed out waiting for a CREATE event in an existing subdirectory")
		}
	}
}

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
	subdir := &Subdirectory{Path: "test/subdir", Name: "subdir"}
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
