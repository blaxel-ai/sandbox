package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestMultipartUploadAppliesPermissionsSentAfterFile(t *testing.T) {
	router := newFilesystemTestRouter(t)
	target := filepath.Join(t.TempDir(), "script.sh")

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "script.sh")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("#!/bin/sh\necho hi\n")); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("permissions", "0755"); err != nil {
		t.Fatal(err)
	}
	if err := form.WriteField("path", target); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPut, absoluteRoute("/filesystem", target), &body)
	request.Header.Set("Content-Type", form.FormDataContentType())
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}

	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0755 {
		t.Fatalf("mode = %o, want 755", mode)
	}
}

func TestMultipartUploadKeepsExistingFileMode(t *testing.T) {
	router := newFilesystemTestRouter(t)
	target := filepath.Join(t.TempDir(), "existing.sh")
	if err := os.WriteFile(target, []byte("old\n"), 0600); err != nil {
		t.Fatal(err)
	}

	for _, permissionsFirst := range []bool{true, false} {
		var body bytes.Buffer
		form := multipart.NewWriter(&body)
		if permissionsFirst {
			_ = form.WriteField("permissions", "0755")
		}
		part, err := form.CreateFormFile("file", "existing.sh")
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write([]byte("new\n"))
		if !permissionsFirst {
			_ = form.WriteField("permissions", "0755")
		}
		_ = form.Close()

		request := httptest.NewRequest(http.MethodPut, absoluteRoute("/filesystem", target), &body)
		request.Header.Set("Content-Type", form.FormDataContentType())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
		}
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Fatalf("permissionsFirst=%v: mode = %o, want 600", permissionsFirst, mode)
		}
	}
}
