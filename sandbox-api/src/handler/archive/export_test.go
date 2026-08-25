package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
)

func exportOptions(t *testing.T, root, lower string) ExportOptions {
	t.Helper()
	t.Cleanup(func() { Resume() })
	// Keep the process state the export saves out of the real sandbox path.
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(t.TempDir(), "process-state.json"))
	return ExportOptions{root: root, ImageMountPoint: lower}
}

func TestExportDryRunDoesNotQuiesce(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "usr/bin/curl"), "ELF", 0o755)

	options := exportOptions(t, root, lower)
	options.DryRun = true

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatalf("dry run failed: %v", err)
	}
	if result.Uploaded {
		t.Error("a dry run must not upload")
	}
	if Quiesced() {
		t.Error("a dry run must leave the sandbox serving calls")
	}
	if result.Size <= 0 {
		t.Error("a dry run must report the size the upload would need")
	}
	if changeFor(result.Changes, "usr/bin/curl") == nil {
		t.Error("a dry run must report what would be archived")
	}
}

func TestExportRequiresURL(t *testing.T) {
	root, lower := fakeSandbox(t)
	if _, err := Export(context.Background(), exportOptions(t, root, lower)); err == nil {
		t.Error("expected an export without a destination to be refused")
	}
	if Quiesced() {
		t.Error("a refused export must not leave the sandbox frozen")
	}
}

func TestExportUploadsWithKnownLengthAndQuiesces(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "usr/bin/curl"), "ELF", 0o755)
	write(t, filepath.Join(lower, "etc/rsyncd.conf"), "conf", 0o644)

	var uploaded bytes.Buffer
	var contentLength string
	var chunked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected a PUT, got %s", r.Method)
		}
		contentLength = r.Header.Get("Content-Length")
		chunked = len(r.TransferEncoding) > 0
		if _, err := io.Copy(&uploaded, r.Body); err != nil {
			t.Errorf("failed to read the upload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL + "/delta.tar"

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}

	if !result.Uploaded {
		t.Error("expected the export to report the upload")
	}
	// S3 rejects a chunked body on a presigned PUT, so the length has to be
	// known up front even though the archive is streamed.
	if chunked {
		t.Error("the upload must not use chunked transfer encoding")
	}
	if length, err := strconv.ParseInt(contentLength, 10, 64); err != nil || length != result.Size {
		t.Errorf("expected Content-Length %d, got %q", result.Size, contentLength)
	}
	if int64(uploaded.Len()) != result.Size {
		t.Errorf("uploaded %d bytes, announced %d", uploaded.Len(), result.Size)
	}
	if !Quiesced() {
		t.Error("expected the sandbox to stay frozen after an export")
	}
	if Status().State != StateQuiesced {
		t.Errorf("expected state %s, got %s", StateQuiesced, Status().State)
	}

	manifest := readManifest(t, uploaded.Bytes())
	if manifest.Version != ManifestVersion {
		t.Errorf("expected manifest version %d, got %d", ManifestVersion, manifest.Version)
	}
	if manifest.Added == 0 {
		t.Error("expected the manifest to count the added paths")
	}
	if len(manifest.Deleted) != 1 || manifest.Deleted[0] != "etc/rsyncd.conf" {
		t.Errorf("expected the deletion in the manifest, got %v", manifest.Deleted)
	}
	if !manifest.Processes {
		t.Error("expected the process list to be saved by default")
	}
}

func TestExportWithoutProcessesArchivesStorageOnly(t *testing.T) {
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/file"), "x", 0o644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	saveProcesses := false
	options := exportOptions(t, root, lower)
	options.URL = server.URL
	options.SaveProcesses = &saveProcesses

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if result.Manifest.Processes {
		t.Error("expected the manifest to record that no process list was saved")
	}
}

func TestExportRefusesConcurrentExport(t *testing.T) {
	root, lower := fakeSandbox(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL
	if _, err := Export(context.Background(), options); err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if _, err := Export(context.Background(), options); err == nil {
		t.Error("expected a second export to be refused while the sandbox is frozen")
	}
}

func TestExportReportsRejectedUpload(t *testing.T) {
	root, lower := fakeSandbox(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code></Error>"))
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL + "/delta.tar?X-Amz-Signature=deadbeef"

	_, err := Export(context.Background(), options)
	if err == nil {
		t.Fatal("expected a rejected upload to fail the export")
	}
	// The destination is a presigned URL: reporting it back would leak the
	// credentials it carries into API responses and logs.
	if bytes.Contains([]byte(err.Error()), []byte("X-Amz-Signature")) {
		t.Errorf("the error must not carry the presigned URL: %v", err)
	}
	if !bytes.Contains([]byte(err.Error()), []byte("403")) {
		t.Errorf("expected the storage status in the error, got %v", err)
	}
}

func readManifest(t *testing.T, archived []byte) Manifest {
	t.Helper()
	reader := tar.NewReader(bytes.NewReader(archived))
	for {
		header, err := reader.Next()
		if err == io.EOF {
			t.Fatal("archive has no manifest")
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Name != ManifestName {
			continue
		}
		var manifest Manifest
		if err := json.NewDecoder(reader).Decode(&manifest); err != nil {
			t.Fatal(err)
		}
		return manifest
	}
}
