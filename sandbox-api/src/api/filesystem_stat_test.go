package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHeadFilesystemIsAStat(t *testing.T) {
	router := newFilesystemTestRouter(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(file, []byte("hello"), 0755); err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := os.Chtimes(file, modified, modified); err != nil {
		t.Fatal(err)
	}

	response := serveFilesystem(router, http.MethodHead, absoluteRoute("/filesystem", file))
	if response.Code != http.StatusOK || response.Body.Len() != 0 {
		t.Fatalf("file: HTTP %d, body %q", response.Code, response.Body.String())
	}
	for header, want := range map[string]string{
		"Content-Length": "5",
		"Last-Modified":  "Mon, 06 May 2024 07:08:09 GMT",
		"X-File-Type":    "file",
		"X-File-Mode":    "755",
	} {
		if got := response.Header().Get(header); got != want {
			t.Errorf("file: %s = %q, want %q", header, got, want)
		}
	}

	response = serveFilesystem(router, http.MethodHead, absoluteRoute("/filesystem", dir))
	if response.Code != http.StatusOK || response.Header().Get("X-File-Type") != "directory" {
		t.Fatalf("directory: HTTP %d, X-File-Type %q", response.Code, response.Header().Get("X-File-Type"))
	}

	response = serveFilesystem(router, http.MethodHead, absoluteRoute("/filesystem", filepath.Join(dir, "missing")))
	if response.Code != http.StatusOK || response.Body.Len() != 0 || response.Header().Get("X-File-Type") != "" {
		t.Fatalf("missing: HTTP %d, body %q, X-File-Type %q, want an empty 200", response.Code, response.Body.String(), response.Header().Get("X-File-Type"))
	}
}
