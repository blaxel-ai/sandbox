package archive

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

// resetRestore gives a test the state a boot starts from: these are package
// variables, and a restore left behind would answer the next test's questions.
func resetRestore(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		restoreMu.Lock()
		restoreProgress = nil
		restoreMu.Unlock()

		quiesceMu.Lock()
		quiesceStatus = QuiesceStatus{State: StateActive}
		quiesceMu.Unlock()
	})
}

// A restore of a large archive takes minutes. The sandbox answers all along, so
// what it answers has to say that it is being restored and how far that got -
// and it has to refuse the calls that would write into a filesystem halfway
// between the image and the archive.
func TestImportReportsItsProgressWhileItRuns(t *testing.T) {
	resetRestore(t)

	body := buildArchive(t, Manifest{Version: ManifestVersion, Root: "/", CreatedAt: time.Unix(1700000000, 0)}, nil, []archiveMember{
		{name: "app/main.go", content: "package main", mode: 0o644},
	})

	// The response is held open halfway through, which is where the sandbox
	// spends most of a real restore.
	halfway := make(chan struct{})
	released := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cut := len(body) / 2
		_, _ = w.Write(body[:cut])
		w.(http.Flusher).Flush()
		close(halfway)
		<-released
		_, _ = w.Write(body[cut:])
	}))
	defer server.Close()

	root := t.TempDir()
	relaunch := false
	done := make(chan error, 1)
	go func() {
		_, err := Import(context.Background(), ImportOptions{
			URL:               server.URL + "/archive.tar",
			root:              root,
			MarkerPath:        filepath.Join(root, "marker.json"),
			RelaunchProcesses: &relaunch,
		})
		done <- err
	}()

	<-halfway
	waitFor(t, func() bool { return Status().State == StateRestoring })

	status := Status()
	if status.Restore == nil {
		t.Fatal("a sandbox being restored must report the restore, so a client can show it instead of a sandbox that looks ready")
	}
	if status.Restore.State != RestoreDownloading && status.Restore.State != RestoreExtracting {
		t.Errorf("a running restore is downloading or extracting, got %q", status.Restore.State)
	}
	if status.ReadOnlyRoot {
		t.Error("the root must stay writable while the archive is written to it")
	}
	if !Quiesced() {
		t.Error("a sandbox whose filesystem is being written must refuse the calls that would write to it too")
	}

	close(released)
	if err := <-done; err != nil {
		t.Fatalf("import failed: %v", err)
	}

	status = Status()
	if status.State != StateActive {
		t.Errorf("a restored sandbox serves every route again, got %q", status.State)
	}
	if status.Restore == nil || status.Restore.State != RestoreSucceeded {
		t.Fatalf("a finished restore is reported as such, got %+v", status.Restore)
	}
	if status.Restore.Downloaded == 0 {
		t.Error("the bytes read from the archive are what a client shows as progress")
	}
	if status.Restore.Restored == 0 {
		t.Error("a restore that wrote files reports them")
	}
	if status.Restore.FinishedAt == nil {
		t.Error("a finished restore says when it finished")
	}
}

// A failed restore must not leave the sandbox reporting itself as restoring
// forever, and must say why it failed.
func TestImportReportsAFailedRestore(t *testing.T) {
	resetRestore(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()

	root := t.TempDir()
	if _, err := Import(context.Background(), ImportOptions{
		URL:        server.URL + "/archive.tar",
		root:       root,
		MarkerPath: filepath.Join(root, "marker.json"),
	}); err == nil {
		t.Fatal("expected the import to fail")
	}

	status := Status()
	if status.State != StateActive {
		t.Errorf("a restore that wrote nothing gives the sandbox back, got %q", status.State)
	}
	if status.Restore == nil || status.Restore.State != RestoreFailed {
		t.Fatalf("a failed restore is reported as such, got %+v", status.Restore)
	}
	if status.Restore.Error == "" {
		t.Error("a failed restore says why")
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was never met")
}
