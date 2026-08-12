package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
)

// httpClient is shared on purpose. One client means one connection pool and
// keep-alive across the whole build, which matters more than it sounds: a fresh
// connection pays DNS resolution, and a resolver with a dead first nameserver
// turns that into two seconds per request. Reusing the connection made the same
// request 4.6ms instead of 2.07s in testing.
var httpClient = &http.Client{
	Timeout: 30 * time.Minute,
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
	},
}

// download fetches a presigned URL to a file.
func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return statusError("GET", resp.StatusCode)
	}

	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(f, resp.Body)
	return err
}

// uploadSmallFiles publishes the artefacts that accompany the rootfs.
func (b *Builder) uploadSmallFiles(ctx context.Context, sw *stopwatch) error {
	ctx, span := tracer().Start(ctx, "upload.artefacts")
	defer span.End()

	// The map keys are the names the control plane presigned, so a mismatch here
	// is a contract break worth failing on rather than skipping quietly.
	files := map[string]string{
		cmdlineName:  filepath.Join(b.OutDir, cmdlineName),
		configName:   filepath.Join(b.OutDir, configName),
		imageName:    filepath.Join(b.OutDir, imageName),
		"bin/kernel": filepath.Join(b.OutDir, kernelName),
	}
	for name, path := range files {
		url, ok := b.Targets.Puts[name]
		if !ok {
			return fmt.Errorf("no upload authorization for %s", name)
		}
		if _, err := putFile(ctx, url, path); err != nil {
			return fmt.Errorf("publishing %s: %w", name, err)
		}
	}
	sw.mark("upload artefacts")
	return nil
}

// uploadRootfs splits the image and uploads the parts concurrently.
//
// Concurrency is what makes this fast, and it is configured rather than fixed
// because the right value depends on the network between the sandbox and the
// bucket: on one path a single flow reached 20MB/s, on another it was capped
// around 0.19MB/s per flow and only parallelism recovered the bandwidth.
func (b *Builder) uploadRootfs(ctx context.Context, rootfs string, sw *stopwatch) ([]CompletedPart, error) {
	ctx, span := tracer().Start(ctx, "upload.rootfs")
	defer span.End()

	info, err := os.Stat(rootfs)
	if err != nil {
		return nil, err
	}
	partSize := b.Targets.Initrd.PartSizeBytes()
	nparts := int((info.Size() + partSize - 1) / partSize)
	if nparts > len(b.Targets.Initrd.PartURLs) {
		return nil, fmt.Errorf(
			"the filesystem image needs %d upload slots but only %d were authorized",
			nparts, len(b.Targets.Initrd.PartURLs))
	}
	span.SetAttributes(
		attribute.Int("upload.parts", nparts),
		attribute.Int("upload.flows", b.Targets.UploadFlows),
		attribute.Int64("upload.bytes", info.Size()),
	)

	type outcome struct {
		part CompletedPart
		err  error
	}
	results := make(chan outcome, nparts)
	sem := make(chan struct{}, b.Targets.UploadFlows)
	var wg sync.WaitGroup

	for i := 0; i < nparts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			offset := int64(idx) * partSize
			length := partSize
			if remaining := info.Size() - offset; remaining < length {
				length = remaining
			}
			etag, err := b.uploadPart(ctx, rootfs, idx, offset, length)
			results <- outcome{part: CompletedPart{PartNumber: idx + 1, ETag: etag}, err: err}
		}(i)
	}
	wg.Wait()
	close(results)

	parts := make([]CompletedPart, 0, nparts)
	for r := range results {
		if r.err != nil {
			return nil, fmt.Errorf("publishing the filesystem image: %w", r.err)
		}
		parts = append(parts, r.part)
	}
	// The order the goroutines finish in is not the part order, and the finalize
	// call requires ascending part numbers.
	sort.Slice(parts, func(i, j int) bool { return parts[i].PartNumber < parts[j].PartNumber })

	sw.mark(fmt.Sprintf("upload rootfs (%d MiB, %d parts)", info.Size()/(1024*1024), nparts))
	return parts, nil
}

// uploadPart sends one slice of the image. It streams a section of the file
// rather than splitting it on disk: writing a second copy of a multi-GB image to
// the volume would be both slow and, on a tight scratch, fatal.
func (b *Builder) uploadPart(ctx context.Context, path string, idx int, offset, length int64) (string, error) {
	ctx, span := tracer().Start(ctx, "upload.part")
	defer span.End()
	span.SetAttributes(
		attribute.Int("part.number", idx+1),
		attribute.Int64("part.bytes", length),
	)

	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		b.Targets.Initrd.PartURLs[idx], io.NewSectionReader(f, offset, length))
	if err != nil {
		return "", err
	}
	// ContentLength must be explicit: without it the request is chunked, which a
	// presigned URL does not accept.
	req.ContentLength = length

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", statusError("PUT", resp.StatusCode)
	}

	etag := resp.Header.Get("ETag")
	if etag == "" {
		// Without an ETag the upload cannot be finalized, so treat it as a
		// failure now rather than at the finalize step.
		return "", fmt.Errorf("part %d was accepted without an identifier", idx+1)
	}
	return etag, nil
}

func putFile(ctx context.Context, url, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, f)
	if err != nil {
		return "", err
	}
	req.ContentLength = info.Size()

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", statusError("PUT", resp.StatusCode)
	}
	return resp.Header.Get("ETag"), nil
}

// statusError keeps the storage provider out of the message. These errors reach
// the customer through the build events, and the response body of a rejected
// presigned request names the provider and echoes request IDs.
func statusError(method string, code int) error {
	switch code {
	case http.StatusForbidden:
		return fmt.Errorf("%s rejected: the upload authorization is no longer valid", method)
	case http.StatusNotFound:
		return fmt.Errorf("%s rejected: the target no longer exists", method)
	default:
		return fmt.Errorf("%s rejected with status %d", method, code)
	}
}
