package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sandbox-api restores the environment from this file at startup and logs, as an
// error, every variable it could not. An image without it loses the customer's
// environment without anything failing.
func TestWriteEnvironmentFile(t *testing.T) {
	tree := t.TempDir()
	if err := writeEnvironmentFile(tree, []string{"FOO=bar", "PATH=/usr/bin"}, "/app"); err != nil {
		t.Fatal(err)
	}
	got := read(t, tree, envFilePath)

	for _, want := range []string{"FOO=bar", "PATH=/usr/bin", "HOME=/root", "PWD=/app"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

// What the image sets wins: adding a second HOME would leave the guest with two
// and the loser depending on parse order.
func TestWriteEnvironmentFileKeepsTheImageValues(t *testing.T) {
	tree := t.TempDir()
	if err := writeEnvironmentFile(tree, []string{"HOME=/blaxel", "PWD=/srv"}, "/app"); err != nil {
		t.Fatal(err)
	}
	got := read(t, tree, envFilePath)

	if strings.Count(got, "HOME=") != 1 || !strings.Contains(got, "HOME=/blaxel") {
		t.Errorf("HOME was overridden or duplicated:\n%s", got)
	}
	if strings.Count(got, "PWD=") != 1 || !strings.Contains(got, "PWD=/srv") {
		t.Errorf("PWD was overridden or duplicated:\n%s", got)
	}
}

// A file without u+r is unreadable in the guest and surfaces as a missing
// library, pointing nowhere near the cause.
func TestFixUnreadableFiles(t *testing.T) {
	tree := t.TempDir()
	bad := filepath.Join(tree, "lib.so")
	if err := os.WriteFile(bad, []byte("x"), 0o000); err != nil {
		t.Fatal(err)
	}
	good := filepath.Join(tree, "keep")
	if err := os.WriteFile(good, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	fixed, err := fixUnreadableFiles(tree)
	if err != nil {
		t.Fatal(err)
	}
	if fixed != 1 {
		t.Fatalf("fixed %d files, want 1", fixed)
	}
	if info, _ := os.Stat(bad); info.Mode().Perm()&0o400 == 0 {
		t.Error("the unreadable file is still unreadable")
	}
	// Modes that were already fine must not be widened.
	if info, _ := os.Stat(good); info.Mode().Perm() != 0o600 {
		t.Errorf("an untouched file changed mode to %v", info.Mode().Perm())
	}
}

// metamorph only creates these when absent — "This preserves permissions from
// the Docker image". Creating them unconditionally, which this builder did,
// overwrites a mode the image chose on purpose.
func TestEnsureRuntimeDirsPreservesTheImage(t *testing.T) {
	tree := t.TempDir()
	tmp := filepath.Join(tree, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tmp, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := ensureRuntimeDirs(tree); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(tmp); info.Mode().Perm() != 0o700 {
		t.Errorf("/tmp mode became %v, the image had chosen 0700", info.Mode().Perm())
	}
	// The ones the image did not provide still have to appear.
	if _, err := os.Stat(filepath.Join(tree, "proc")); err != nil {
		t.Errorf("a missing runtime directory was not created: %v", err)
	}
}

// An image is entitled to make one of these a symlink; replacing it with a
// directory would hide whatever it pointed at.
func TestEnsureRuntimeDirsLeavesASymlinkAlone(t *testing.T) {
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(tree, "tmp")); err != nil {
		t.Fatal(err)
	}

	if err := ensureRuntimeDirs(tree); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(tree, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the image's symlink was replaced by a directory")
	}
}
