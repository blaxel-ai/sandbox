package main

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// Builder runs one build.
type Builder struct {
	Targets    *Targets
	WorkDir    string
	OutDir     string
	ContextDir string
	NoUpload   bool

	// Filled in while reading the OCI export.
	ociDir       string
	configDigest string
}

// Layer is one OCI layer of the built image, in application order.
type Layer struct {
	Digest    string
	MediaType string
	Size      int64
	Path      string
}

// Paths of the artefacts, relative to OutDir. They mirror the kraft/ layout the
// compute plane expects, so the set stays interchangeable with the one the
// external builder produces today.
const (
	rootfsName  = "rootfs.erofs"
	kernelName  = "kernel"
	cmdlineName = "cmdline.txt"
	configName  = "config.json"
	imageName   = "image.json"
)

// Run executes the pipeline and always returns a Result when it got far enough
// to have one, so a failure still carries its timings.
func (b *Builder) Run(ctx context.Context) (*Result, error) {
	ctx, span := tracer().Start(ctx, "build")
	defer span.End()
	span.SetAttributes(buildAttributes(b.Targets)...)

	if err := b.checkExpiry(); err != nil {
		return nil, err
	}
	for _, d := range []string{b.WorkDir, b.OutDir} {
		if err := os.RemoveAll(d); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}

	sw := newStopwatch()
	result := &Result{
		UploadID: b.Targets.Initrd.UploadID,
		Key:      b.Targets.Initrd.Key,
	}

	contextDir, err := b.fetchContext(ctx, sw)
	if err != nil {
		return result, err
	}

	layers, err := b.runBuildkit(ctx, contextDir, sw)
	if err != nil {
		return result, err
	}
	result.Layers = len(layers)
	span.SetAttributes(attribute.Int("build.layers", len(layers)))

	incremental, err := b.buildErofs(ctx, layers, sw)
	if err != nil {
		return result, err
	}
	result.Incremental = incremental

	rootfs := filepath.Join(b.OutDir, rootfsName)
	info, err := os.Stat(rootfs)
	if err != nil {
		return result, fmt.Errorf("the filesystem image was not produced: %w", err)
	}
	result.RootfsBytes = info.Size()
	span.SetAttributes(attribute.Int64("build.rootfs_bytes", info.Size()))

	if err := b.validate(ctx, rootfs, sw); err != nil {
		return result, err
	}

	if err := b.writeKraftFiles(ctx, sw); err != nil {
		return result, err
	}

	if b.NoUpload {
		result.TotalSeconds = sw.total()
		result.Steps = sw.steps
		return result, nil
	}

	if err := b.uploadSmallFiles(ctx, sw); err != nil {
		return result, err
	}
	parts, err := b.uploadRootfs(ctx, rootfs, sw)
	if err != nil {
		return result, err
	}
	result.Parts = parts
	result.TotalSeconds = sw.total()
	result.Steps = sw.steps
	return result, nil
}

// checkExpiry fails before doing any work if the URLs are already dead, which
// turns a wall of 403s at the end of a long build into one clear message.
func (b *Builder) checkExpiry() error {
	if b.Targets.ExpiresAt == 0 {
		return nil
	}
	expiry := time.UnixMilli(b.Targets.ExpiresAt)
	if time.Now().After(expiry) {
		return fmt.Errorf("the upload authorization expired at %s", expiry.UTC().Format(time.RFC3339))
	}
	return nil
}

// fetchContext downloads and unpacks the build context, or reuses a local
// directory when -context was passed.
func (b *Builder) fetchContext(ctx context.Context, sw *stopwatch) (string, error) {
	if b.ContextDir != "" {
		sw.mark("reuse local context")
		return b.ContextDir, nil
	}

	ctx, span := tracer().Start(ctx, "fetch_source")
	defer span.End()

	archive := filepath.Join(b.WorkDir, "source.zip")
	if err := download(ctx, b.Targets.SourceURL, archive); err != nil {
		return "", fmt.Errorf("downloading the build context: %w", err)
	}
	if info, err := os.Stat(archive); err == nil {
		span.SetAttributes(attribute.Int64("source.bytes", info.Size()))
	}
	sw.mark("download context")

	dir := filepath.Join(b.WorkDir, "ctx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "unzip", "-oq", archive, "-d", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("unpacking the build context: %w: %s", err, lastLine(string(out)))
	}
	sw.mark("unpack context")
	return dir, nil
}

// runBuildkit builds the image and returns its layers in application order.
//
// zstd with force-compression: the erofs step decompresses each layer straight
// into mkfs, and zstd is markedly faster than gzip on that path. The compute
// plane never sees a compressed layer, so this is a transport choice only.
func (b *Builder) runBuildkit(ctx context.Context, contextDir string, sw *stopwatch) ([]Layer, error) {
	ctx, span := tracer().Start(ctx, "buildkit")
	defer span.End()

	ociTar := filepath.Join(b.WorkDir, "img.tar")
	output := fmt.Sprintf(
		"type=oci,dest=%s,compression=zstd,force-compression=true,oci-mediatypes=true", ociTar)

	cmd := exec.CommandContext(ctx, "buildctl", "build",
		"--progress=plain",
		"--frontend", "dockerfile.v0",
		"--local", "context="+contextDir,
		"--local", "dockerfile="+contextDir,
		"--output", output,
	)
	// The build log is what the customer reads in the CLI, so it is streamed
	// through unchanged rather than summarized.
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Deliberately not wrapped with the exit status: buildkit already
		// printed the failing step, and "exit status 1" adds nothing.
		return nil, fmt.Errorf("the image build failed")
	}
	sw.mark("buildctl build")

	layers, err := b.readOCILayers(ociTar)
	if err != nil {
		return nil, err
	}
	sw.mark("read oci export")
	return layers, nil
}

// readOCILayers unpacks the OCI archive and returns its layers in order.
func (b *Builder) readOCILayers(ociTar string) ([]Layer, error) {
	ociDir := filepath.Join(b.WorkDir, "oci")
	if err := os.MkdirAll(ociDir, 0o755); err != nil {
		return nil, err
	}
	if out, err := exec.Command("tar", "-xf", ociTar, "-C", ociDir).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("unpacking the image export: %w: %s", err, lastLine(string(out)))
	}

	var index struct {
		Manifests []struct {
			Digest string `json:"digest"`
		} `json:"manifests"`
	}
	if err := readJSON(filepath.Join(ociDir, "index.json"), &index); err != nil {
		return nil, err
	}
	if len(index.Manifests) == 0 {
		return nil, fmt.Errorf("the image export contains no manifest")
	}

	var manifest struct {
		Config struct {
			Digest string `json:"digest"`
		} `json:"config"`
		Layers []struct {
			Digest    string `json:"digest"`
			MediaType string `json:"mediaType"`
			Size      int64  `json:"size"`
		} `json:"layers"`
	}
	if err := readJSON(blobPath(ociDir, index.Manifests[0].Digest), &manifest); err != nil {
		return nil, err
	}

	b.ociDir = ociDir
	b.configDigest = manifest.Config.Digest

	layers := make([]Layer, 0, len(manifest.Layers))
	for _, l := range manifest.Layers {
		layers = append(layers, Layer{
			Digest:    l.Digest,
			MediaType: l.MediaType,
			Size:      l.Size,
			Path:      blobPath(ociDir, l.Digest),
		})
	}
	return layers, nil
}

// blobPath maps an OCI digest to its file in the unpacked export.
func blobPath(ociDir, digest string) string {
	return filepath.Join(ociDir, "blobs", strings.ReplaceAll(digest, ":", "/"))
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func (b *Builder) appendLog(name, content string) {
	path := filepath.Join(b.WorkDir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(content)
}

// writePreludeTar builds the base layer: Blaxel's own binaries, which go in
// before any customer layer (see buildErofs for why).
func (b *Builder) writePreludeTar() (string, error) {
	path := filepath.Join(b.WorkDir, "prelude.tar")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()

	for _, entry := range preludeFiles() {
		if err := addFileToTar(tw, entry.src, entry.dst); err != nil {
			return "", fmt.Errorf("staging %s: %w", entry.dst, err)
		}
	}
	return path, nil
}

type preludeEntry struct{ src, dst string }

// wrapperPath is where the wrapper lands in the produced image, without the
// leading slash (tar) — see writeKraftFiles, which execs it from cmdline.txt.
const wrapperPath = "opt/blaxel/metamorph-wrapper"

// preludeFiles is what every produced image gets from us.
//
// blfs is read from this sandbox's own filesystem rather than shipped in the
// builder image: the push that produced the builder already injected the current
// FUSE client there, so reusing it keeps the two in lockstep. Without it, Agent
// Drives break in every image this builder produces.
func preludeFiles() []preludeEntry {
	return []preludeEntry{
		{src: artefactPath("BLBUILD_BLFS", "/usr/local/bin/blfs"), dst: "usr/local/bin/blfs"},
		// Not /bin: the prelude goes in before the customer layers (see
		// buildErofs), so it creates /bin as a directory — and a usrmerge image
		// such as debian ships /bin as a symlink to /usr/bin, which replaces it
		// and takes the wrapper with it. The image then boots to
		// "/bin/metamorph-wrapper: -2" (ENOENT) and reboots forever. /opt/blaxel
		// is ours and collides with nothing; cmdline.txt is generated here too,
		// so the two move together.
		{src: artefactPath("BLBUILD_WRAPPER", "/opt/blaxel/metamorph-wrapper"), dst: wrapperPath},
	}
}

// kernelSource is the kernel shipped as kraft/bin/kernel.
func kernelSource() string {
	return artefactPath("BLBUILD_KERNEL", "/opt/blaxel/kernel.bin")
}

// artefactPath lets the fixed in-image paths be redirected. The default is what
// runs in production; the override exists so the pipeline is testable outside a
// builder image, and is handy when bisecting a bad kernel.
func artefactPath(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

func (b *Builder) extractPrelude(tree string) error {
	for _, entry := range preludeFiles() {
		dst := filepath.Join(tree, entry.dst)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := copyFile(entry.src, dst, 0o755); err != nil {
			return err
		}
	}
	return nil
}

// runtimeDirs are the mount points and permissions a booting image needs. Kept
// deliberately tiny and applied last so no customer layer resets /tmp's sticky
// bit — and small enough that the incremental append is safe.
//
// The mode is the raw POSIX value, not an os.FileMode: Go encodes the sticky bit
// in a high bit of its own, which neither tar nor chmod(2) understand, so
// converting back and forth is a good way to ship a /tmp that is not 1777.
var runtimeDirs = []struct {
	path string
	mode int64
}{
	{"proc", 0o755},
	{"sys", 0o755},
	{"dev", 0o755},
	{"run", 0o755},
	{"tmp", 0o1777},
	{"var", 0o755},
	{"var/tmp", 0o1777},
}

func (b *Builder) writeRuntimeDirsTar() (string, error) {
	path := filepath.Join(b.WorkDir, "runtime-dirs.tar")
	f, err := os.Create(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	tw := tar.NewWriter(f)
	defer tw.Close()

	for _, d := range runtimeDirs {
		if err := tw.WriteHeader(&tar.Header{
			Name:     "./" + d.path + "/",
			Typeflag: tar.TypeDir,
			Mode:     d.mode,
			ModTime:  time.Unix(0, 0),
		}); err != nil {
			return "", err
		}
	}
	return path, nil
}

func (b *Builder) materializeRuntimeDirs(tree string) error {
	for _, d := range runtimeDirs {
		p := filepath.Join(tree, d.path)
		if err := os.MkdirAll(p, 0o755); err != nil {
			return err
		}
		// syscall.Chmod takes the raw mode, so the sticky bit survives.
		if err := syscall.Chmod(p, uint32(d.mode)); err != nil {
			return err
		}
	}
	return nil
}

func (b *Builder) extractLayer(ctx context.Context, layer Layer, tree string) error {
	decompress, err := decompressorFor(layer.MediaType)
	if err != nil {
		return err
	}
	// tar reads the decompressed stream from the pipe; no intermediate file.
	script := fmt.Sprintf("%s %q | tar -xf - -C %q",
		strings.Join(decompress, " "), layer.Path, tree)
	cmd := exec.CommandContext(ctx, "sh", "-c", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("extracting a layer: %w: %s", err, lastLine(string(out)))
	}
	return nil
}

func addFileToTar(tw *tar.Writer, src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     "./" + dst,
		Typeflag: tar.TypeReg,
		Mode:     0o755,
		Size:     info.Size(),
		ModTime:  time.Unix(0, 0),
	}); err != nil {
		return err
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(tw, f)
	return err
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, in)
	return err
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// workers caps mkfs's thread count. It is bounded because a build sandbox is
// small (4 vCPU in the reference config) and mkfs competing with buildkit's
// remaining work helps nobody.
func workers() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	if n > 8 {
		return 8
	}
	return n
}
