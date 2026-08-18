package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// The treatments metamorph applies to the extracted tree before it becomes an
// image. Each of these is silent when missing: the image builds, and the
// difference only shows at boot or in use.
//
// Ported from metamorph rather than invented, for the same reason the Dockerfile
// templates are: an image has to behave the same whichever builder produced it.

// envFilePath is where sandbox-api reads the environment back. Its main() calls
// envfile.Load() at startup and logs, as an error, every variable it could not
// restore — so an image without this file quietly loses the customer's
// environment.
const envFilePath = "etc/default/metamorph-env"

// writeEnvironmentFile records the image environment where the guest can read it,
// adding HOME and PWD when the image sets neither, as metamorph does.
func writeEnvironmentFile(tree string, env []string, workingDir string) error {
	path := filepath.Join(tree, envFilePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	var b strings.Builder
	if !hasPrefix(env, "HOME=") {
		b.WriteString("HOME=/root\n")
	}
	if !hasPrefix(env, "PWD=") {
		if workingDir == "" {
			workingDir = "/"
		}
		fmt.Fprintf(&b, "PWD=%s\n", workingDir)
	}
	for _, e := range env {
		b.WriteString(e)
		b.WriteString("\n")
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}

// fixUnreadableFiles makes every regular file readable by its owner.
//
// A file the image left without u+r is unreadable in the guest, which surfaces
// as a missing library or a binary that will not start — a failure that points
// nowhere near its cause.
func fixUnreadableFiles(tree string) (int, error) {
	fixed := 0
	err := filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// A tree extracted from arbitrary layers can contain entries we
			// cannot stat; none of them is worth failing a build over.
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		mode := info.Mode().Perm()
		if mode&0o400 != 0 {
			return nil
		}
		if err := os.Chmod(path, mode|0o400); err != nil {
			return nil
		}
		fixed++
		return nil
	})
	return fixed, err
}

// ensureRuntimeDirs creates what the guest needs and nothing more.
//
// Only when absent, deliberately: metamorph notes "This preserves permissions
// from the Docker image", and an image that sets its own mode on /tmp has a
// reason to. Creating them unconditionally and chmod-ing afterwards — which is
// what this builder did — overwrites that choice.
func ensureRuntimeDirs(tree string) error {
	for _, d := range runtimeDirs {
		path := filepath.Join(tree, d.path)
		if _, err := os.Lstat(path); err == nil {
			// Present already — including as a symlink, which an image is
			// entitled to use and which we must not replace.
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return err
		}
		// The raw POSIX mode, so the sticky bit survives.
		if err := syscall.Chmod(path, uint32(d.mode)); err != nil {
			return err
		}
	}
	return nil
}
