package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/lib/apierrors"
)

func TestFilesystemConflictResponse(t *testing.T) {
	h := &FileSystemHandler{BaseHandler: NewBaseHandler()}
	for _, test := range []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"wrapped-exists", fmt.Errorf("copying: %w", &os.PathError{Op: "open", Path: "destination", Err: os.ErrExist}), http.StatusConflict, "FILE_ALREADY_EXISTS"},
		{"permission", os.ErrPermission, http.StatusUnprocessableEntity, "FILESYSTEM_WRITE_FAILED"},
		{"other", errors.New("other error"), http.StatusUnprocessableEntity, "FILESYSTEM_WRITE_FAILED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			h.sendFilesystemError(context, apierrors.FilesystemWriteFailed, test.err)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var result ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != test.code || result.ErrorCode != test.code || result.Error != test.err.Error() || result.Message != test.err.Error() {
				t.Fatalf("response = %+v", result)
			}
			// A copy is not a transaction: either way, part of it may be done.
			if result.DispatchState != "dispatched_unknown" || result.SafeToRetryRequest {
				t.Fatalf("response = %+v: want dispatched_unknown, not safe to retry", result)
			}
		})
	}
}
