package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

// ociConfig is the subset of the image config the artefacts are derived from.
type ociConfig struct {
	Config struct {
		Entrypoint []string `json:"Entrypoint"`
		Cmd        []string `json:"Cmd"`
		Env        []string `json:"Env"`
		WorkingDir string   `json:"WorkingDir"`
		User       string   `json:"User"`
	} `json:"config"`
}

// imageMetadata is image.json, field for field as the compute plane declares it
// in kraft-provider's ImageMetadata. The names are its contract, not ours, so
// they are spelled out here rather than inherited from the OCI config.
type imageMetadata struct {
	Entrypoint []string `json:"entrypoint,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
	Env        []string `json:"env,omitempty"`
	WorkingDir string   `json:"working_dir,omitempty"`
	User       string   `json:"user,omitempty"`
}

// writeKraftFiles produces the four artefacts that accompany the rootfs. Their
// shape is dictated by what the compute plane reads, so it matches the existing
// builder field for field — an image whose config.json differs simply will not
// boot.
func (b *Builder) writeKraftFiles(ctx context.Context, sw *stopwatch) error {
	_, span := tracer().Start(ctx, "kraft_files")
	defer span.End()

	var cfg ociConfig
	if err := readJSON(blobPath(b.ociDir, b.configDigest), &cfg); err != nil {
		return fmt.Errorf("reading the image configuration: %w", err)
	}

	workingDir := cfg.Config.WorkingDir
	if workingDir == "" {
		workingDir = "/"
	}

	// The kernel is shipped, not built: it comes from the builder image, which
	// embeds the same one the current builder does.
	kernelPath := filepath.Join(b.OutDir, kernelName)
	if err := copyFile(kernelSource(), kernelPath, 0o644); err != nil {
		return fmt.Errorf("staging the kernel: %w", err)
	}

	// cmdline.txt: the wrapper receives the working directory then the command.
	cmd := append([]string{"/" + wrapperPath, workingDir},
		append(cfg.Config.Entrypoint, cfg.Config.Cmd...)...)
	if err := os.WriteFile(filepath.Join(b.OutDir, cmdlineName),
		[]byte(strings.Join(cmd, " ")+"\n"), 0o644); err != nil {
		return err
	}
	// This one line is what a boot failure is diagnosed from. The wrapper chdirs
	// to the working directory and only *warns* when it cannot, so an image that
	// starts in the wrong place looks identical to one that started correctly
	// until the application itself fails — `pnpm` reporting no package.json in
	// "/" was three hours of guessing that this line answers immediately.
	b.cmdline = strings.Join(cmd, " ")
	fmt.Printf("cmdline: %s\n", b.cmdline)
	span.SetAttributes(
		attribute.String("build.working_dir", workingDir),
		attribute.String("build.cmdline", b.cmdline),
	)

	// The env has to match the current builder entry for entry, because mk3.0
	// reads config.json — it is the kraftcloud/unikraft descriptor, which is why
	// `os`, `os.features` and the cloud.unikraft.v1 labels live in it — while
	// mk3.1 reads image.json and the EROFS directly. A field missing here is
	// therefore invisible on mk3.1 and load-bearing on mk3.0: hub/cua-xfce built,
	// ran on mk3.1, and on mk3.0 never answered, the gateway timing out after 10s
	// on a VM that had nothing listening. The only difference against a metamorph
	// build of the same image was this one entry.
	//
	// Copied, not appended in place: `append` on cfg.Config.Env would write
	// through to the caller's slice whenever it has spare capacity, so the OCI
	// config would gain entries depending on how it happened to be decoded.
	env := make([]string, len(cfg.Config.Env))
	copy(env, cfg.Config.Env)
	// HOME: without it a shell started in the VM has no home.
	if !hasPrefix(env, "HOME=") {
		env = append(env, "HOME=/root")
	}
	// PWD: the wrapper chdirs to the working directory, but anything reading $PWD
	// rather than calling getcwd() sees the value baked in here.
	if !hasPrefix(env, "PWD=") {
		env = append(env, "PWD="+workingDir)
	}

	kernelSHA, err := sha256File(kernelPath)
	if err != nil {
		return err
	}
	rootfsSHA, err := sha256File(filepath.Join(b.OutDir, rootfsName))
	if err != nil {
		return err
	}

	config := map[string]any{
		"architecture": "x86_64",
		"config": map[string]any{
			"Cmd": cmd,
			"Env": env,
			"Labels": map[string]string{
				"cloud.unikraft.v1.instances/scale_to_zero.cooldown_time_ms": "5000",
				"cloud.unikraft.v1.instances/scale_to_zero.policy":           "on",
				"cloud.unikraft.v1.instances/scale_to_zero.stateful":         "true",
			},
			"WorkingDir": workingDir,
		},
		"os": "kraftcloud",
		"os.features": []string{
			"CONFIG_ARCH_X86_64=y",
			"CONFIG_KVM_VMM_FIRECRACKER=y",
			"CONFIG_PLAT_KVM=y",
		},
		// Order is load-bearing: kernel first, rootfs second.
		"rootfs": map[string]any{
			"diff_ids": []string{"sha256:" + kernelSHA, "sha256:" + rootfsSHA},
			"type":     "layers",
		},
	}
	if err := writeJSON(filepath.Join(b.OutDir, configName), config); err != nil {
		return err
	}

	// image.json is read by the compute plane's ImageMetadata
	// (executionplane, kraft-provider/image_handler.go), whose tags are
	// snake_case. Writing the OCI config as-is looked equivalent because Go
	// matches JSON fields case-insensitively — "Entrypoint" does find
	// `json:"entrypoint"` — but that only holds while the names differ by case
	// alone. "WorkingDir" cannot match `json:"working_dir"`, so that one field,
	// and only that one, arrived empty.
	//
	// The compute plane then substitutes "/" for an empty working directory
	// (wrapImageArgsWithMetamorph), so every image with a relative entrypoint
	// started in the wrong place: `bl new` projects failed with pnpm reporting no
	// package.json in "/", while hub images survived only because their
	// entrypoints are absolute.
	if err := writeJSON(filepath.Join(b.OutDir, imageName), imageMetadata{
		Entrypoint: cfg.Config.Entrypoint,
		Cmd:        cfg.Config.Cmd,
		// The same env as config.json: the two files described the same image
		// with different environments, HOME and PWD appearing in one and not
		// the other.
		Env:        env,
		WorkingDir: workingDir,
		User:       cfg.Config.User,
	}); err != nil {
		return err
	}

	sw.mark("write artefacts")
	return nil
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func hasPrefix(values []string, prefix string) bool {
	for _, v := range values {
		if strings.HasPrefix(v, prefix) {
			return true
		}
	}
	return false
}
