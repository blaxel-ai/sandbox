package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func exportOptions(t *testing.T, root, lower string) ExportOptions {
	t.Helper()
	t.Cleanup(func() { forceResume() })
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
	var contentType string
	var chunked bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected a PUT, got %s", r.Method)
		}
		contentLength = r.Header.Get("Content-Length")
		contentType = r.Header.Get("Content-Type")
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
	// A presigned URL signs the headers it was generated for, so sending a
	// content type the signature does not cover fails with SignatureDoesNotMatch.
	if contentType != "" {
		t.Errorf("the upload must not send a Content-Type, got %q", contentType)
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

func TestExportSendsTheSignedHeaders(t *testing.T) {
	// A storage class has to be signed into the presigned URL and sent with the
	// request, so the caller passes what it signed.
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/file"), "x", 0o644)

	var storageClass, length string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		storageClass = r.Header.Get("x-amz-storage-class")
		length = r.Header.Get("Content-Length")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL
	options.Headers = map[string]string{
		"x-amz-storage-class": "GLACIER_IR",
		// The request owns its length: honouring this one would announce a size
		// that does not match the archive.
		"Content-Length": "1",
	}

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if storageClass != "GLACIER_IR" {
		t.Errorf("expected the storage class to be sent, got %q", storageClass)
	}
	if parsed, err := strconv.ParseInt(length, 10, 64); err != nil || parsed != result.Size {
		t.Errorf("expected Content-Length %d, got %q", result.Size, length)
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

func TestExportRefusesADryRunWhileAnExportIsReadingTheImage(t *testing.T) {
	// A dry run is not held back by the freeze, but it mounts the pristine
	// image at the same place: replacing that mount under a running export
	// would have it compare the filesystem against an empty directory and
	// archive the whole root.
	root, lower := fakeSandbox(t)
	uploading := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(uploading)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL
	done := make(chan error, 1)
	go func() {
		_, err := Export(context.Background(), options)
		done <- err
	}()
	<-uploading

	dry := options
	dry.URL = ""
	dry.DryRun = true
	if _, err := Export(context.Background(), dry); !errors.Is(err, ErrExportInProgress) {
		t.Errorf("expected the dry run to be refused while an export holds the image mount, got %v", err)
	}

	close(release)
	if err := <-done; err != nil {
		t.Fatalf("export failed: %v", err)
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

func TestWaitForExitWatchesTheOSProcess(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start a process to wait on: %v", err)
	}
	// The manager marks a process stopped as soon as SIGTERM is sent, so only
	// the OS process can tell the export whether it still writes.
	candidate := stoppedProcess{identifier: "1", pid: cmd.Process.Pid}

	if pending := waitForExit([]stoppedProcess{candidate}, 200*time.Millisecond); len(pending) != 1 {
		t.Fatalf("expected the process still running to be reported for the kill, got %d", len(pending))
	}

	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("failed to kill the process: %v", err)
	}
	if _, err := cmd.Process.Wait(); err != nil {
		t.Fatalf("failed to reap the process: %v", err)
	}

	if pending := waitForExit([]stoppedProcess{candidate}, time.Second); len(pending) != 0 {
		t.Errorf("expected the exited process to settle, got %d pending", len(pending))
	}
}

func TestWaitForExitSettlesOnTheCompletionChannel(t *testing.T) {
	done := make(chan struct{})
	close(done)
	// The PID is this very test process, so anything but the completion channel
	// would report it alive.
	candidate := stoppedProcess{identifier: "1", pid: os.Getpid(), done: done}

	if pending := waitForExit([]stoppedProcess{candidate}, 100*time.Millisecond); len(pending) != 0 {
		t.Errorf("expected the completed process to settle, got %d pending", len(pending))
	}
}

func TestExportRefusesAnUntrustworthyImageSource(t *testing.T) {
	// The export compares the live filesystem against the image the sandbox
	// booted from, and what it compares against decides what ends up in the
	// archive: a comparison against an empty directory reports the whole
	// filesystem as changed, so a request cannot name one.
	empty := t.TempDir()
	regular := filepath.Join(t.TempDir(), "device")
	if err := os.WriteFile(regular, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]ExportOptions{
		"a mount point that is not a mount": {ImageMountPoint: empty},
		"a relative mount point":            {ImageMountPoint: "mnt/lower"},
		// A real mount is not enough: compared against /proc, /dev or an attached
		// drive the archive becomes a copy of the whole root, uploaded wherever the
		// request says.
		"another filesystem's mount point": {ImageMountPoint: "/proc"},
		"a device outside /dev":            {ImageDevice: regular},
		"a device path that is not clean":  {ImageDevice: "/dev/../" + strings.TrimPrefix(regular, "/")},
	}
	for name, options := range cases {
		t.Run(name, func(t *testing.T) {
			if err := options.validateImageSource(); err == nil {
				t.Errorf("expected %s to be refused", name)
			}
		})
	}

	// Comparing the root against itself is how a dry run is checked, and it
	// reports nothing, so it stays allowed.
	if err := (ExportOptions{ImageMountPoint: "/"}).validateImageSource(); err != nil {
		t.Errorf("expected the root to be accepted, got %v", err)
	}

	// The real device, when there is one, is accepted.
	if _, err := os.Stat("/dev/null"); err == nil {
		if err := (ExportOptions{ImageDevice: "/dev/null"}).validateImageSource(); err != nil {
			t.Errorf("expected a real device to be accepted, got %v", err)
		}
	}
}
