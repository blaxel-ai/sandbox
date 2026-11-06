package filesystem

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
)

var exclusive = WriteOptions{NoOverwrite: true}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func mustExist(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("got %v, want os.ErrExist", err)
	}
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	must(t, err)
	return string(data)
}

func TestCopyNoOverwrite(t *testing.T) {
	root, f, cleanup := setupTestEnvironment(t)
	defer cleanup()
	must(t, f.WriteFile("source/nested/file", []byte("copied"), 0644))
	must(t, os.Symlink("nested/file", filepath.Join(root, "source/link")))

	// Absent destination: the copy is created at that path.
	must(t, f.Copy("source", "destination", exclusive))
	if got := readString(t, filepath.Join(root, "destination/nested/file")); got != "copied" {
		t.Fatalf("copied content = %q", got)
	}
	if link, err := os.Readlink(filepath.Join(root, "destination/link")); err != nil || link != "nested/file" {
		t.Fatalf("copied link = %q, %v", link, err)
	}

	// Existing directory: like cp -r, the copy goes inside it.
	must(t, f.CreateDirectory("container", 0755))
	must(t, f.Copy("source", "container", exclusive))
	if got := readString(t, filepath.Join(root, "container/source/nested/file")); got != "copied" {
		t.Fatalf("copy into container = %q", got)
	}
	mustExist(t, f.Copy("source", "container", exclusive))

	// Any existing final target conflicts and is left unchanged.
	must(t, f.WriteFile("file", []byte("original"), 0600))
	must(t, f.CreateDirectory("holder/source", 0755))
	must(t, os.Symlink("missing", filepath.Join(root, "dangling")))
	mustExist(t, f.Copy("source/nested/file", "file", exclusive))
	mustExist(t, f.Copy("source", "holder", exclusive))
	mustExist(t, f.Copy("source/nested/file", "dangling", exclusive))
	mustExist(t, f.Copy("source/link", "file", exclusive))
	if got := readString(t, filepath.Join(root, "file")); got != "original" {
		t.Fatalf("protected file = %q", got)
	}
	if _, err := os.Lstat(filepath.Join(root, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dangling symlink target was created: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(root, "holder/source")); len(entries) != 0 {
		t.Fatalf("existing directory was merged into: %v", entries)
	}

	// Default mode overwrites files and merges directories, as before.
	must(t, f.Copy("source/nested/file", "file"))
	if got := readString(t, filepath.Join(root, "file")); got != "copied" {
		t.Fatalf("default copy = %q", got)
	}
	must(t, f.Copy("source", "holder"))
	if got := readString(t, filepath.Join(root, "holder/source/nested/file")); got != "copied" {
		t.Fatalf("default merge = %q", got)
	}

	// Copying a directory into itself is refused, also through a symlink.
	if err := f.Copy("source", "source/inside", exclusive); err == nil {
		t.Fatal("copy into itself succeeded")
	}
	if err := f.Copy("source", "source"); err == nil {
		t.Fatal("copy into itself succeeded")
	}
	must(t, os.Symlink("source", filepath.Join(root, "alias")))
	if err := f.Copy("source", "alias/inside", exclusive); err == nil {
		t.Fatal("copy into itself through a symlink succeeded")
	}
	if err := f.Copy("source/nested/file", "source/nested/file"); err == nil {
		t.Fatal("copy onto itself succeeded")
	}
	if got := readString(t, filepath.Join(root, "source/nested/file")); got != "copied" {
		t.Fatalf("source changed: %q", got)
	}
	if err := f.Copy("missing-source", "unused", exclusive); err == nil {
		t.Fatal("copy of a missing source succeeded")
	}
}

func TestCopyReadOnlyDirectoriesAndSpecialFiles(t *testing.T) {
	root, f, cleanup := setupTestEnvironment(t)
	defer cleanup()
	must(t, f.WriteFile("source/nested/file", []byte("copied"), 0644))
	must(t, os.Chmod(filepath.Join(root, "source/nested"), 0500))
	defer os.Chmod(filepath.Join(root, "source/nested"), 0700)
	must(t, f.Copy("source", "destination", exclusive))
	defer os.Chmod(filepath.Join(root, "destination/nested"), 0700)
	info, err := os.Stat(filepath.Join(root, "destination/nested"))
	must(t, err)
	if info.Mode().Perm() != 0500 {
		t.Fatalf("copied directory mode = %o, want 500", info.Mode().Perm())
	}
	if got := readString(t, filepath.Join(root, "destination/nested/file")); got != "copied" {
		t.Fatalf("copied content = %q", got)
	}
	must(t, syscall.Mkfifo(filepath.Join(root, "fifo"), 0600))
	if err := f.Copy("fifo", "unused", exclusive); err == nil {
		t.Fatal("copy of a FIFO succeeded")
	}
}

func TestCopyNoOverwriteConcurrentCopiesHaveOneWinner(t *testing.T) {
	_, f, cleanup := setupTestEnvironment(t)
	defer cleanup()
	must(t, f.WriteFile("source/file", []byte("copied"), 0644))
	must(t, f.CreateDirectory("container", 0755))
	// Contenders need the same final target: a directory copied to an absent
	// path turns that path into a container for the next copy, as with cp -r.
	for source, destination := range map[string]string{"source": "container", "source/file": "file-copy"} {
		results := make(chan error, 16)
		var workers sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < cap(results); i++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				results <- f.Copy(source, destination, exclusive)
			}()
		}
		close(start)
		workers.Wait()
		close(results)
		successes := 0
		for err := range results {
			if err == nil {
				successes++
			} else {
				mustExist(t, err)
			}
		}
		if successes != 1 {
			t.Fatalf("%s: %d copies succeeded, want 1", source, successes)
		}
	}
}
