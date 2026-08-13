package main

import (
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
	for _, pattern := range cleanupPatterns {
		if strings.HasPrefix(pattern, "**/") {
			removed += removeAnywhere(tree, strings.TrimPrefix(pattern, "**/"))
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
	return removed
}

// removeAnywhere handles the **/ patterns, which Go's Glob does not.
func removeAnywhere(tree, name string) int {
	removed := 0
	var victims []string
	_ = filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if ok, _ := filepath.Match(name, filepath.Base(path)); ok {
			victims = append(victims, path)
			if info.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	for _, v := range victims {
		if os.RemoveAll(v) == nil {
			removed++
		}
	}
	return removed
}
