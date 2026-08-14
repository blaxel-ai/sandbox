package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Rootfs slimming, as metamorph does it.
//
// The list is metamorph's CLEANUP_PATTERNS, in the same order and with the same
// omissions — the locale entries are commented out there on purpose ("We don't
// want to clean that"), so they stay out here too. An image is expected to lose
// the same things whichever builder produced it; removing more would break
// workloads that work today, removing less would ship a fatter image than the
// customer had yesterday.
//
// Slimming is skipped when blaxel.toml says [build] slim = false.
var cleanupPatterns = []string{
	// Package manager caches
	"var/cache/apk/*",
	"var/cache/apt/*",
	"var/cache/yum/*",
	"var/cache/dnf/*",
	"var/lib/apt/lists/*",
	// Documentation
	"usr/share/doc/*",
	"usr/share/man/*",
	"usr/share/info/*",
	"usr/share/gtk-doc/*",
	// Development files
	"usr/include/*",
	"usr/lib/*.a",
	"usr/local/include/*",
	"usr/local/lib/*.a",
	"usr/src/*",
	// Build tools
	"usr/bin/gcc*",
	"usr/bin/g++*",
	"usr/bin/make",
	"usr/bin/cmake",
	"usr/bin/autoconf",
	"usr/bin/automake",
	// Systemd, which a microVM guest does not run
	"lib/systemd/*",
	"usr/lib/systemd/*",
	"etc/systemd/*",
	// Python build artefacts
	"**/__pycache__",
	"**/*.pyc",
	"**/*.pyo",
	"usr/lib/python*/test",
	// Package manager state
	"var/lib/dpkg/*",
	// Scratch space
	"tmp/*",
}

// slimRootfs removes what the patterns match and reports how many entries went.
//
// Failures are not fatal: an image that keeps a cache directory is worse than an
// image that ships, and the alternative is failing a build over a file we were
// only trying to delete.
func slimRootfs(tree string) int {
	removed := 0
	var anywhere []string
	for _, pattern := range cleanupPatterns {
		if name, ok := strings.CutPrefix(pattern, "**/"); ok {
			anywhere = append(anywhere, name)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(tree, pattern))
		if err != nil {
			continue
		}
		for _, m := range matches {
			// Never step outside the tree, whatever a pattern or a symlink says.
			if rel, err := filepath.Rel(tree, m); err != nil || strings.HasPrefix(rel, "..") {
				continue
			}
			if os.RemoveAll(m) == nil {
				removed++
			}
		}
	}
	return removed + removeAnywhere(tree, anywhere)
}

// removeAnywhere handles the **/ patterns, which Go's Glob does not, in a single
// pass over the tree.
//
// One walk for all of them, not one walk each: the three Python patterns
// (__pycache__, *.pyc, *.pyo) meant three complete traversals of the rootfs, and
// a Node image has none of those files to find. Measured on a 700 MiB image with
// a large node_modules, slimming spent 5.2s to delete 2 entries, while a
// different image deleted 257 in 2.0s — the cost was never the deletion, it was
// walking hundreds of thousands of inodes three times over.
//
// WalkDir rather than Walk for the same reason: Walk calls lstat on every entry
// to build a FileInfo nothing here needs.
func removeAnywhere(tree string, names []string) int {
	if len(names) == 0 {
		return 0
	}
	var victims []string
	_ = filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		for _, name := range names {
			if ok, _ := filepath.Match(name, base); ok {
				victims = append(victims, path)
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		return nil
	})
	removed := 0
	for _, v := range victims {
		if os.RemoveAll(v) == nil {
			removed++
		}
	}
	return removed
}
