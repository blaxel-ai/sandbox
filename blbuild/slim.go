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

// Directories the **/ patterns are not worth descending into.
//
// Every remaining **/ pattern looks for Python bytecode (__pycache__, *.pyc,
// *.pyo), and these two directories hold none: node_modules is a JavaScript
// dependency tree and .git is object storage. They are also, on a typical image,
// most of the inodes in the rootfs — slimming a 700 MiB Node image spent 4.4s to
// delete two files, essentially all of it walking node_modules.
//
// .venv is the deliberate exception: it is full of the bytecode these patterns
// exist for, so skipping it does keep .pyc files an image would otherwise lose.
// That is the intended trade — the rootfs is a read-only EROFS, where a stripped
// .pyc cannot be regenerated and is recompiled on every start instead of once.
var slimSkipDirs = map[string]struct{}{
	"node_modules": {},
	".git":         {},
	".venv":        {},
}

// removeAnywhere handles the **/ patterns, which Go's Glob does not, in a single
// pass over the tree.
//
// One walk for all of them, not one walk each: the three Python patterns meant
// three complete traversals, and a Node image has none of those files to find.
// metamorph is looser still — it shells out to `find` twice per pattern, once to
// count and once to delete — so skipping work here does not make the two
// builders disagree on the result.
//
// WalkDir rather than Walk for the same reason: Walk calls lstat on every entry
// to build a FileInfo nothing here reads.
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
		// Checked after the patterns so a directory that is itself a match is
		// still removed, whatever its name.
		if d.IsDir() && path != tree {
			if _, skip := slimSkipDirs[base]; skip {
				return filepath.SkipDir
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
