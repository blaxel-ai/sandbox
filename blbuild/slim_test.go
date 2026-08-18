package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The three Python patterns used to cost one full traversal each, so a Node
// image with no .pyc anywhere still paid three complete walks of its
// node_modules — measured at 5.2s to delete 2 entries, while another image
// deleted 257 in 2.0s. The deletion was never the cost.
func TestSlimRemovesNestedMatchesInOnePass(t *testing.T) {
	tree := t.TempDir()
	mk := func(path string, dir bool) string {
		full := filepath.Join(tree, path)
		if dir {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}
			return full
		}
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	// All three **/ patterns, at different depths, plus files that must survive.
	cache := mk("usr/lib/python3/site-packages/__pycache__", true)
	pyc := mk("opt/app/mod.pyc", false)
	pyo := mk("opt/app/deep/nested/mod.pyo", false)
	keep := mk("opt/app/main.py", false)
	keepDir := mk("opt/app/src", true)

	if n := slimRootfs(tree); n < 3 {
		t.Errorf("removed %d entries, want at least the 3 nested matches", n)
	}
	for _, gone := range []string{cache, pyc, pyo} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s survived slimming", gone)
		}
	}
	for _, alive := range []string{keep, keepDir} {
		if _, err := os.Stat(alive); err != nil {
			t.Errorf("%s was deleted but should not have been: %v", alive, err)
		}
	}
}

// A tree with nothing to delete must still come out intact — the common case for
// a Node image, and the one that used to be most expensive.
func TestSlimLeavesAnUnrelatedTreeAlone(t *testing.T) {
	tree := t.TempDir()
	for _, p := range []string{"blaxel/package.json", "blaxel/node_modules/x/index.js", "blaxel/dist/server.js"} {
		full := filepath.Join(tree, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	slimRootfs(tree)
	for _, p := range []string{"blaxel/package.json", "blaxel/node_modules/x/index.js", "blaxel/dist/server.js"} {
		if _, err := os.Stat(filepath.Join(tree, p)); err != nil {
			t.Errorf("%s was deleted: %v", p, err)
		}
	}
}

// The **/ patterns only look for Python bytecode, and these directories hold
// none while dominating the inode count — walking a 700 MiB node_modules to
// delete two files was 4.4s of the build.
func TestSlimSkipsDirectoriesWithNothingToFind(t *testing.T) {
	tree := t.TempDir()
	write := func(path string) string {
		full := filepath.Join(tree, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return full
	}

	// Inside a skipped directory: kept, on purpose.
	inNode := write("blaxel/node_modules/pkg/build/stale.pyc")
	inGit := write("blaxel/.git/objects/thing.pyc")
	inVenv := write("blaxel/.venv/lib/python3.12/site-packages/mod.pyc")
	// Outside: still removed.
	loose := write("blaxel/app/mod.pyc")

	slimRootfs(tree)

	for _, kept := range []string{inNode, inGit, inVenv} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("%s was deleted, the directory should not have been walked: %v", kept, err)
		}
	}
	if _, err := os.Stat(loose); !os.IsNotExist(err) {
		t.Errorf("%s survived but is outside any skipped directory", loose)
	}
}

// A directory that is itself a match must go even if it sits at a skipped name's
// level: the pattern check runs before the skip.
func TestSlimStillRemovesAMatchingDirectory(t *testing.T) {
	tree := t.TempDir()
	cache := filepath.Join(tree, "srv/app/__pycache__")
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	slimRootfs(tree)
	if _, err := os.Stat(cache); !os.IsNotExist(err) {
		t.Error("__pycache__ survived")
	}
}
