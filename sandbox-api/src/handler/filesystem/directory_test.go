package filesystem

import (
	"os"
	"path/filepath"
	"strings"
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
	stop, err := NewFilesystem(root).WatchDirectoryRecursive(root, nil, func(event fsnotify.Event) {
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
	// event proves the watcher works; keep observing briefly afterward to catch
	// an initial snapshot emitted asynchronously after that event.
	created := filepath.Join(subdir, "after-watch.txt")
	if err := os.WriteFile(created, []byte("after watch"), 0644); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	createdSeen := false
	for {
		select {
		case event := <-events:
			if event.Op&fsnotify.Create == 0 {
				continue
			}
			if event.Name != created {
				t.Fatalf("unexpected initial CREATE event: %v", event)
			}
			if !createdSeen {
				createdSeen = true
				timer.Reset(200 * time.Millisecond)
			}
		case <-timer.C:
			if createdSeen {
				return
			}
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

func TestWatchDirectoryRecursiveSkipsIgnoredDirectories(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"node_modules/pkg", "src", "locked"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// An unreadable subdirectory must not fail the whole watch.
	if os.Geteuid() != 0 {
		if err := os.Chmod(filepath.Join(root, "locked"), 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(filepath.Join(root, "locked"), 0755) })
	}

	events := make(chan fsnotify.Event, 64)
	done := make(chan struct{})
	skip := func(p string) bool { return strings.Contains(p, "node_modules") }
	stop, err := NewFilesystem(root).WatchDirectoryRecursive(root, skip, func(event fsnotify.Event) {
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

	// Neither the existing ignored tree nor one created later gets a watch.
	if err := os.WriteFile(filepath.Join(root, "node_modules/pkg/index.js"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src/node_modules/dep"), 0755); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(root, "src/node_modules/dep/index.js"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "src/marker.txt")
	if err := os.WriteFile(marker, nil, 0644); err != nil {
		t.Fatal(err)
	}

	timeout := time.After(5 * time.Second)
	for {
		select {
		case event := <-events:
			if strings.Contains(event.Name, "node_modules/") {
				t.Fatalf("got event inside an ignored directory: %v", event)
			}
			if event.Name == marker {
				return
			}
		case <-timeout:
			t.Fatal("timed out waiting for the marker event")
		}
	}
}
