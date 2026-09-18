package archive

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExportStreamsALargeFile checks that the size of the archive is not the
// size of the memory it takes to produce it. The sandbox's whole filesystem
// lives in guest RAM, so an export that staged the archive - or a single large
// member of it - in memory would kill the very sandbox it is archiving, and the
// files that make it happen are exactly the uninteresting ones: a sparse disk
// image, a database file, a model.
func TestExportStreamsALargeFile(t *testing.T) {
	const size = 512 << 20

	root, lower := fakeSandbox(t)
	big := filepath.Join(root, "blaxel/large.img")
	if err := os.MkdirAll(filepath.Dir(big), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	// Sparse, so the test does not need half a gigabyte of disk: the archive
	// carries the holes as zeros, which is the size the upload announces.
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	var received int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	options := exportOptions(t, root, lower)
	options.URL = server.URL

	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)

	result, err := Export(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if received != result.Size {
		t.Errorf("uploaded %d bytes for an announced size of %d", received, result.Size)
	}
	if result.Size < size {
		t.Errorf("expected an archive of at least %d bytes, got %d", size, result.Size)
	}
	// Generous on purpose: the point is the order of magnitude, a copy buffer
	// rather than a copy of the file.
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/8 {
		t.Errorf("exporting a %d byte file allocated %d bytes, which is not streaming it", size, allocated)
	}
}
