package main

import (
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

func dataFor(kind projectType, dir string, cfg blaxelBuild) templateData {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	// pre_install sits on its own line in the node and golang templates, so it
	// has to be a whole instruction or nothing — a bare "true" there is parsed as
	// an unknown Dockerfile instruction. Python wraps it in RUN, where a shell
	// no-op is exactly right.
	d := templateData{workingDir: "/blaxel"}
	if kind == projectPython {
		d.preInstall = "true"
	}
	if cfg.WorkingDir != "" {
		d.workingDir = cfg.WorkingDir
	}

	switch kind {
	case projectNode:
		d.baseImage = "node:22-slim"
		switch {
		case exists("pnpm-lock.yaml"):
			d.lockFileCopy = "COPY pnpm-lock.yaml ./"
			d.installCommand = "corepack enable && pnpm install --frozen-lockfile"
		case exists("yarn.lock"):
			d.lockFileCopy = "COPY yarn.lock ./"
			d.installCommand = "corepack enable && yarn install --frozen-lockfile"
		case exists("package-lock.json"):
			d.lockFileCopy = "COPY package-lock.json ./"
			d.installCommand = "npm ci"
		default:
			d.installCommand = "npm install"
		}
		d.entrypointJSON = `"npm", "start"`
	case projectPython:
		d.baseImage = "python:3.12-slim"
		switch {
		case exists("pyproject.toml"):
			d.requirementFile = "pyproject.toml"
			d.installCommand = "pip install --no-cache-dir ."
		default:
			d.requirementFile = "requirements.txt"
			d.installCommand = "pip install --no-cache-dir -r requirements.txt"
		}
		d.entrypointJSON = `"python", "main.py"`
	case projectGolang:
		d.baseImage = "golang:1.23"
		d.requirementFile = "go.mod"
		d.lockFileCopy = "COPY go.sum ./"
		d.installCommand = "go mod download"
		d.buildCommand = "RUN go build -o /app ."
		d.entrypointJSON = `"/app"`
	}

	// What the customer asked for wins over anything inferred.
	if cfg.Entrypoint != "" {
		parts := strings.Fields(cfg.Entrypoint)
		quoted := make([]string, 0, len(parts))
		for _, p := range parts {
			quoted = append(quoted, fmt.Sprintf("%q", p))
		}
		d.entrypointJSON = strings.Join(quoted, ", ")
	}
	return d
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
	out := strings.NewReplacer(
		"{base_image}", d.baseImage,
		"{working_dir}", d.workingDir,
		"{pre_install}", d.preInstall,
		"{lock_file_copy}", d.lockFileCopy,
		"{install_command}", d.installCommand,
		"{build_command}", d.buildCommand,
		"{requirement_file}", d.requirementFile,
		"{entrypoint_json}", d.entrypointJSON,
	).Replace(string(tmpl))

	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(out), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
