// blbuild turns a source context into the EROFS artefact set the compute plane
// boots from, from inside a sandbox, without ever holding cloud credentials.
//
//	blbuild -targets /scratch/targets.json
//
// targets.json is issued by the control plane and contains presigned URLs only:
// one GET for the source archive, one PUT per small artefact, and one PUT per
// part of the rootfs. Everything blbuild sends is plain HTTP against those URLs,
// so a leaked target file buys nothing but overwriting the artefact of the build
// that leaked it — which matters, because a build runs a customer Dockerfile and
// therefore arbitrary customer code.
//
// It writes result.json: per-step timings, the rootfs size, and the part ETags
// the control plane needs to finalize the multipart upload.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var (
		targetsPath = flag.String("targets", "/scratch/targets.json", "path to the presigned targets issued by the control plane")
		resultPath  = flag.String("result", "/scratch/result.json", "where to write the build result")
		workDir     = flag.String("work", "/scratch/work", "scratch directory for the build")
		outDir      = flag.String("out", "/scratch/out", "directory holding the produced artefacts")
		contextDir  = flag.String("context", "", "build a local directory instead of fetching the source archive (debugging)")
		noUpload    = flag.Bool("no-upload", false, "build and validate but publish nothing (debugging)")
	)
	flag.Parse()

	// SIGTERM matters: the control plane deletes the sandbox when a build times
	// out, and a half-written result is more useful than none.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	targets, err := LoadTargets(*targetsPath)
	if err != nil {
		fatal("reading targets: %v", err)
	}

	shutdown, err := InitTracing(ctx, targets.TraceParent)
	if err != nil {
		// Losing traces must never lose a build.
		warn("tracing disabled: %v", err)
	} else {
		defer shutdown()
	}

	b := &Builder{
		Targets:    targets,
		WorkDir:    *workDir,
		OutDir:     *outDir,
		ContextDir: *contextDir,
		NoUpload:   *noUpload,
	}

	// TraceContext, not ctx: this is what attaches the build to the trace of the
	// orchestration that started it. Passing ctx here would still produce spans,
	// just orphaned ones — which looks fine until you try to explain a slow
	// build and have nothing linking it to its execution.
	result, runErr := b.Run(TraceContext(ctx))

	// The result is written even on failure: its timings are what tell us which
	// step broke, and the ETags let the control plane abort the upload cleanly.
	if result != nil {
		if err := writeResult(*resultPath, result); err != nil {
			warn("writing result: %v", err)
		}
	}
	if runErr != nil {
		fatal("build failed: %v", runErr)
	}

	fmt.Printf("\n=== total %.2fs | rootfs %d MiB | %d part(s)\n",
		result.TotalSeconds, result.RootfsBytes/(1024*1024), len(result.Parts))
}

func writeResult(path string, r *Result) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func warn(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "warn: "+format+"\n", args...)
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
	os.Exit(1)
}

// step records one pipeline stage in the result and prints it, so a build log
// reads like the timing table it produces.
type step struct {
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
}

type stopwatch struct {
	start time.Time
	last  time.Time
	steps []step
}

func newStopwatch() *stopwatch {
	now := time.Now()
	return &stopwatch{start: now, last: now}
}

func (s *stopwatch) mark(name string) {
	now := time.Now()
	d := now.Sub(s.last).Seconds()
	s.steps = append(s.steps, step{Name: name, Seconds: d})
	fmt.Printf("%-30s %7.2fs  (t+%.2fs)\n", name, d, now.Sub(s.start).Seconds())
	s.last = now
}

func (s *stopwatch) total() float64 {
	return time.Since(s.start).Seconds()
}
