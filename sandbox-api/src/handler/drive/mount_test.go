package drive

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// TestCreateMountPointReportsCreation checks the flag that decides whether the
// mount point may be handed to the workload user.
func TestCreateMountPointReportsCreation(t *testing.T) {
	root := t.TempDir()

	fresh := filepath.Join(root, "nested", "mount")
	created, err := createMountPoint(fresh)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", fresh, err)
	}
	if !created {
		t.Fatalf("createMountPoint(%q) = false, want true for a new directory", fresh)
	}

	// A pre-existing directory must never be reported as created: it may be a
	// root-owned system directory the caller asked for.
	if created, err = createMountPoint(fresh); err != nil {
		t.Fatalf("createMountPoint(%q) on existing dir: %v", fresh, err)
	}
	if created {
		t.Fatalf("createMountPoint(%q) = true, want false for an existing directory", fresh)
	}
}

// TestCreateMountPointHandlesTrailingSlash: a trailing slash must not make the
// parent creation swallow the mount point itself, which would leave a directory
// created for the workload owned by root.
func TestCreateMountPointHandlesTrailingSlash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mount") + "/"
	created, err := createMountPoint(path)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", path, err)
	}
	if !created {
		t.Fatalf("createMountPoint(%q) = false, want true", path)
	}
}

// TestCreateMountPointDoesNotAdoptSymlinks makes sure a symlink planted at the
// mount path is not reported as created, which would chown its target.
func TestCreateMountPointDoesNotAdoptSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "privileged")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	created, err := createMountPoint(link)
	if err != nil {
		t.Fatalf("createMountPoint(%q): %v", link, err)
	}
	if created {
		t.Fatalf("createMountPoint(%q) = true, want false for a symlink", link)
	}
}

// TestCreateMountPointRejectsFiles keeps a file from being used as a mount
// target, where it would previously have been chowned.
func TestCreateMountPointRejectsFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := createMountPoint(path); err == nil {
		t.Fatalf("createMountPoint(%q) = nil error, want a failure", path)
	}
}

// TestChownMountPointRefusesSymlinks verifies the chown cannot be redirected
// through a symlink swapped in after the directory was created.
func TestChownMountPointRefusesSymlinks(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "privileged")
	if err := os.Mkdir(target, 0755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := chownMountPoint(link, os.Getuid(), os.Getgid()); err == nil {
		t.Fatal("chownMountPoint followed a symlink, want a failure")
	}
}

func TestNormalizeDrivePath(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"root stays root", "/", "/"},
		{"missing leading slash", "sub", "/sub"},
		{"trailing slash trimmed", "/sub/", "/sub"},
		{"nested trailing slash trimmed", "/a/b/", "/a/b"},
		{"empty becomes root", "", "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeDrivePath(tt.input); got != tt.want {
				t.Errorf("normalizeDrivePath(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestMountLockForSameKey verifies the same mutex is returned for paths that
// clean to the same key, so concurrent mounts on the same target serialize.
func TestMountLockForSameKey(t *testing.T) {
	a := mountLockFor("/mnt/test")
	b := mountLockFor("/mnt/test/")
	c := mountLockFor("/mnt/other")
	if a != b {
		t.Errorf("expected identical lock for equivalent paths")
	}
	if a == c {
		t.Errorf("expected distinct locks for different paths")
	}
}

// TestMountLockForConcurrent ensures concurrent lookups for the same key never
// hand out two different mutexes (which would defeat serialization).
func TestMountLockForConcurrent(t *testing.T) {
	const n = 50
	locks := make([]*sync.Mutex, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			locks[idx] = mountLockFor("/mnt/concurrent")
		}(i)
	}
	wg.Wait()
	first := locks[0]
	for i := 1; i < n; i++ {
		if locks[i] != first {
			t.Fatalf("mountLockFor returned different mutexes for the same key")
		}
	}
}
