package archive

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRestoringThroughAHeldDirectoryIgnoresADirectorySwappedForASymlink is the
// race a terminal opened during a restore can run: the workload's user turns a
// directory of a member's path into a symlink after it has been checked, so the
// root-privileged write that follows lands wherever the link points.
func TestRestoringThroughAHeldDirectoryIgnoresADirectorySwappedForASymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.MkdirAll(filepath.Join(root, "home", "app"), 0o755); err != nil {
		t.Fatalf("failed to build the tree the archive is restored into: %v", err)
	}

	parent, _, err := openParent(root, "home/app/config", true)
	if err != nil {
		t.Fatalf("failed to open the directory the member belongs in: %v", err)
	}
	defer parent.Close()

	// The swap, between the check and the write.
	if err := os.Rename(filepath.Join(root, "home", "app"), filepath.Join(root, "home", "moved")); err != nil {
		t.Fatalf("failed to move the directory the terminal replaces: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "home", "app")); err != nil {
		t.Fatalf("failed to put the symlink the terminal replaces it with: %v", err)
	}

	if _, err := writeFile(parent.path(), "home/app/config", 0o644, strings.NewReader("restored")); err != nil {
		t.Fatalf("failed to restore the member: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(outside, "config")); err == nil {
		t.Fatal("the member was restored through the symlink, outside the root")
	}
	if _, err := os.Lstat(filepath.Join(root, "home", "moved", "config")); err != nil {
		t.Fatalf("the member was not restored into the directory that was checked: %v", err)
	}
}

// TestOpeningAParentRefusesASymlinkedComponent keeps the check the restore
// starts from: a member is never written through a link the image, or an
// earlier member, left on the way.
func TestOpeningAParentRefusesASymlinkedComponent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	if err := os.Symlink(outside, filepath.Join(root, "etc")); err != nil {
		t.Fatalf("failed to put the symlink the member is restored through: %v", err)
	}

	if _, _, err := openParent(root, "etc/resolv.conf", true); err == nil {
		t.Fatal("a member restored through a symlink should be refused")
	}
	if _, err := os.Lstat(filepath.Join(outside, "resolv.conf")); err == nil {
		t.Fatal("the member was created outside the root")
	}
}
