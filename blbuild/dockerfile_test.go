package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stageTemplates(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	// Same shape as metamorph's, reduced to the placeholders under test.
	for name, body := range map[string]string{
		// {pre_install} sits on its own line here, as in metamorph's template.
		"dockerfile.node.tmpl":   "FROM {base_image}\nWORKDIR {working_dir}\n{pre_install}\n{lock_file_copy}\nRUN {install_command}\nENTRYPOINT [{entrypoint_json}]\n",
		"dockerfile.python.tmpl": "FROM {base_image}\nCOPY {requirement_file} ./\nRUN {install_command}\nENTRYPOINT [{entrypoint_json}]\n",
		"dockerfile.golang.tmpl": "FROM {base_image}\nRUN {install_command}\n{build_command}\nENTRYPOINT [{entrypoint_json}]\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BLBUILD_TEMPLATES", dir)
}

// Most of what `bl new` produces ships no Dockerfile. Without one, buildkit gets
// an empty build definition — "transferring dockerfile: 2B" — and every agent,
// MCP and job fails on its first step.
func TestEnsureDockerfileGeneratesForNode(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"x"}`)
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9")

	generated, err := ensureDockerfile(dir)
	if err != nil || !generated {
		t.Fatalf("ensureDockerfile: generated=%v err=%v", generated, err)
	}
	got := read(t, dir, "Dockerfile")
	for _, want := range []string{"FROM node:", "pnpm install --frozen-lockfile", "COPY pnpm-lock.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, got)
		}
	}
	// Every non-empty line has to be a Dockerfile instruction. A placeholder
	// rendered bare — pre_install as "true" — is a parse error, not a no-op.
	for _, line := range strings.Split(got, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verb := strings.ToUpper(strings.Fields(line)[0])
		switch verb {
		case "FROM", "RUN", "COPY", "WORKDIR", "ENTRYPOINT", "CMD", "ENV", "ARG", "EXPOSE", "USER", "LABEL", "VOLUME":
		default:
			t.Errorf("not a Dockerfile instruction: %q\n%s", line, got)
		}
	}
}

// A context that ships its own recipe is the customer's business, not ours.
func TestEnsureDockerfileLeavesAnExistingOneAlone(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", "{}")
	write(t, dir, "Dockerfile", "FROM scratch\n")

	generated, err := ensureDockerfile(dir)
	if err != nil || generated {
		t.Fatalf("generated=%v err=%v, want it untouched", generated, err)
	}
	if got := read(t, dir, "Dockerfile"); got != "FROM scratch\n" {
		t.Fatalf("the customer's Dockerfile was rewritten: %q", got)
	}
}

// What the manifest asks for wins over anything inferred from the file tree.
func TestBlaxelTomlOverridesTheEntrypoint(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", "{}")
	write(t, dir, "blaxel.toml", "type = \"agent\"\n\n[entrypoint]\nprod = \"node dist/index.js\"\n")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	if !strings.Contains(got, `ENTRYPOINT ["node", "dist/index.js"]`) {
		t.Fatalf("the manifest entrypoint was ignored:\n%s", got)
	}
}

func TestReadBlaxelTomlBuildSettings(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "blaxel.toml", `type = "sandbox"

[build]
slim = false

[build.args]
VERSION = "1.2.3"
`)
	cfg := readBlaxelToml(dir)
	if !cfg.NoSlim {
		t.Error("[build] slim = false was not honoured")
	}
	if cfg.Args["VERSION"] != "1.2.3" {
		t.Errorf("build args = %v", cfg.Args)
	}
}

// A language we cannot identify must fail with something a customer can act on,
// not produce an empty Dockerfile that fails inside buildkit.
func TestEnsureDockerfileRefusesAnUnknownProject(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "README.md", "hello")

	if _, err := ensureDockerfile(dir); err == nil {
		t.Fatal("an unidentifiable project produced a Dockerfile")
	}
}

func TestDetectProjectPrefersNodeOverStrayPythonFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", "{}")
	write(t, dir, "requirements.txt", "")
	if kind, _ := detectProject(dir); kind != projectNode {
		t.Fatalf("detected %q, want node", kind)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
