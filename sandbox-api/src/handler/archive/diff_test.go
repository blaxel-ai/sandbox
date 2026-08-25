package archive

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeSandbox builds two directories standing in for the live root and the
// pristine image: the tests compare plain directories, so a copied-up file that
// overlay would give a new inode is naturally a distinct inode here too.
func fakeSandbox(t *testing.T) (root, lower string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	lower = filepath.Join(base, "lower")
	for _, dir := range []string{root, lower} {
		if err := os.MkdirAll(filepath.Join(dir, "etc"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root, lower
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func changeFor(changes []Change, path string) *Change {
	for i := range changes {
		if changes[i].Path == path {
			return &changes[i]
		}
	}
	return nil
}

func TestDiffClassifiesChanges(t *testing.T) {
	root, lower := fakeSandbox(t)
	stamp := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Untouched: same content, same metadata, different inode. A workload that
	// merely opened a file for write must not drag the whole file into the
	// archive.
	write(t, filepath.Join(lower, "etc/passwd"), "root:x:0:0", 0o644)
	write(t, filepath.Join(root, "etc/passwd"), "root:x:0:0", 0o644)
	for _, dir := range []string{root, lower} {
		if err := os.Chtimes(filepath.Join(dir, "etc/passwd"), stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	// Added.
	write(t, filepath.Join(root, "usr/bin/curl"), "ELF", 0o755)
	// Modified content, same size: only the mtime tells them apart.
	write(t, filepath.Join(lower, "etc/motd"), "hello", 0o644)
	write(t, filepath.Join(root, "etc/motd"), "HELLO", 0o644)
	// Modified permissions only.
	write(t, filepath.Join(lower, "etc/script.sh"), "#!/bin/sh", 0o644)
	write(t, filepath.Join(root, "etc/script.sh"), "#!/bin/sh", 0o755)
	// Deleted.
	write(t, filepath.Join(lower, "etc/rsyncd.conf"), "conf", 0o644)
	// Symlink retargeted.
	if err := os.Symlink("busybox", filepath.Join(lower, "bin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("bash", filepath.Join(root, "bin")); err != nil {
		t.Fatal(err)
	}
	// Excluded: the sandbox's own network configuration never travels.
	write(t, filepath.Join(lower, "etc/resolv.conf"), "nameserver 1.1.1.1", 0o644)
	write(t, filepath.Join(root, "etc/resolv.conf"), "nameserver 10.0.0.1", 0o644)

	changes, err := Diff(root, lower, DefaultExcludes)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	expected := map[string]ChangeKind{
		"usr":             ChangeAdded,
		"usr/bin":         ChangeAdded,
		"usr/bin/curl":    ChangeAdded,
		"etc/motd":        ChangeModified,
		"etc/script.sh":   ChangeModified,
		"bin":             ChangeModified,
		"etc/rsyncd.conf": ChangeDeleted,
	}
	for path, kind := range expected {
		change := changeFor(changes, path)
		if change == nil {
			t.Errorf("expected %s to be reported as %s, it is absent", path, kind)
			continue
		}
		if change.Kind != kind {
			t.Errorf("expected %s to be %s, got %s", path, kind, change.Kind)
		}
	}
	for _, path := range []string{"etc/passwd", "etc", "etc/resolv.conf"} {
		if change := changeFor(changes, path); change != nil {
			t.Errorf("expected %s not to be reported, got %s", path, change.Kind)
		}
	}
	if size := changeFor(changes, "usr/bin/curl").Size; size != 3 {
		t.Errorf("expected the added file to record its size, got %d", size)
	}
}

func TestDiffReportsDeletedDirectoryOnce(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(lower, "opt/tool/bin/tool"), "ELF", 0o755)
	write(t, filepath.Join(lower, "opt/tool/README"), "doc", 0o644)

	changes, err := Diff(root, lower, nil)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}

	var deleted []string
	for _, change := range changes {
		if change.Kind == ChangeDeleted {
			deleted = append(deleted, change.Path)
		}
	}
	if len(deleted) != 1 || deleted[0] != "opt" {
		t.Errorf("expected the deleted subtree to be reported as its top path, got %v", deleted)
	}
}

func TestDiffHonoursExtraExcludes(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "workspace/secret.env"), "TOKEN=1", 0o600)
	write(t, filepath.Join(root, "workspace/main.go"), "package main", 0o644)

	changes, err := Diff(root, lower, []string{"workspace/secret.env"})
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if changeFor(changes, "workspace/secret.env") != nil {
		t.Error("expected the excluded path to be left out of the archive")
	}
	if changeFor(changes, "workspace/main.go") == nil {
		t.Error("expected the rest of the directory to be archived")
	}
}
