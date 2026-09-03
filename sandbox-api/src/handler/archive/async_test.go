package archive

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// resetAsyncExport keeps one test's background export out of the next one's
// status, the state being process-wide as the sandbox is.
func resetAsyncExport(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		asyncMu.Lock()
		asyncProgress = nil
		asyncMu.Unlock()
	})
}

func waitForExport(t *testing.T, state ExportState) ExportProgress {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if progress := exportProgress(); progress != nil && progress.State == state {
			return *progress
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the export never reached %s", state)
	return ExportProgress{}
}

func TestStartExportAnswersBeforeTheUploadEnds(t *testing.T) {
	resetAsyncExport(t)
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/file"), "x", 0o644)

	// The upload is held open, which is what an archive of a large filesystem
	// does: the caller must not be waiting for it.
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL + "/delta.tar"

	progress, err := StartExport(context.Background(), options)
	if err != nil {
		t.Fatalf("failed to start the export: %v", err)
	}
	if progress.State != ExportRunning {
		t.Errorf("expected the export to be reported as running, got %s", progress.State)
	}

	// A second export is the conflict it would be synchronously.
	if _, err := StartExport(context.Background(), options); !errors.Is(err, ErrExportInProgress) {
		t.Errorf("expected a second export to be refused, got %v", err)
	}

	once.Do(func() { close(release) })
	finished := waitForExport(t, ExportSucceeded)
	if !finished.Uploaded || finished.Size == 0 {
		t.Errorf("expected the finished export to report the archive, got %+v", finished)
	}
	if status := Status(); status.Export == nil || status.Export.State != ExportSucceeded {
		t.Error("expected the archive status to carry the export's outcome")
	}
}

func TestStartExportReportsAFailedUpload(t *testing.T) {
	resetAsyncExport(t)
	root, lower := fakeSandbox(t)
	write(t, filepath.Join(root, "data/file"), "x", 0o644)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL + "/delta.tar"

	if _, err := StartExport(context.Background(), options); err != nil {
		t.Fatalf("failed to start the export: %v", err)
	}

	failed := waitForExport(t, ExportFailed)
	if failed.Error == "" {
		t.Error("expected the failure to be reported")
	}
	if failed.Uploaded {
		t.Error("a failed export must not report an archive")
	}
	// A failed export leaves the sandbox usable rather than frozen with no
	// archive to restore.
	if Quiesced() {
		t.Error("expected the sandbox to be resumed after a failed export")
	}
}

// TestStartExportRefusesWhileTheFilesystemIsAlreadyRead covers the export a
// dry run is already holding the sandbox for: the caller is told it conflicts
// instead of being told the export started and left to poll the failure of
// work that never began.
func TestStartExportRefusesWhileTheFilesystemIsAlreadyRead(t *testing.T) {
	resetAsyncExport(t)
	root, lower := fakeSandbox(t)

	if err := claimExport(); err != nil {
		t.Fatalf("failed to claim the sandbox: %v", err)
	}
	defer releaseExport()

	options := exportOptions(t, root, lower)
	options.URL = "https://store.invalid/delta.tar"
	if _, err := StartExport(context.Background(), options); !errors.Is(err, ErrExportInProgress) {
		t.Fatalf("expected the export to be refused, got %v", err)
	}
	if progress := exportProgress(); progress != nil {
		t.Errorf("a refused export must not be reported as started: %+v", progress)
	}
}

func TestStartExportRefusesADryRun(t *testing.T) {
	resetAsyncExport(t)
	root, lower := fakeSandbox(t)
	options := exportOptions(t, root, lower)
	options.DryRun = true

	_, err := StartExport(context.Background(), options)
	var invalid *InvalidOptionsError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected a dry run to be refused, got %v", err)
	}
}

func TestStartExportRefusesAnExportWithoutADestination(t *testing.T) {
	resetAsyncExport(t)
	root, lower := fakeSandbox(t)

	if _, err := StartExport(context.Background(), exportOptions(t, root, lower)); !errors.Is(err, ErrURLRequired) {
		t.Fatalf("expected an export without a destination to be refused, got %v", err)
	}
	if Quiesced() {
		t.Error("a refused export must not leave the sandbox frozen")
	}
}
