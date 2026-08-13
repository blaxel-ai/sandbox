package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWriteKraftFiles pins the artefact format. The compute plane reads these
// files directly, so a drifted field name or a swapped diff_ids order produces an
// image that builds fine and then refuses to boot — the worst kind of bug to
// debug, because the build reports success.
func TestWriteKraftFiles(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()

	// A minimal OCI export: just the config blob writeKraftFiles reads.
	ociDir := filepath.Join(work, "oci")
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	blob := blobPath(ociDir, digest)
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := map[string]any{
		"config": map[string]any{
			"Entrypoint": []string{"/entrypoint.sh"},
			"Cmd":        []string{"--serve"},
			"Env":        []string{"PATH=/usr/bin"},
			"WorkingDir": "/app",
		},
	}
	writeTestJSON(t, blob, cfg)

	// The kernel is read from a fixed path in the builder image; point it at a
	// stand-in for the test.
	kernelSrc := filepath.Join(work, "kernel.bin")
	if err := os.WriteFile(kernelSrc, []byte("kernel"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, rootfsName), []byte("rootfs"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLBUILD_KERNEL", kernelSrc)

	b := &Builder{WorkDir: work, OutDir: out, ociDir: ociDir, configDigest: digest}
	if err := b.writeKraftFiles(context.Background(), newStopwatch()); err != nil {
		t.Fatalf("writeKraftFiles: %v", err)
	}

	// cmdline.txt: the wrapper, then the working directory, then the command.
	cmdline := readTestFile(t, filepath.Join(out, cmdlineName))
	want := "/" + wrapperPath + " /app /entrypoint.sh --serve"
	if strings.TrimSpace(cmdline) != want {
		t.Errorf("cmdline.txt = %q, want %q", strings.TrimSpace(cmdline), want)
	}

	var config struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Config       struct {
			Cmd        []string          `json:"Cmd"`
			Env        []string          `json:"Env"`
			Labels     map[string]string `json:"Labels"`
			WorkingDir string            `json:"WorkingDir"`
		} `json:"config"`
		Rootfs struct {
			DiffIDs []string `json:"diff_ids"`
			Type    string   `json:"type"`
		} `json:"rootfs"`
	}
	readTestJSON(t, filepath.Join(out, configName), &config)

	if config.Architecture != "x86_64" || config.OS != "kraftcloud" {
		t.Errorf("architecture/os = %q/%q, want x86_64/kraftcloud", config.Architecture, config.OS)
	}
	if config.Config.WorkingDir != "/app" {
		t.Errorf("WorkingDir = %q, want /app", config.Config.WorkingDir)
	}
	if config.Rootfs.Type != "layers" {
		t.Errorf("rootfs.type = %q, want layers", config.Rootfs.Type)
	}
	if len(config.Rootfs.DiffIDs) != 2 {
		t.Fatalf("rootfs.diff_ids has %d entries, want 2 (kernel then rootfs)", len(config.Rootfs.DiffIDs))
	}
	// Order is load-bearing and silent when wrong.
	kernelSHA, _ := sha256File(filepath.Join(out, kernelName))
	rootfsSHA, _ := sha256File(filepath.Join(out, rootfsName))
	if config.Rootfs.DiffIDs[0] != "sha256:"+kernelSHA {
		t.Error("the first diff_id must be the kernel digest")
	}
	if config.Rootfs.DiffIDs[1] != "sha256:"+rootfsSHA {
		t.Error("the second diff_id must be the rootfs digest")
	}

	// HOME is added when absent: a shell in the VM otherwise has no home.
	if !hasPrefix(config.Config.Env, "HOME=") {
		t.Errorf("Env must gain a HOME entry, got %v", config.Config.Env)
	}
	if len(config.Config.Labels) != 3 {
		t.Errorf("expected the three scale-to-zero labels, got %v", config.Config.Labels)
	}

	// The kernel must actually be staged, not just referenced.
	if _, err := os.Stat(filepath.Join(out, kernelName)); err != nil {
		t.Errorf("the kernel was not staged: %v", err)
	}
}

// TestWriteKraftFilesDefaultWorkingDir covers the empty-WorkingDir case: the
// wrapper receives a path, so "" would shift the command by one argument.
func TestWriteKraftFilesDefaultWorkingDir(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	ociDir := filepath.Join(work, "oci")
	digest := "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	blob := blobPath(ociDir, digest)
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, blob, map[string]any{
		"config": map[string]any{"Cmd": []string{"/bin/sh"}, "Env": []string{"HOME=/custom"}},
	})
	kernelSrc := filepath.Join(work, "kernel.bin")
	if err := os.WriteFile(kernelSrc, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, rootfsName), []byte("r"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLBUILD_KERNEL", kernelSrc)

	b := &Builder{WorkDir: work, OutDir: out, ociDir: ociDir, configDigest: digest}
	if err := b.writeKraftFiles(context.Background(), newStopwatch()); err != nil {
		t.Fatalf("writeKraftFiles: %v", err)
	}

	cmdline := strings.TrimSpace(readTestFile(t, filepath.Join(out, cmdlineName)))
	if cmdline != "/"+wrapperPath+" / /bin/sh" {
		t.Errorf("cmdline.txt = %q, want the working directory to default to /", cmdline)
	}

	var config struct {
		Config struct{ Env []string } `json:"config"`
	}
	readTestJSON(t, filepath.Join(out, configName), &config)
	// An image that already sets HOME must not get a second one.
	homes := 0
	for _, e := range config.Config.Env {
		if strings.HasPrefix(e, "HOME=") {
			homes++
		}
	}
	if homes != 1 {
		t.Errorf("expected exactly one HOME entry, got %d in %v", homes, config.Config.Env)
	}
}

func writeTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// cmdline.txt execs the wrapper from the path the prelude injects it to. The
// two used to be written independently, and drifted: the prelude landed the
// wrapper in /bin, which a usrmerge image replaces with a symlink to /usr/bin,
// so every image built from debian booted to "/bin/metamorph-wrapper: -2" and
// rebooted forever. Nothing catches that except booting the image.
func TestCmdlineExecsTheInjectedWrapper(t *testing.T) {
	var injected string
	for _, entry := range preludeFiles() {
		if strings.HasSuffix(entry.dst, "metamorph-wrapper") {
			injected = entry.dst
		}
	}
	if injected == "" {
		t.Fatal("the prelude no longer injects the wrapper")
	}
	if injected != wrapperPath {
		t.Fatalf("the prelude injects %q, cmdline.txt execs %q", injected, wrapperPath)
	}
	if strings.HasPrefix(injected, "bin/") || strings.HasPrefix(injected, "usr/") {
		t.Fatalf("%q sits under a path a customer image can replace", injected)
	}
}
