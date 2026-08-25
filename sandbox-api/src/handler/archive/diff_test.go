package archive

import (
	"os"
	"path/filepath"
	"strings"
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

func TestDiffHandlesADirectoryOfTheImageReplacedByAFile(t *testing.T) {
	// The image has a directory where the sandbox now has a file. Walking the
	// image below it asks about paths whose parent is not a directory any more,
	// which fails with ENOTDIR rather than reporting them absent - and treating
	// that as an error failed the export of a perfectly ordinary change.
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(lower, "opt/tool/bin/tool"), "ELF", 0o755)
	write(t, filepath.Join(root, "opt/tool"), "a file now", 0o644)

	changes, err := Diff(root, lower, nil)
	if err != nil {
		t.Fatalf("Diff failed: %v", err)
	}
	if change := changeFor(changes, "opt/tool"); change == nil || change.Kind != ChangeModified {
		t.Errorf("expected the replaced directory to travel as a modified path, got %+v", change)
	}
	for _, change := range changes {
		if change.Kind == ChangeDeleted && strings.HasPrefix(change.Path, "opt/tool/") {
			t.Errorf("what the replaced directory held is not a deletion of its own, got %q", change.Path)
		}
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

func TestParseMountPointsKeepsOnlyPointsBelowTheRoot(t *testing.T) {
	// A mk3.0 sandbox: an overlay root, the pseudo-filesystems, a fuse filer.
	info := []byte(strings.Join([]string{
		"21 1 0:21 / / rw,relatime shared:1 - overlay overlay rw,lowerdir=/uk/rom,upperdir=/uk/rw/upper",
		"22 21 0:22 / /proc rw,nosuid - proc proc rw",
		"23 21 0:23 / /uk rw,relatime - fuse uk rw",
		"24 21 0:24 / /var/lib/kubelet/pods/x rw - tmpfs tmpfs rw",
		"25 21 0:25 / /run/blaxel\\040data rw - tmpfs tmpfs rw",
		"malformed line",
	}, "\n"))

	mounts := parseMountPoints(info, "/")

	for _, point := range []string{"/proc", "/uk", "/var/lib/kubelet/pods/x", "/run/blaxel data"} {
		if !mounts[point] {
			t.Errorf("expected %q to be pruned from the walk", point)
		}
	}
	// The root itself is walked, not pruned, or nothing would be archived.
	if mounts["/"] {
		t.Error("the root must not be pruned from its own walk")
	}
	if len(mounts) != 4 {
		t.Errorf("expected 4 mountpoints, got %v", mounts)
	}
}

func TestScanDeletedPrunesLiveMounts(t *testing.T) {
	// The image has a directory that a mount now covers in the live root: its
	// content belongs to the mounted filesystem, and the path is not a deletion.
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(lower, "data/from-image"), "image", 0o644)
	if err := os.MkdirAll(filepath.Join(root, "data"), 0o755); err != nil {
		t.Fatal(err)
	}

	s := &scanner{
		root:   root,
		lower:  lower,
		mounts: map[string]bool{filepath.Join(root, "data"): true},
	}
	changes, err := s.scanDeleted()
	if err != nil {
		t.Fatalf("scanDeleted failed: %v", err)
	}
	for _, change := range changes {
		if change.Path == "data" || strings.HasPrefix(change.Path, "data/") {
			t.Errorf("a path covered by a mount must not be reported as deleted, got %s", change.Path)
		}
	}
}
