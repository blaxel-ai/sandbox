package handler

import (
	stdjson "encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/lib/apierrors"
)

func TestSendErrorMarksAFullDisk(t *testing.T) {
	dir := t.TempDir()
	// A server fault: DISK_FULL keeps the status of the entry it replaces.
	internal := apierrors.FilesystemWriteFailed
	internal.Status = http.StatusInternalServerError
	for _, tc := range []struct {
		name  string
		entry apierrors.Entry
		err   error
		want  apierrors.Entry
	}{
		{"write", apierrors.FilesystemWriteFailed, fmt.Errorf("failed to write file: %w", &fs.PathError{Op: "write", Path: dir + "/big.bin", Err: syscall.ENOSPC}), apierrors.DiskFull},
		{"rename", apierrors.FilesystemWriteFailed, &os.LinkError{Op: "rename", Old: dir + "/a", New: dir + "/b", Err: syscall.ENOSPC}, apierrors.DiskFull},
		{"server fault", internal, &fs.PathError{Op: "write", Path: dir + "/big.bin", Err: syscall.ENOSPC}, apierrors.DiskFull},
		// inotify answers ENOSPC at its watch limit: not a full disk.
		{"watch limit", apierrors.FilesystemWriteFailed, fmt.Errorf("failed to watch: %w", syscall.ENOSPC), apierrors.FilesystemWriteFailed},
		{"other", apierrors.FilesystemWriteFailed, &fs.PathError{Op: "open", Path: dir + "/x", Err: syscall.EACCES}, apierrors.FilesystemWriteFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			NewBaseHandler().SendError(ctx, tc.entry, tc.err)

			if recorder.Code != tc.entry.Status {
				t.Errorf("status = %d, want the handler's own %d", recorder.Code, tc.entry.Status)
			}
			var body map[string]any
			if err := stdjson.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			message, _ := body["error"].(string)
			checkErrorFields(t, body, tc.want, tc.entry.Status, message)
			if got := recorder.Header().Get("X-Blaxel-Error-Code"); got != tc.want.Code {
				t.Fatalf("header %q, want %q", got, tc.want.Code)
			}
			if !strings.HasPrefix(message, tc.err.Error()) {
				t.Errorf("error %q does not start with the original %q", message, tc.err)
			}
			if tc.want.Code == apierrors.DiskFull.Code && (!strings.Contains(message, "disk is full") || !strings.Contains(message, " in all)") || !strings.Contains(message, "Free space")) {
				t.Errorf("error %q does not say what is full, its size and what to do", message)
			}
		})
	}
	if _, ok := diskFullPath(errors.New("no space left on device")); ok {
		t.Error("an error that is only text was taken for a full disk")
	}
}

func TestHumanBytes(t *testing.T) {
	for n, want := range map[uint64]string{999: "999 B", 1000: "1.0 kB", 8_589_934_592: "8.6 GB", 1_500_000_000_000: "1.5 TB"} {
		if got := humanBytes(n); got != want {
			t.Errorf("humanBytes(%d) = %q, want %q", n, got, want)
		}
	}
}
