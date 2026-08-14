package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// metamorph's templates/dockerfile.*.tmpl, verbatim. Copied rather than reduced:
// a reduced shape hides exactly the bugs this file exists to catch — a bare
// `RUN ` from an empty placeholder, or a Go binary the runtime stage cannot find
// because the builder stage was never named.
const (
	nodeTemplate = `FROM {base_image}

WORKDIR {working_dir}

{pre_install}

# Copy package files first for better layer caching
COPY package.json ./
{lock_file_copy}

# Install dependencies
RUN {install_command}

# Copy application code
COPY . .

{build_command}

ENTRYPOINT [{entrypoint_json}]`

	pythonTemplate = `FROM {base_image}

WORKDIR {working_dir}

# Install system dependencies first if needed
RUN {pre_install}

# Copy dependency files first for better layer caching
COPY {requirement_file} ./
{lock_file_copy}

# Install Python dependencies
RUN {install_command}

# Copy application code
COPY . .

{build_command}

ENV PATH="/blaxel/.venv/bin:$PATH"

ENTRYPOINT [{entrypoint_json}]`

	golangTemplate = `# Build stage
FROM {base_image}

{pre_install}

WORKDIR {working_dir}

# Copy go mod files
COPY {requirement_file} ./
{lock_file_copy}

# Download dependencies
RUN {install_command}

# Copy source code
COPY . .

# Build the application
{build_command}

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

# Copy the binary from builder
COPY --from=builder {working_dir}/app /app

# Run the binary
ENTRYPOINT ["/app"]`
)

func stageTemplates(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{
		"dockerfile.node.tmpl":   nodeTemplate,
		"dockerfile.python.tmpl": pythonTemplate,
		"dockerfile.golang.tmpl": golangTemplate,
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
	for _, want := range []string{
		"FROM public.ecr.aws/docker/library/node:22-alpine",
		"RUN npm install -g pnpm",
		"RUN pnpm install --frozen-lockfile --config.dangerouslyAllowAllBuilds=true",
		"COPY pnpm-lock.yaml",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Dockerfile missing %q:\n%s", want, got)
		}
	}
	// corepack refuses to run unless package.json pins packageManager, and that
	// refusal is exit 1. metamorph installs the package manager with npm instead,
	// and every `bl new` project relies on that.
	if strings.Contains(got, "corepack") {
		t.Errorf("corepack is back; it fails on projects without packageManager:\n%s", got)
	}
	assertDockerfileParses(t, got)
}

// Every non-empty line has to be a Dockerfile instruction. An empty placeholder
// left in place renders `RUN ` or `COPY  ./`, which buildkit rejects before it
// runs a single step.
func assertDockerfileParses(t *testing.T, content string) {
	t.Helper()
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		verb := strings.ToUpper(fields[0])
		switch verb {
		case "FROM", "RUN", "COPY", "WORKDIR", "ENTRYPOINT", "CMD", "ENV", "ARG", "EXPOSE", "USER", "LABEL", "VOLUME":
		default:
			t.Errorf("not a Dockerfile instruction: %q\n%s", line, content)
			continue
		}
		if len(fields) == 1 {
			t.Errorf("instruction with no argument: %q\n%s", line, content)
		}
	}
}

// A framework project (Next.js, Astro) has to run its build script, or the image
// ships sources and starts on something that was never compiled.
func TestNodeRunsTheBuildScript(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{"build":"next build","start":"next start"}}`)
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	if !strings.Contains(got, "RUN pnpm run build") {
		t.Errorf("the build script is not run:\n%s", got)
	}
	// The package manager's own script wins over guessing at a file.
	if !strings.Contains(got, `ENTRYPOINT ["pnpm", "run", "start"]`) {
		t.Errorf("the start script was not used as entrypoint:\n%s", got)
	}
	assertDockerfileParses(t, got)
}

// prod beats start, as in metamorph's priority order.
func TestNodePrefersTheProdScript(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", `{"scripts":{"prod":"node dist/x.js","start":"nodemon"}}`)

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "Dockerfile"); !strings.Contains(got, `ENTRYPOINT ["npm", "run", "prod"]`) {
		t.Errorf("prod did not win over start:\n%s", got)
	}
}

// A Python project with no dependency file leaves install_command empty. Left in
// place that renders `RUN `, which is a parse error — the whole line has to go.
func TestPythonWithoutDependenciesStillParses(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "main.py", "print('hi')")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	assertDockerfileParses(t, got)
	if !strings.Contains(got, `ENTRYPOINT ["python", "main.py"]`) {
		t.Errorf("entrypoint not detected from main.py:\n%s", got)
	}
}

// The slim image ships no compiler, so any dependency with a source distribution
// fails to build without one. metamorph installs the toolchain unconditionally.
func TestPythonInstallsABuildToolchain(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "requirements.txt", "uvicorn\n")
	write(t, dir, "app.py", "")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	if !strings.Contains(got, "RUN apt update && apt install -y build-essential") {
		t.Errorf("no build toolchain installed:\n%s", got)
	}
	if !strings.Contains(got, "RUN pip install -r requirements.txt") {
		t.Errorf("dependencies not installed:\n%s", got)
	}
	assertDockerfileParses(t, got)
}

func TestPythonUsesUvForPyproject(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "pyproject.toml", "[project]\nname='x'\n")
	write(t, dir, "uv.lock", "version = 1")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	for _, want := range []string{"COPY uv.lock ./", "RUN pip install uv && uv sync", "COPY pyproject.toml ./"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// The runtime stage copies `--from=builder`, so the build stage has to be named.
// Without the stage name the Dockerfile fails at the COPY, after the whole
// compile has already been paid for.
func TestGolangBuildStageIsNamed(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "go.mod", "module x\n\ngo 1.22\n")
	write(t, dir, "go.sum", "")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	got := read(t, dir, "Dockerfile")
	if !strings.Contains(got, "AS builder") {
		t.Errorf("the build stage is unnamed but the runtime stage copies from it:\n%s", got)
	}
	if !strings.Contains(got, "go build -a -installsuffix cgo -o app main.go") {
		t.Errorf("unexpected build command:\n%s", got)
	}
	assertDockerfileParses(t, got)
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

// Build args come from two places and .env.build wins, as in metamorph.
func TestResolveBuildArgsMergesBothSources(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "blaxel.toml", "[build.args]\nVERSION = \"1.0.0\"\nREGION = \"eu\"\n")
	write(t, dir, ".env.build", "VERSION=2.0.0\nTOKEN=secret\n# a comment\n\n")

	args, err := resolveBuildArgs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if args["VERSION"] != "2.0.0" {
		t.Errorf("VERSION = %q, .env.build should win", args["VERSION"])
	}
	if args["REGION"] != "eu" {
		t.Errorf("REGION = %q, the manifest value was dropped", args["REGION"])
	}
	if args["TOKEN"] != "secret" {
		t.Errorf("TOKEN = %q", args["TOKEN"])
	}
}

// Build args carry registry tokens and package credentials. A .env.build left in
// the context is copied into a layer by any `COPY . .` and published with the
// image, readable by anyone who pulls it.
func TestResolveBuildArgsRemovesTheEnvFileFromTheContext(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env.build", "TOKEN=secret\n")

	if _, err := resolveBuildArgs(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".env.build")); !os.IsNotExist(err) {
		t.Fatal(".env.build is still in the build context and will be baked into a layer")
	}
}

// A context with neither source is the common case and must not error.
func TestResolveBuildArgsWithNeitherSource(t *testing.T) {
	args, err := resolveBuildArgs(t.TempDir())
	if err != nil || len(args) != 0 {
		t.Fatalf("args=%v err=%v", args, err)
	}
}

// A malformed line is the customer's typo; failing loudly beats silently
// building without the arg and failing somewhere unrelated.
func TestResolveBuildArgsRejectsAMalformedLine(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".env.build", "NOT_A_PAIR\n")
	if _, err := resolveBuildArgs(dir); err == nil {
		t.Fatal("a line without = was accepted")
	}
}

// pnpm 10 makes an unapproved dependency build script fatal, so every `bl new`
// project fails at install without this. npm and yarn run those scripts
// unconditionally; esbuild does not even get a platform binary otherwise.
func TestNodeAllowsDependencyBuildScripts(t *testing.T) {
	stageTemplates(t)
	dir := t.TempDir()
	write(t, dir, "package.json", `{"dependencies":{"esbuild":"0.25.10"}}`)
	write(t, dir, "pnpm-lock.yaml", "lockfileVersion: 9")

	if _, err := ensureDockerfile(dir); err != nil {
		t.Fatal(err)
	}
	if got := read(t, dir, "Dockerfile"); !strings.Contains(got, "--config.dangerouslyAllowAllBuilds=true") {
		t.Errorf("pnpm will refuse the ignored build scripts and exit 1:\n%s", got)
	}
}
