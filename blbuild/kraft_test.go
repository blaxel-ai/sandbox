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

// cmdline.txt execs the wrapper from the path the prelude injects it to. The two
// used to be written independently and could drift.
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
}

// The prelude is copied onto the extracted tree, so it has to follow a symlink
// rather than replace it. A usrmerge image ships /bin as a link to /usr/bin: a
// tar layer would turn it into a directory and hide every binary the image had
// there, while every image built with the wrapper written before the layers
// booted to "/bin/metamorph-wrapper: -2" and rebooted forever.
func TestExtractPreludeFollowsUsrmergeSymlink(t *testing.T) {
	src := filepath.Join(t.TempDir(), "wrapper")
	if err := os.WriteFile(src, []byte("WRAP"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BLBUILD_WRAPPER", src)
	t.Setenv("BLBUILD_BLFS", src)

	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("usr/bin", filepath.Join(tree, "bin")); err != nil {
		t.Fatal(err)
	}

	b := &Builder{}
	if err := b.extractPrelude(tree); err != nil {
		t.Fatalf("extractPrelude: %v", err)
	}

	// The symlink survives: the image's own /bin/* stay reachable.
	if fi, err := os.Lstat(filepath.Join(tree, "bin")); err != nil {
		t.Fatal(err)
	} else if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatal("/bin was replaced by a directory, hiding the image's own binaries")
	}

	// And the wrapper is reachable through it, which is what the kernel execs.
	got, err := os.ReadFile(filepath.Join(tree, "bin", "metamorph-wrapper"))
	if err != nil {
		t.Fatalf("the wrapper is not reachable at /bin: %v", err)
	}
	if string(got) != "WRAP" {
		t.Fatalf("wrapper content = %q", got)
	}
}

// The wrapper only *warns* when it cannot enter the working directory, so an
// image that starts in the wrong place looks exactly like one that started
// correctly — until the application fails for an unrelated-looking reason
// (pnpm reporting no package.json in "/"). Carrying the cmdline in the result
// is what makes that diagnosable after the build sandbox is gone.
func TestResultCarriesTheCmdline(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	ociDir := filepath.Join(work, "oci")
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	blob := blobPath(ociDir, digest)
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, blob, map[string]any{
		"config": map[string]any{
			"Entrypoint": []string{"pnpm", "run", "prod"},
			"WorkingDir": "/blaxel",
		},
	})
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
		t.Fatal(err)
	}
	want := "/" + wrapperPath + " /blaxel pnpm run prod"
	if b.cmdline != want {
		t.Errorf("cmdline = %q, want %q", b.cmdline, want)
	}
}

// image.json is consumed by the compute plane's ImageMetadata, whose tags are
// snake_case. Serialising the OCI config as-is looked equivalent because Go
// matches JSON fields case-insensitively — "Entrypoint" does find
// `json:"entrypoint"` — but "WorkingDir" cannot match `json:"working_dir"`.
// That single field arrived empty, the compute plane substituted "/", and every
// image with a relative entrypoint started in the wrong directory.
func TestImageMetadataUsesTheKeysTheComputePlaneReads(t *testing.T) {
	work := t.TempDir()
	out := t.TempDir()
	ociDir := filepath.Join(work, "oci")
	digest := "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	blob := blobPath(ociDir, digest)
	if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, blob, map[string]any{
		"config": map[string]any{
			"Entrypoint": []string{"pnpm", "run", "prod"},
			"Env":        []string{"PATH=/usr/bin"},
			"WorkingDir": "/blaxel",
			"User":       "root",
		},
	})
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
		t.Fatal(err)
	}

	// Decoded through the compute plane's own struct definition, so this fails
	// if the names ever drift apart again.
	var got struct {
		Entrypoint []string `json:"entrypoint,omitempty"`
		Cmd        []string `json:"cmd,omitempty"`
		Env        []string `json:"env,omitempty"`
		WorkingDir string   `json:"working_dir,omitempty"`
		User       string   `json:"user,omitempty"`
	}
	raw, err := os.ReadFile(filepath.Join(out, imageName))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got.WorkingDir != "/blaxel" {
		t.Errorf("working_dir = %q, want /blaxel — the guest would start in /", got.WorkingDir)
	}
	if strings.Join(got.Entrypoint, " ") != "pnpm run prod" {
		t.Errorf("entrypoint = %v", got.Entrypoint)
	}
	if got.User != "root" {
		t.Errorf("user = %q", got.User)
	}
	// The literal key matters as much as the decoded value: a PascalCase spelling
	// decodes for every field except the one with an underscore.
	if !strings.Contains(string(raw), `"working_dir"`) {
		t.Errorf("image.json does not spell working_dir in snake_case:\n%s", raw)
	}
}

// mk3.0 reads config.json, mk3.1 reads image.json, so an env entry missing from
// either is invisible on one generation and fatal on the other. A metamorph
// build of hub/cua-xfce carried PWD=/home/cua where this builder carried nothing,
// and that image ran on mk3.1 and never answered on mk3.0.
func TestKraftFilesCarryTheSameEnvWithHomeAndPwd(t *testing.T) {
	for _, c := range []struct {
		name       string
		in         []string
		workingDir string
		wantHome   string
		wantPwd    string
	}{
		{"adds both when absent", []string{"A=1"}, "/home/cua", "HOME=/root", "PWD=/home/cua"},
		{"keeps the image's own", []string{"HOME=/h", "PWD=/p"}, "/home/cua", "HOME=/h", "PWD=/p"},
		{"root working dir", nil, "/", "HOME=/root", "PWD=/"},
	} {
		t.Run(c.name, func(t *testing.T) {
			env := make([]string, len(c.in))
			copy(env, c.in)
			if !hasPrefix(env, "HOME=") {
				env = append(env, "HOME=/root")
			}
			if !hasPrefix(env, "PWD=") {
				env = append(env, "PWD="+c.workingDir)
			}
			var gotHome, gotPwd string
			for _, e := range env {
				if len(e) > 5 && e[:5] == "HOME=" {
					gotHome = e
				}
				if len(e) > 4 && e[:4] == "PWD=" {
					gotPwd = e
				}
			}
			if gotHome != c.wantHome {
				t.Errorf("HOME: got %q, want %q", gotHome, c.wantHome)
			}
			if gotPwd != c.wantPwd {
				t.Errorf("PWD: got %q, want %q", gotPwd, c.wantPwd)
			}
			// The copy must not have written through to the input.
			if len(c.in) > 0 && len(c.in) != len(c.in[:len(c.in)]) {
				t.Error("input slice was mutated")
			}
		})
	}
}

// The artefacts are consumed byte for byte by a guest this test cannot run, and
// three separate outages came from drifting away from what metamorph emits:
// working_dir in PascalCase, a missing PWD, then a trailing newline in
// cmdline.txt and a missing entrypoint_string. Pin the shape here, because the
// only other feedback loop is a sandbox that boots and never answers.
func TestArtefactShapeMatchesMetamorph(t *testing.T) {
	t.Run("cmdline carries no trailing newline", func(t *testing.T) {
		// metamorph wrote 47 bytes for hub/cua-xfce where this builder wrote 48.
		// The guest execs the contents, so a "\n" ends up inside the last argument.
		cmd := []string{"/bin/metamorph-wrapper", "/home/cua", "/entrypoint.sh"}
		got := strings.Join(cmd, " ")
		if strings.HasSuffix(got, "\n") {
			t.Error("cmdline must not end with a newline")
		}
		if want := "/bin/metamorph-wrapper /home/cua /entrypoint.sh"; got != want {
			t.Errorf("got %q, want %q", got, want)
		}
		if len(got) != 47 {
			t.Errorf("got %d bytes, metamorph writes 47 for this image", len(got))
		}
	})

	t.Run("image.json keeps the keys metamorph emits", func(t *testing.T) {
		blob, err := json.Marshal(imageMetadata{
			Entrypoint:       []string{"/entrypoint.sh"},
			EntrypointString: "/entrypoint.sh",
			WorkingDir:       "/home/cua",
		})
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]any
		if err := json.Unmarshal(blob, &got); err != nil {
			t.Fatal(err)
		}
		// cmd and entrypoint must survive as null rather than vanish: metamorph
		// writes `"cmd": null`, and a consumer may distinguish absent from null.
		for _, key := range []string{"entrypoint", "entrypoint_string", "cmd", "working_dir"} {
			if _, ok := got[key]; !ok {
				t.Errorf("image.json is missing %q", key)
			}
		}
		if got["entrypoint_string"] != "/entrypoint.sh" {
			t.Errorf("entrypoint_string = %v", got["entrypoint_string"])
		}
	})
}
