package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	cmd := append([]string{"/bin/metamorph-wrapper", workingDir},
		append(cfg.Config.Entrypoint, cfg.Config.Cmd...)...)
	if err := os.WriteFile(filepath.Join(b.OutDir, cmdlineName),
		[]byte(strings.Join(cmd, " ")+"\n"), 0o644); err != nil {
		return err
	}

	// HOME is added when the image does not set it, matching the current
	// builder: without it a shell started in the VM has no home.
	env := cfg.Config.Env
	if !hasPrefix(env, "HOME=") {
		env = append(env, "HOME=/root")
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

	// image.json carries the container metadata as-is.
	if err := writeJSON(filepath.Join(b.OutDir, imageName), cfg.Config); err != nil {
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
