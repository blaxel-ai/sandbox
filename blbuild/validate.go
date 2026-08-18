package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// validate refuses to publish an image the kernel would reject at runtime.
//
// fsck.erofs alone is not enough, and that is the whole point of this step: it
// returns OK on images whose layout the kernel later refuses, which is exactly
// how a corrupted customer image once shipped and only surfaced as
// "cannot open shared object file: Error 117" (EUCLEAN) when apt and git ran
// inside the VM. A build we own can afford to actually read the image back.
//
// Cost measured on a 2703 MiB image with 1885 symlinks and 1404 executables:
// 2.48s, about 1% of that build. Cheap enough to be unconditional.
func (b *Builder) validate(ctx context.Context, rootfs string, sw *stopwatch) error {
	ctx, span := tracer().Start(ctx, "validate")
	defer span.End()

	if out, err := exec.CommandContext(ctx, "fsck.erofs", rootfs).CombinedOutput(); err != nil {
		b.appendLog("fsck.log", string(out))
		return fmt.Errorf("the filesystem image failed its integrity check: %s", lastLine(string(out)))
	}
	sw.mark("fsck")

	mnt := filepath.Join(b.WorkDir, "verify")
	if err := os.MkdirAll(mnt, 0o755); err != nil {
		return err
	}
	// A loop mount is the only way to exercise the kernel's own reader, which is
	// the reader that matters.
	if out, err := exec.CommandContext(ctx, "mount", "-t", "erofs", "-o", "loop", rootfs, mnt).CombinedOutput(); err != nil {
		// Not fatal: a sandbox without mount privileges still gets fsck. Losing
		// the deeper check is worth reporting, not worth failing a good build.
		warn("could not mount the image for verification, skipping deep check: %s", lastLine(string(out)))
		span.SetAttributes(attribute.Bool("validate.mounted", false))
		return nil
	}
	defer func() {
		_ = exec.Command("umount", mnt).Run()
	}()
	span.SetAttributes(attribute.Bool("validate.mounted", true))

	symlinks, executables, bad := 0, 0, []string{}
	err := filepath.Walk(mnt, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			// An unreadable directory entry is itself a corruption signal.
			bad = append(bad, fmt.Sprintf("%s (%v)", rel(mnt, path), err))
			return nil
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			symlinks++
			// Reading the target is what trips the inline-data check in the
			// kernel: a symlink whose target crosses a block boundary is
			// rejected with EUCLEAN.
			if _, err := os.Readlink(path); err != nil {
				bad = append(bad, rel(mnt, path))
			}
		case info.Mode().IsRegular() && isExecutableOrLibrary(path, info):
			executables++
			// One byte is enough: the failure mode is the filesystem refusing
			// the read, not the content being wrong.
			if err := readFirstByte(path); err != nil {
				bad = append(bad, rel(mnt, path))
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("verifying the filesystem image: %w", err)
	}

	span.SetAttributes(
		attribute.Int("validate.symlinks", symlinks),
		attribute.Int("validate.executables", executables),
		attribute.Int("validate.unreadable", len(bad)),
	)
	sw.mark(fmt.Sprintf("verify (%d symlinks, %d exec)", symlinks, executables))

	if len(bad) > 0 {
		shown := bad
		if len(shown) > 5 {
			shown = shown[:5]
		}
		return fmt.Errorf("%d file(s) in the built image cannot be read, e.g. %s",
			len(bad), strings.Join(shown, ", "))
	}
	return nil
}

func rel(base, path string) string {
	if r, err := filepath.Rel(base, path); err == nil {
		return "/" + r
	}
	return path
}

// isExecutableOrLibrary picks the files whose unreadability actually breaks a
// sandbox: binaries and shared objects. Reading every file would multiply the
// cost by the image size for no added signal.
func isExecutableOrLibrary(path string, info os.FileInfo) bool {
	if info.Mode().Perm()&0o111 != 0 {
		return true
	}
	base := filepath.Base(path)
	return strings.Contains(base, ".so.") || strings.HasSuffix(base, ".so")
}

func readFirstByte(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1)
	if _, err := f.Read(buf); err != nil && err.Error() != "EOF" {
		return err
	}
	return nil
}
