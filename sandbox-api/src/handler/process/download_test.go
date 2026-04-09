package process

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRetryableDownloadSuccess verifies a clean download with no retries needed.
func TestRetryableDownloadSuccess(t *testing.T) {
	body := "binary-content-here"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != downloadUserAgent {
			t.Errorf("expected User-Agent %q, got %q", downloadUserAgent, r.Header.Get("User-Agent"))
		}
		if r.Header.Get("Accept") != "application/octet-stream" {
			t.Errorf("expected Accept application/octet-stream, got %q", r.Header.Get("Accept"))
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	resp, err := retryableDownload(client, server.URL)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if string(data) != body {
		t.Errorf("expected body %q, got %q", body, string(data))
	}
}

// TestRetryableDownloadRetriesOn500 verifies that 5xx errors are retried
// and the download succeeds once the server recovers.
func TestRetryableDownloadRetriesOn500(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n <= 2 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("server error"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	resp, err := retryableDownload(client, server.URL)
	if err != nil {
		t.Fatalf("expected success after retries, got: %v", err)
	}
	defer resp.Body.Close()

	if got := attempts.Load(); got != 3 {
		t.Errorf("expected 3 attempts, got %d", got)
	}
}

// TestRetryableDownloadRetriesOn429 verifies that rate limit responses (429)
// are retried.
func TestRetryableDownloadRetriesOn429(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte("rate limited"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	resp, err := retryableDownload(client, server.URL)
	if err != nil {
		t.Fatalf("expected success after rate limit retry, got: %v", err)
	}
	defer resp.Body.Close()

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 attempts, got %d", got)
	}
}

// TestRetryableDownloadRetriesOn403 verifies that 403 (anti-scraping/CDN) errors
// are retried, since GitHub can return transient 403s.
func TestRetryableDownloadRetriesOn403(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusForbidden)
			w.Write([]byte("forbidden"))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	resp, err := retryableDownload(client, server.URL)
	if err != nil {
		t.Fatalf("expected success after 403 retry, got: %v", err)
	}
	defer resp.Body.Close()

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 attempts, got %d", got)
	}
}

// TestRetryableDownloadNoRetryOn404 verifies that a 404 fails immediately
// without retrying, since it indicates a permanent problem (wrong URL/version).
func TestRetryableDownloadNoRetryOn404(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("not found"))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	_, err := retryableDownload(client, server.URL)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Errorf("expected 404 in error, got: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("expected 1 attempt (no retry on 404), got %d", got)
	}
}

// TestRetryableDownloadExhaustsRetries verifies that after all retries are
// exhausted, the function returns an error with the attempt count.
func TestRetryableDownloadExhaustsRetries(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("always failing"))
	}))
	defer server.Close()

	client := newDownloadHTTPClient()
	_, err := retryableDownload(client, server.URL)
	if err == nil {
		t.Fatal("expected error after exhausting retries, got nil")
	}

	expectedAttempts := int32(downloadMaxRetries + 1)
	if got := attempts.Load(); got != expectedAttempts {
		t.Errorf("expected %d attempts, got %d", expectedAttempts, got)
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("after %d attempts", expectedAttempts)) {
		t.Errorf("expected attempt count in error, got: %v", err)
	}
}

// TestRetryableDownloadNetworkError verifies that network-level errors
// (e.g., connection refused) are retried.
func TestRetryableDownloadNetworkError(t *testing.T) {
	// Start a server and immediately close it to get a connection-refused error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close()

	client := newDownloadHTTPClient()
	_, err := retryableDownload(client, serverURL)
	if err == nil {
		t.Fatal("expected error for unreachable server, got nil")
	}
	if !strings.Contains(err.Error(), fmt.Sprintf("after %d attempts", downloadMaxRetries+1)) {
		t.Errorf("expected exhausted retries error, got: %v", err)
	}
}

// TestDownloadReleaseWithDetailsURLConstruction verifies that the download URL
// is built correctly for different version formats.
func TestDownloadReleaseWithDetailsURLConstruction(t *testing.T) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	expectedAsset := fmt.Sprintf("sandbox-api-%s-%s", goos, goarch)

	tests := []struct {
		name        string
		version     string
		expectedURL string
	}{
		{
			name:        "latest version",
			version:     "latest",
			expectedURL: fmt.Sprintf("/latest/download/%s", expectedAsset),
		},
		{
			name:        "develop branch",
			version:     "develop",
			expectedURL: fmt.Sprintf("/download/develop/%s", expectedAsset),
		},
		{
			name:        "specific tag",
			version:     "v1.2.3",
			expectedURL: fmt.Sprintf("/download/v1.2.3/%s", expectedAsset),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requestedPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestedPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("fake-binary"))
			}))
			defer server.Close()

			path, url, written, err := downloadReleaseWithDetails(tt.version, server.URL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			defer os.Remove(path)

			if requestedPath != tt.expectedURL {
				t.Errorf("expected request path %q, got %q", tt.expectedURL, requestedPath)
			}
			if !strings.Contains(url, tt.expectedURL) {
				t.Errorf("expected URL to contain %q, got %q", tt.expectedURL, url)
			}
			if written != int64(len("fake-binary")) {
				t.Errorf("expected %d bytes written, got %d", len("fake-binary"), written)
			}
		})
	}
}

// TestDownloadReleaseWithDetailsWritesExecutableFile verifies the downloaded
// binary is written to disk as an executable file.
func TestDownloadReleaseWithDetailsWritesExecutableFile(t *testing.T) {
	binaryContent := "#!/bin/sh\necho hello"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binaryContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(binaryContent))
	}))
	defer server.Close()

	path, _, written, err := downloadReleaseWithDetails("develop", server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer os.Remove(path)

	if written != int64(len(binaryContent)) {
		t.Errorf("expected %d bytes, got %d", len(binaryContent), written)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat downloaded file: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Error("expected file to be executable")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read downloaded file: %v", err)
	}
	if string(data) != binaryContent {
		t.Errorf("file content mismatch: got %q", string(data))
	}
}

// TestDownloadReleaseWithDetailsIncompleteDownload verifies that a truncated
// response (Content-Length mismatch) is detected and rejected.
// When the server declares a Content-Length but sends fewer bytes, Go's HTTP
// client returns an "unexpected EOF" during io.Copy, which our code surfaces
// as a write failure. Either detection path (io.Copy error or our own
// Content-Length check) correctly rejects the truncated download.
func TestDownloadReleaseWithDetailsIncompleteDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Claim 1000 bytes but only send 10
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short-body"))
	}))
	defer server.Close()

	path, _, _, err := downloadReleaseWithDetails("develop", server.URL)
	if path != "" {
		os.Remove(path)
	}
	if err == nil {
		t.Fatal("expected error for incomplete download, got nil")
	}
	// Accept either detection path: io.Copy EOF or our Content-Length check
	errMsg := err.Error()
	if !strings.Contains(errMsg, "incomplete download") &&
		!strings.Contains(errMsg, "unexpected EOF") &&
		!strings.Contains(errMsg, "failed to write binary") {
		t.Errorf("expected truncation error, got: %v", err)
	}
}

// TestDownloadReleaseWithDetailsRetriesThenSucceeds is an end-to-end test
// simulating a transient GitHub outage during binary download.
func TestDownloadReleaseWithDetailsRetriesThenSucceeds(t *testing.T) {
	var attempts atomic.Int32
	binaryContent := "real-binary-data"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := attempts.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusBadGateway)
			w.Write([]byte("upstream error"))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(binaryContent)))
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(binaryContent))
	}))
	defer server.Close()

	path, _, written, err := downloadReleaseWithDetails("develop", server.URL)
	if err != nil {
		t.Fatalf("expected success after retry, got: %v", err)
	}
	defer os.Remove(path)

	if got := attempts.Load(); got != 2 {
		t.Errorf("expected 2 attempts, got %d", got)
	}
	if written != int64(len(binaryContent)) {
		t.Errorf("expected %d bytes, got %d", len(binaryContent), written)
	}
}

// TestNewDownloadHTTPClient verifies that the HTTP client is configured
// with the expected timeout.
func TestNewDownloadHTTPClient(t *testing.T) {
	client := newDownloadHTTPClient()
	if client.Timeout != downloadTotalTimeout {
		t.Errorf("expected timeout %v, got %v", downloadTotalTimeout, client.Timeout)
	}
	if client.Transport == nil {
		t.Error("expected custom transport, got nil")
	}
}
