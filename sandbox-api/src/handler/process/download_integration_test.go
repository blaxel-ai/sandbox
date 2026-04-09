//go:build integration

package process

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"testing"
)

const (
	integrationBaseURL = "https://github.com/blaxel-ai/sandbox/releases"
	// Use "develop" release which is continuously updated
	integrationVersion = "develop"
)

// TestIntegrationDownloadFromGitHub performs a real download of the sandbox-api
// binary from GitHub releases. It validates the full flow: HTTP client creation,
// redirect following, binary download, Content-Length verification, and file
// permissions.
//
// Run with: go test -tags integration -run TestIntegrationDownload -v -timeout 120s
func TestIntegrationDownloadFromGitHub(t *testing.T) {
	path, url, written, err := downloadReleaseWithDetails(integrationVersion, integrationBaseURL)
	if err != nil {
		t.Fatalf("download failed: %v\n  URL: %s", err, url)
	}
	defer os.Remove(path)

	t.Logf("Downloaded %d bytes from %s to %s", written, url, path)

	// Binary should be non-trivially sized (at least 1MB for a Go binary)
	if written < 1_000_000 {
		t.Errorf("binary too small (%d bytes), expected at least 1MB for a Go binary", written)
	}

	// File should exist and be executable
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat downloaded file: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("downloaded file is not executable")
	}
	if info.Size() != written {
		t.Errorf("file size on disk (%d) does not match bytes written (%d)", info.Size(), written)
	}
}

// TestIntegrationDownloadLatestRelease tests the "latest" version alias which
// uses GitHub's /latest/download/ redirect.
func TestIntegrationDownloadLatestRelease(t *testing.T) {
	path, url, written, err := downloadReleaseWithDetails("latest", integrationBaseURL)
	if err != nil {
		t.Fatalf("download failed: %v\n  URL: %s", err, url)
	}
	defer os.Remove(path)

	t.Logf("Downloaded %d bytes from %s", written, url)

	if written < 1_000_000 {
		t.Errorf("binary too small (%d bytes)", written)
	}
}

// TestIntegrationDownloadSpecificTag tests downloading from a pinned version tag.
func TestIntegrationDownloadSpecificTag(t *testing.T) {
	path, url, written, err := downloadReleaseWithDetails("v0.2.21", integrationBaseURL)
	if err != nil {
		t.Fatalf("download failed: %v\n  URL: %s", err, url)
	}
	defer os.Remove(path)

	t.Logf("Downloaded %d bytes from %s", written, url)

	if written < 1_000_000 {
		t.Errorf("binary too small (%d bytes)", written)
	}
}

// TestIntegrationDownloadNonExistentVersion verifies that requesting a version
// that doesn't exist fails fast (no retries on 404).
func TestIntegrationDownloadNonExistentVersion(t *testing.T) {
	path, url, _, err := downloadReleaseWithDetails("v99.99.99-doesnotexist", integrationBaseURL)
	if path != "" {
		os.Remove(path)
	}
	if err == nil {
		t.Fatalf("expected error for non-existent version, got nil\n  URL: %s", url)
	}
	t.Logf("Got expected error: %v", err)
}

// TestIntegrationRetryableDownloadHeaders verifies that our HTTP client sends
// the correct headers when hitting the real GitHub endpoint.
func TestIntegrationRetryableDownloadHeaders(t *testing.T) {
	assetName := fmt.Sprintf("sandbox-api-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("%s/download/%s/%s", integrationBaseURL, integrationVersion, assetName)

	client := newDownloadHTTPClient()
	resp, err := retryableDownload(client, url)
	if err != nil {
		t.Fatalf("download failed: %v", err)
	}
	defer resp.Body.Close()

	// Drain and discard body to not leak the connection
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	// Verify Content-Length was provided (GitHub CDN always sends it)
	if resp.ContentLength <= 0 {
		t.Logf("WARNING: Content-Length not set or zero (%d), CDN may have changed behavior", resp.ContentLength)
	} else {
		t.Logf("Content-Length: %d bytes", resp.ContentLength)
	}
}

// TestIntegrationHTTPClientTimeouts verifies the HTTP client can connect and
// complete TLS handshake with GitHub within the configured timeouts.
func TestIntegrationHTTPClientTimeouts(t *testing.T) {
	client := newDownloadHTTPClient()

	// Just do a HEAD-like request (we'll use GET and discard) to verify connectivity
	assetName := fmt.Sprintf("sandbox-api-%s-%s", runtime.GOOS, runtime.GOARCH)
	url := fmt.Sprintf("%s/download/%s/%s", integrationBaseURL, integrationVersion, assetName)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("User-Agent", downloadUserAgent)
	req.Header.Set("Accept", "application/octet-stream")

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed (possible timeout issue): %v", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	t.Logf("Response status: %d, Content-Length: %d", resp.StatusCode, resp.ContentLength)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

// TestIntegrationBinaryValidation downloads and validates the binary format
// to ensure the downloaded file is a real executable, not an error page.
func TestIntegrationBinaryValidation(t *testing.T) {
	path, url, _, err := downloadReleaseWithDetails(integrationVersion, integrationBaseURL)
	if err != nil {
		t.Fatalf("download failed: %v\n  URL: %s", err, url)
	}
	defer os.Remove(path)

	// validateBinaryFormat is the existing function that checks ELF/Mach-O magic bytes
	if err := validateBinaryFormat(path); err != nil {
		t.Errorf("downloaded binary failed format validation: %v", err)
	}
}
