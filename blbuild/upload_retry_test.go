package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A part upload had one attempt and a 30-minute client timeout, so a network
// blip parked a build for the whole of it — measured at 15 minutes on a build
// that had already produced every artefact and simply never uploaded them.
func TestUploadPartRetriesTransientFailures(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Fail twice, then accept: the shape of a connection that recovers.
		if atomic.AddInt32(&attempts, 1) <= 2 {
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, err := hj.Hijack()
				if err == nil {
					_ = conn.Close()
					return
				}
			}
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs.erofs")
	if err := os.WriteFile(rootfs, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &Builder{Targets: &Targets{Initrd: InitrdUpload{PartURLs: []string{server.URL}}}}
	etag, err := b.uploadPartWithRetry(context.Background(), rootfs, 0, 0, 7)
	if err != nil {
		t.Fatalf("upload gave up on a recoverable failure: %v", err)
	}
	if etag != `"abc"` {
		t.Errorf("etag = %q", etag)
	}
	if got := atomic.LoadInt32(&attempts); got != 3 {
		t.Errorf("made %d attempts, want 3", got)
	}
}

// A part that never succeeds must stop, and say how many times it tried — not
// hang until the client timeout.
func TestUploadPartGivesUpWithAClearError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	rootfs := filepath.Join(dir, "rootfs.erofs")
	if err := os.WriteFile(rootfs, []byte("payload"), 0o644); err != nil {
		t.Fatal(err)
	}

	b := &Builder{Targets: &Targets{Initrd: InitrdUpload{PartURLs: []string{server.URL}}}}
	start := time.Now()
	_, err := b.uploadPartWithRetry(context.Background(), rootfs, 0, 0, 7)
	if err == nil {
		t.Fatal("a part that never succeeds must fail")
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("gave up after %v, far too long to be useful", elapsed)
	}
	if want := fmt.Sprintf("%d attempts", partAttempts); !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not say how many attempts were made", err)
	}
}

// A fixed deadline is wrong for a part upload: two minutes on a 512MiB part
// silently demands 4.3MiB/s, so a healthy-but-slow link fails while the client's
// own 30-minute timeout would have let it finish. Observed exactly that — parts
// timing out on a sandbox where an HTTPS request completed in 0.07s.
func TestPartDeadlineScalesWithTheData(t *testing.T) {
	const MiB = 1 << 20

	// A part gets at least the base, however small it is.
	if got := partDeadline(1); got < partAttemptBase {
		t.Errorf("a tiny part got %v, less than the %v base", got, partAttemptBase)
	}

	// A full 512MiB part must tolerate a link far slower than any healthy one:
	// measured throughput is ~150MiB/s across 8 flows, so the floor is ~150x
	// slower than normal and only ever abandons a connection that is dead.
	big := partDeadline(512 * MiB)
	if implied := float64(512*MiB) / big.Seconds() / MiB; implied > 1.1 {
		t.Errorf("a 512MiB part gets %v, implying %.1f MiB/s — too demanding", big, implied)
	}

	// Bigger parts must get proportionally longer, or the bound is fixed again.
	if partDeadline(512*MiB) <= partDeadline(64*MiB) {
		t.Error("the deadline does not scale with the part size")
	}

	// And the worst case must stay inside the build's own timeout (3600s).
	if worst := time.Duration(partAttempts) * partDeadline(512*MiB); worst > 45*time.Minute {
		t.Errorf("%d attempts on a full part is %v, too close to the build timeout",
			partAttempts, worst)
	}
}
