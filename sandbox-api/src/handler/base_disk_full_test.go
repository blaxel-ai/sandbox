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
)

func TestSendErrorMarksAFullDisk(t *testing.T) {
	dir := t.TempDir()
	for _, tc := range []struct {
		name     string
		err      error
		wantCode string
	}{
		{"write", fmt.Errorf("failed to write file: %w", &fs.PathError{Op: "write", Path: dir + "/big.bin", Err: syscall.ENOSPC}), codeDiskFull},
		{"rename", &os.LinkError{Op: "rename", Old: dir + "/a", New: dir + "/b", Err: syscall.ENOSPC}, codeDiskFull},
		// inotify answers ENOSPC at its watch limit: not a full disk.
		{"watch limit", fmt.Errorf("failed to watch: %w", syscall.ENOSPC), ""},
		{"other", &fs.PathError{Op: "open", Path: dir + "/x", Err: syscall.EACCES}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			NewBaseHandler().SendError(ctx, http.StatusUnprocessableEntity, tc.err)

			if recorder.Code != http.StatusUnprocessableEntity {
				t.Errorf("status = %d, want the handler's own %d", recorder.Code, http.StatusUnprocessableEntity)
			}
			var body ErrorResponse
			if err := stdjson.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != tc.wantCode || recorder.Header().Get("X-Blaxel-Error-Code") != tc.wantCode {
				t.Fatalf("code %q, header %q; want %q in both", body.Code, recorder.Header().Get("X-Blaxel-Error-Code"), tc.wantCode)
			}
			if !strings.HasPrefix(body.Error, tc.err.Error()) {
				t.Errorf("error %q does not start with the original %q", body.Error, tc.err)
			}
			if tc.wantCode != "" && (!strings.Contains(body.Error, "disk is full") || !strings.Contains(body.Error, " in all)") || !strings.Contains(body.Error, "Free space")) {
				t.Errorf("error %q does not say what is full, its size and what to do", body.Error)
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
