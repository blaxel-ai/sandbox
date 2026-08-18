package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// Targets is the presigned-URL bundle the control plane issues for one build.
// It is the only thing the sandbox receives: no credentials, no role, no token.
type Targets struct {
	// SourceURL is a presigned GET for the build context archive.
	SourceURL string `json:"sourceUrl"`
	// Puts maps an artefact name (cmdline.txt, config.json, image.json,
	// bin/kernel) to a presigned PUT.
	Puts map[string]string `json:"puts"`
	// Initrd carries the multipart handle and one presigned PUT per part.
	Initrd InitrdUpload `json:"initrd"`
	// ExpiresAt is when the URLs stop working, in unix milliseconds. Used to
	// fail early with a clear message instead of a wall of 403s.
	ExpiresAt int64 `json:"expiresAt"`
	// UploadFlows is how many part uploads to run concurrently.
	UploadFlows int `json:"uploadFlows"`
	// TraceParent is the W3C trace context of the step function execution, so
	// this build shows up as a subtree of it rather than an orphan trace.
	TraceParent string `json:"traceparent"`
}

// defaultUploadFlows is the fallback when the targets do not carry one.
//
// 24, not 8: sandbox egress is capped per connection at ~0.2MB/s, and measured
// aggregate throughput was still climbing linearly at 24 flows (4.8MB/s). On a
// fast path the same 24 flows are harmless.
const defaultUploadFlows = 24

// InitrdUpload is the multipart upload of the rootfs.
type InitrdUpload struct {
	Key         string   `json:"key"`
	UploadID    string   `json:"uploadId"`
	PartSizeMiB int      `json:"partSizeMib"`
	PartURLs    []string `json:"partUrls"`
}

// PartSizeBytes is the split size for the rootfs.
func (i InitrdUpload) PartSizeBytes() int64 {
	return int64(i.PartSizeMiB) * 1024 * 1024
}

// Result is written to result.json and consumed by the control plane: the
// timings feed observability, the parts feed CompleteMultipartUpload.
type Result struct {
	UploadID     string          `json:"uploadId"`
	Key          string          `json:"key"`
	Parts        []CompletedPart `json:"parts"`
	RootfsBytes  int64           `json:"rootfsBytes"`
	TotalSeconds float64         `json:"totalSeconds"`
	Steps        []step          `json:"steps"`
	// Layers is how many OCI layers the build produced, useful when reading a
	// slow build's trace.
	Layers int `json:"layers"`
	// Incremental reports whether the fast per-layer path was used or whether
	// it fell back to a single mkfs over an extracted tree.
	Incremental bool `json:"incremental"`
	// Cmdline is what the guest actually executes, wrapper and working
	// directory included. It travels in the result because a boot failure is
	// diagnosed from it and the build sandbox is gone by then.
	Cmdline string `json:"cmdline,omitempty"`
	// Error is the failure reason, product-neutral: this string reaches the
	// customer through the build events.
	Error string `json:"error,omitempty"`
}

// CompletedPart is one uploaded part.
type CompletedPart struct {
	PartNumber int    `json:"partNumber"`
	ETag       string `json:"etag"`
}

// LoadTargets reads and sanity-checks the targets file. Every check here turns a
// confusing mid-build failure into an immediate, explicit one.
func LoadTargets(path string) (*Targets, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var t Targets
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if t.SourceURL == "" {
		return nil, fmt.Errorf("%s has no sourceUrl", path)
	}
	if len(t.Puts) == 0 {
		return nil, fmt.Errorf("%s has no puts", path)
	}
	if t.Initrd.UploadID == "" || len(t.Initrd.PartURLs) == 0 {
		return nil, fmt.Errorf("%s has no rootfs upload", path)
	}
	if t.Initrd.PartSizeMiB <= 0 {
		return nil, fmt.Errorf("%s has a zero part size", path)
	}
	if t.UploadFlows <= 0 {
		t.UploadFlows = defaultUploadFlows
	}
	return &t, nil
}
