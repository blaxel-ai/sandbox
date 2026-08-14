package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A project that ships no Dockerfile still has to build: that is most of what
// `bl new` produces. metamorph detects the language and renders one of three
// templates, and this does the same, from the same templates — they are copied
// into the builder image from the metamorph checkout rather than rewritten, so a
// project builds the same whichever builder handles it.
//
// Without this, buildkit received an empty build definition ("transferring
// dockerfile: 2B") and every agent, MCP and job failed at the first step.

type projectType string

const (
	projectNode   projectType = "node"
	projectPython projectType = "python"
	projectGolang projectType = "golang"
)

// templateDir holds the templates staged into the builder image.
func templateDir() string {
	return artefactPath("BLBUILD_TEMPLATES", "/opt/blaxel/templates")
}

// detectProject mirrors metamorph's detect_project_type, including the order:
// package.json wins over a stray requirements.txt in a Node project.
func detectProject(dir string) (projectType, bool) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	switch {
	case exists("package.json"):
		return projectNode, true
	case exists("requirements.txt"), exists("pyproject.toml"),
		exists("setup.py"), exists("Pipfile"):
		return projectPython, true
	case exists("go.mod"):
		return projectGolang, true
	}
	// metamorph also accepts a bare entry point as a Python project.
	for _, entry := range []string{"main.py", "app.py", "server.py", "run.py"} {
		if exists(entry) {
			return projectPython, true
		}
	}
	return "", false
}

// blaxelBuild is the part of blaxel.toml the build reads: what the customer asked
// for, which has to win over anything inferred.
type blaxelBuild struct {
	// Entrypoint is [entrypoint] prod — the command the image starts with.
	Entrypoint string
	// WorkingDir overrides the default /blaxel.
	WorkingDir string
	// Slim reports whether [build] slim was set to false, which turns the rootfs
	// optimisation off.
	NoSlim bool
	// Args are [build.args], passed to the build as --opt build-arg:K=V.
	Args map[string]string
}

// readBlaxelToml parses the little of blaxel.toml the build depends on.
//
// Hand-parsed rather than pulled through a TOML library: the file is read inside
// the builder image, the three keys below are the whole contract, and a parser
// that rejects a manifest it does not fully understand would fail builds that
// metamorph accepts.
func readBlaxelToml(dir string) blaxelBuild {
	out := blaxelBuild{Args: map[string]string{}}
	raw, err := os.ReadFile(filepath.Join(dir, "blaxel.toml"))
	if err != nil {
		return out
	}
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch {
		case section == "entrypoint" && key == "prod":
			out.Entrypoint = value
		case key == "workingDir" || key == "working_dir":
			out.WorkingDir = value
		case section == "build" && key == "slim":
			out.NoSlim = value == "false"
		case section == "build.args":
			out.Args[key] = value
		}
	}
	return out
}

// defaults per project type, matching what metamorph renders.
type templateData struct {
	baseImage, workingDir, preInstall string
	lockFileCopy, installCommand      string
	buildCommand, requirementFile     string
	entrypointJSON                    string
}

// quoteJSON renders an ENTRYPOINT argv the way metamorph does: every element
// quoted, joined by ", ", with the template supplying the brackets.
func quoteJSON(parts ...string) string {
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return strings.Join(quoted, ", ")
}

// packageJSONScript reports whether package.json declares a given script.
// A malformed or absent package.json simply has no scripts, which is how
// metamorph treats it too — a build should not fail on a file it only consults.
func packageJSONScript(dir, name string) bool {
	raw, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return false
	}
	var parsed struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(raw, &parsed) != nil {
		return false
	}
	_, ok := parsed.Scripts[name]
	return ok
}

// firstExisting returns the first candidate present in dir, or "".
func firstExisting(dir string, candidates []string) string {
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(dir, c)); err == nil {
			return c
		}
	}
	return ""
}

// dataFor mirrors metamorph's three generators (src/dockerfile/{node,python,
// golang}.rs) field for field. It is deliberately a transcription rather than a
// tidier equivalent: the two builders have to render the same Dockerfile for the
// same project, and every place this drifted produced a build that succeeded on
// one builder and failed on the other.
func dataFor(kind projectType, dir string, cfg blaxelBuild) templateData {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	d := templateData{workingDir: "/blaxel"}
	if cfg.WorkingDir != "" {
		d.workingDir = cfg.WorkingDir
	}

	switch kind {
	case projectNode:
		d.baseImage = "public.ecr.aws/docker/library/node:22-alpine"
		// The package manager is installed with npm, not enabled with corepack:
		// corepack refuses to run unless package.json pins packageManager, and
		// that refusal is exit 1 — which is how `bl new mcp` and `bl new job`
		// were failing at "corepack enable && pnpm install --frozen-lockfile".
		pm := "npm"
		switch {
		case exists("bun.lockb"):
			pm, d.lockFileCopy = "bun", "COPY bun.lockb ./"
			d.installCommand, d.preInstall = "bun install --frozen-lockfile", "RUN npm install -g bun"
		case exists("bun.lock"):
			pm, d.lockFileCopy = "bun", "COPY bun.lock ./"
			d.installCommand, d.preInstall = "bun install --frozen-lockfile", "RUN npm install -g bun"
		case exists("pnpm-lock.yaml"):
			pm, d.lockFileCopy = "pnpm", "COPY pnpm-lock.yaml ./"
			d.installCommand, d.preInstall = "pnpm install --frozen-lockfile", "RUN npm install -g pnpm"
		case exists("yarn.lock"):
			pm, d.lockFileCopy = "yarn", "COPY yarn.lock ./"
			d.installCommand = "yarn install --frozen-lockfile"
		case exists("package-lock.json"):
			d.lockFileCopy, d.installCommand = "COPY package-lock.json ./", "npm ci"
		default:
			d.installCommand = "npm install"
		}

		// Without this, a framework project (Next.js, Astro) shipped its sources
		// and no build output, and the image started on something that was never
		// compiled.
		hasBuild := packageJSONScript(dir, "build")
		if hasBuild {
			d.buildCommand = "RUN " + pm + " run build"
		}

		switch {
		case packageJSONScript(dir, "prod"):
			d.entrypointJSON = quoteJSON(pm, "run", "prod")
		case packageJSONScript(dir, "start"):
			d.entrypointJSON = quoteJSON(pm, "run", "start")
		default:
			// A project that builds is searched for its output first; one that
			// does not, for its sources.
			built := []string{
				"dist/index.js", "dist/app.js", "dist/server.js",
				"build/index.js", "build/app.js", "build/server.js",
				"index.js", "app.js", "server.js",
				"src/index.js", "src/app.js", "src/server.js",
			}
			sources := []string{
				"index.js", "app.js", "server.js",
				"src/index.js", "src/app.js", "src/server.js",
				"dist/index.js", "dist/app.js", "dist/server.js",
				"build/index.js", "build/app.js", "build/server.js",
			}
			order := sources
			fallback := "index.js"
			if hasBuild {
				order, fallback = built, "dist/index.js"
			}
			entry := firstExisting(dir, order)
			if entry == "" {
				entry = fallback
			}
			d.entrypointJSON = quoteJSON("node", entry)
		}

	case projectPython:
		d.baseImage = "public.ecr.aws/docker/library/python:3.12-slim"
		// Unconditional, as in metamorph: a wheel that has to compile needs a
		// toolchain, and the slim image ships none. This used to be "true",
		// which turned every source distribution into a build failure.
		d.preInstall = "apt update && apt install -y build-essential"
		switch {
		case exists("uv.lock"):
			d.requirementFile, d.lockFileCopy = "pyproject.toml", "COPY uv.lock ./"
			d.installCommand = "pip install uv && uv sync"
		case exists("poetry.lock"):
			d.requirementFile, d.lockFileCopy = "pyproject.toml", "COPY poetry.lock ./"
			d.installCommand = "pip install poetry && poetry config virtualenvs.in-project true && poetry install"
		case exists("Pipfile.lock"):
			d.requirementFile, d.lockFileCopy = "Pipfile", "COPY Pipfile.lock ./"
			d.installCommand = "pip install pipenv && PIPENV_VENV_IN_PROJECT=1 pipenv install --deploy"
		case exists("Pipfile"):
			d.requirementFile = "Pipfile"
			d.installCommand = "pip install pipenv && PIPENV_VENV_IN_PROJECT=1 pipenv install"
		case exists("pyproject.toml"):
			d.requirementFile = "pyproject.toml"
			d.installCommand = "pip install uv && uv sync"
		case exists("requirements.txt"):
			d.requirementFile = "requirements.txt"
			d.installCommand = "pip install -r requirements.txt"
		}
		// No dependency file leaves requirementFile and installCommand empty, and
		// the renderer drops their lines rather than emitting a bare `RUN`.

		entry := firstExisting(dir, []string{
			"app.py", "main.py", "api.py",
			"app/main.py", "app/app.py", "app/api.py",
			"src/main.py", "src/app.py", "src/api.py",
		})
		if entry == "" {
			d.entrypointJSON = quoteJSON("python", "-m")
		} else {
			d.entrypointJSON = quoteJSON("python", entry)
		}

	case projectGolang:
		// The base image carries the stage name: the template's runtime stage
		// copies the binary `--from=builder`.
		d.baseImage = "public.ecr.aws/docker/library/golang:1.22-alpine AS builder"
		d.requirementFile = "go.mod"
		d.preInstall = "# Install ca-certificates for HTTPS\nRUN apk add --no-cache ca-certificates"
		if exists("go.sum") {
			d.lockFileCopy = "COPY go.sum ./"
		}
		vendorFlag := ""
		d.installCommand = "go mod download"
		if exists("vendor") {
			vendorFlag = " -mod=vendor"
			d.installCommand = "# Using vendored dependencies"
		}
		// blaxel.toml points at the main file only when it names a .go path;
		// anything else is a runtime command, and the binary is always /app.
		mainFile := ""
		if strings.HasSuffix(cfg.Entrypoint, ".go") {
			mainFile = cfg.Entrypoint
		}
		if mainFile == "" {
			if mainFile = firstExisting(dir, []string{"main.go", "src/main.go", "cmd/main.go"}); mainFile == "" {
				mainFile = "."
			}
		}
		d.buildCommand = fmt.Sprintf(
			"RUN CGO_ENABLED=0 GOOS=linux go build%s -a -installsuffix cgo -o app %s",
			vendorFlag, mainFile,
		)
		d.entrypointJSON = quoteJSON("app")
	}

	// What the customer asked for wins over anything inferred. Go is excluded:
	// its template hardcodes ENTRYPOINT ["/app"], and the entrypoint has already
	// been consumed above as the file to compile.
	if cfg.Entrypoint != "" && kind != projectGolang {
		d.entrypointJSON = quoteJSON(strings.Fields(cfg.Entrypoint)...)
	}
	return d
}

// buildEnvFile is read for build args and then deleted from the context.
const buildEnvFile = ".env.build"

// resolveBuildArgs merges [build.args] with .env.build, the latter winning, and
// removes .env.build from the context.
//
// The removal is the point, not tidiness: build args routinely carry a registry
// token or a private package credential, and a file left in the context is
// copied into a layer by any `COPY . .` — published, and readable by anyone who
// pulls the image. metamorph deletes it before the build for the same reason.
func resolveBuildArgs(dir string) (map[string]string, error) {
	args := map[string]string{}
	for k, v := range readBlaxelToml(dir).Args {
		args[k] = v
	}

	path := filepath.Join(dir, buildEnvFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return args, nil
		}
		return nil, fmt.Errorf("reading %s: %w", buildEnvFile, err)
	}
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("%s line %d: expected KEY=VALUE, got %q", buildEnvFile, i+1, line)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("%s line %d: empty key", buildEnvFile, i+1)
		}
		args[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}

	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing %s from the build context: %w", buildEnvFile, err)
	}
	return args, nil
}

// renderTemplate substitutes a placeholder, or deletes the whole line when the
// value is empty — metamorph's replace_or_remove_line.
//
// Substituting an empty string instead is not equivalent and not harmless: a
// Python project with no dependency file renders `RUN ` and `COPY  ./`, which
// buildkit rejects before running a single instruction.
func renderTemplate(tmpl string, values map[string]string) string {
	out := tmpl
	for placeholder, value := range values {
		if value != "" {
			out = strings.ReplaceAll(out, placeholder, value)
			continue
		}
		kept := make([]string, 0, strings.Count(out, "\n")+1)
		for _, line := range strings.Split(out, "\n") {
			if !strings.Contains(line, placeholder) {
				kept = append(kept, line)
			}
		}
		out = strings.Join(kept, "\n")
	}
	return out
}

// ensureDockerfile writes one when the context has none, and reports whether it
// generated it. A context that ships a Dockerfile is never touched.
func ensureDockerfile(dir string) (bool, error) {
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		return false, nil
	}
	kind, ok := detectProject(dir)
	if !ok {
		return false, fmt.Errorf("no Dockerfile, and the project language could not be identified")
	}

	tmpl, err := os.ReadFile(filepath.Join(templateDir(), "dockerfile."+string(kind)+".tmpl"))
	if err != nil {
		return false, fmt.Errorf("reading the %s template: %w", kind, err)
	}

	cfg := readBlaxelToml(dir)
	d := dataFor(kind, dir, cfg)
	out := renderTemplate(string(tmpl), map[string]string{
		"{base_image}":       d.baseImage,
		"{working_dir}":      d.workingDir,
		"{pre_install}":      d.preInstall,
		"{lock_file_copy}":   d.lockFileCopy,
		"{install_command}":  d.installCommand,
		"{build_command}":    d.buildCommand,
		"{requirement_file}": d.requirementFile,
		"{entrypoint_json}":  d.entrypointJSON,
	})

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(out), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
