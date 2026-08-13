package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// mkfs flags, split so each group can be explained on its own.
var (
	// erofsBase applies to every invocation. noinline_data keeps file data out
	// of inodes; note it does NOT cover symlink targets, which erofs stores
	// inline by design.
	erofsBase = []string{"--all-root", "-E", "noinline_data"}

	// erofsTar feeds mkfs a tar stream instead of a directory tree.
	//
	// --aufs and --ovlfs-strip=1 are mandatory, not tuning: OCI whiteouts are
	// aufs .wh.* markers, and --tar=f on its own keeps BOTH the marker and the
	// file it was supposed to delete. That is the bug that leaves `RUN rm
	// secret` in the shipped image and inflates every layer above it.
	erofsTar = []string{"--tar=f", "--aufs", "--ovlfs-strip=1", "--sort=none"}

	// erofsIncremental appends a layer to an existing image. The value is
	// required on 1.8.x/1.9.x — bare --incremental is rejected.
	erofsIncremental = []string{"--incremental=data"}
)

// buildErofs turns the OCI layers into a single rootfs image.
//
// Layer order matters twice over. Blaxel's own binaries go in FIRST, as a
// non-incremental base layer: applied last, mkfs.erofs 1.9.3 fails with "no
// enough room for the root inode" and a bogus ENOSPC after trying to extend the
// image to 2TiB. It is not about size — a 523MiB build layer applies
// incrementally without trouble — but about the shape of the tar: one 170MB file
// and almost no entries. Reproduced with 15.9GB free on the volume.
//
// The consequence of that ordering is that a customer layer writing to those
// exact paths now wins over ours. In practice nothing touches
// /usr/local/bin/blfs.
func (b *Builder) buildErofs(ctx context.Context, layers []Layer, sw *stopwatch) (incremental bool, err error) {
	ctx, span := tracer().Start(ctx, "erofs")
	defer span.End()

	rootfs := filepath.Join(b.OutDir, "rootfs.erofs")
	_ = os.Remove(rootfs)

	// Extract to a tree and run one mkfs over it, which is how metamorph does it.
	//
	// Appending layers incrementally was faster on paper, but a tar layer cannot
	// follow a symlink — it replaces it. Our own binaries have to land where the
	// execution plane reads them (/bin/metamorph-wrapper), and on a usrmerge
	// image that path is a symlink into /usr/bin: applied before the layers the
	// wrapper is lost, applied after it turns /bin into a directory and hides
	// every binary the image had there. A filesystem copy onto the extracted tree
	// has neither problem.
	//
	// It also drops the dependency on --incremental, flagged EXPERIMENTAL upstream
	// and already broken in two different ways across versions. The measured cost
	// is about 1.4s on a ~75s build.
	if err := b.buildErofsFromTree(ctx, layers, rootfs, sw); err != nil {
		return false, err
	}
	return false, nil
}

// applyLayer streams one compressed layer into mkfs.
//
// The stream goes through a named FIFO rather than the process's stdin: mkfs
// refuses /dev/stdin as a tar source ("failed to parse source directory"), while
// a FIFO gives identical streaming with no intermediate tar on disk.
func (b *Builder) applyLayer(ctx context.Context, rootfs string, layer Layer) error {
	fifo := filepath.Join(b.WorkDir, "layer.fifo")
	_ = os.Remove(fifo)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		return fmt.Errorf("creating the layer pipe: %w", err)
	}
	defer os.Remove(fifo)

	decompress, err := decompressorFor(layer.MediaType)
	if err != nil {
		return err
	}

	// The writer must run concurrently with mkfs: opening a FIFO for writing
	// blocks until a reader shows up, and vice versa.
	writeErr := make(chan error, 1)
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			writeErr <- err
			return
		}
		defer f.Close()
		cmd := exec.CommandContext(ctx, decompress[0], append(decompress[1:], layer.Path)...)
		cmd.Stdout = f
		cmd.Stderr = os.Stderr
		writeErr <- cmd.Run()
	}()

	mkfsErr := b.mkfs(ctx, rootfs, fifo, true)
	if err := <-writeErr; err != nil && mkfsErr == nil {
		return fmt.Errorf("decompressing the layer: %w", err)
	}
	return mkfsErr
}

// mkfs runs mkfs.erofs over a tar source.
func (b *Builder) mkfs(ctx context.Context, rootfs, source string, incremental bool) error {
	args := append([]string{}, erofsBase...)
	args = append(args, fmt.Sprintf("--workers=%d", workers()))
	args = append(args, erofsTar...)
	if incremental {
		args = append(args, erofsIncremental...)
	}
	args = append(args, rootfs, source)

	cmd := exec.CommandContext(ctx, "mkfs.erofs", args...)
	out, err := cmd.CombinedOutput()
	b.appendLog("mkfs.log", string(out))
	if err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(string(out)))
	}
	return nil
}

// buildErofsFromTree is the fallback: extract every layer in order into a tree,
// applying whiteouts as we go, then run a single mkfs over the result. This is
// the shape metamorph uses today, so it is the well-trodden path.
func (b *Builder) buildErofsFromTree(ctx context.Context, layers []Layer, rootfs string, sw *stopwatch) error {
	ctx, span := tracer().Start(ctx, "erofs.fallback")
	defer span.End()

	tree := filepath.Join(b.WorkDir, "tree")
	if err := os.RemoveAll(tree); err != nil {
		return err
	}
	if err := os.MkdirAll(tree, 0o755); err != nil {
		return err
	}
	for _, layer := range layers {
		if err := b.extractLayer(ctx, layer, tree); err != nil {
			return err
		}
		// Whiteouts are resolved after each layer, which is what makes the
		// order correct: a marker only masks what the layers below it put there.
		if err := applyWhiteouts(tree); err != nil {
			return err
		}
	}
	// After the layers, never before: this is a filesystem copy, so it follows
	// whatever /bin ended up being. A usrmerge image ships /bin as a symlink to
	// /usr/bin, and the copy lands in /usr/bin with the symlink intact — exactly
	// what metamorph produces. Going in first instead created /bin as a real
	// directory, the image's symlink replaced it, and the wrapper vanished:
	// every such image booted to "/bin/metamorph-wrapper: -2" and rebooted.
	if err := b.extractPrelude(tree); err != nil {
		return err
	}
	if err := ensureRuntimeDirs(tree); err != nil {
		return err
	}

	// The image's own environment, where the guest can read it back: sandbox-api
	// restores it at startup and reports as an error every variable it could not.
	// Without this file the customer's environment is silently lost.
	var cfg ociConfig
	if err := readJSON(blobPath(b.ociDir, b.configDigest), &cfg); err != nil {
		return fmt.Errorf("reading the image configuration: %w", err)
	}
	if err := writeEnvironmentFile(tree, cfg.Config.Env, cfg.Config.WorkingDir); err != nil {
		return fmt.Errorf("writing the environment file: %w", err)
	}

	// A file the image left without u+r is unreadable in the guest, and surfaces
	// as a missing library rather than as a permission problem.
	fixed, err := fixUnreadableFiles(tree)
	if err != nil {
		return fmt.Errorf("fixing unreadable files: %w", err)
	}
	if fixed > 0 {
		sw.mark(fmt.Sprintf("  made %d file(s) readable", fixed))
	}
	sw.mark("extract layers to tree")

	_ = os.Remove(rootfs)
	args := append([]string{}, erofsBase...)
	args = append(args, fmt.Sprintf("--workers=%d", workers()), rootfs, tree)
	cmd := exec.CommandContext(ctx, "mkfs.erofs", args...)
	out, err := cmd.CombinedOutput()
	b.appendLog("mkfs.log", string(out))
	if err != nil {
		return fmt.Errorf("%w: %s", err, lastLine(string(out)))
	}
	sw.mark("single-pass mkfs")
	return nil
}

// applyWhiteouts deletes what the aufs markers of the layer just extracted are
// masking, then removes the markers. In tree mode mkfs has no --aufs equivalent,
// so this is where the whiteout bug is avoided: BOTH the marker and its target
// have to go, and metamorph historically dropped only the marker.
func applyWhiteouts(tree string) error {
	var markers []string
	err := filepath.Walk(tree, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !strings.HasPrefix(filepath.Base(path), ".wh.") {
			return nil
		}
		markers = append(markers, path)
		return nil
	})
	if err != nil {
		return err
	}

	for _, marker := range markers {
		dir := filepath.Dir(marker)
		base := filepath.Base(marker)
		if base == ".wh..wh..opq" {
			// Opaque directory: everything inherited from lower layers is
			// hidden. Whatever this layer itself added is already on disk
			// alongside the marker, so only siblings older than the marker can
			// be removed — which we cannot tell apart here. Removing the marker
			// alone matches the common case (an empty dir replacing a
			// populated one is rare) and never deletes data the layer wanted.
			if err := os.Remove(marker); err != nil {
				return err
			}
			continue
		}
		target := filepath.Join(dir, strings.TrimPrefix(base, ".wh."))
		if err := os.RemoveAll(target); err != nil {
			return err
		}
		if err := os.Remove(marker); err != nil {
			return err
		}
	}
	return nil
}

func decompressorFor(mediaType string) ([]string, error) {
	switch {
	case strings.HasSuffix(mediaType, "zstd"):
		return []string{"zstd", "-dc"}, nil
	case strings.HasSuffix(mediaType, "gzip"):
		return []string{"gzip", "-dc"}, nil
	case strings.HasSuffix(mediaType, ".tar"):
		return []string{"cat"}, nil
	default:
		return nil, fmt.Errorf("unsupported layer format %q", mediaType)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
