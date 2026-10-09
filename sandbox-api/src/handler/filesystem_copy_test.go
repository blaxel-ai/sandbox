package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
		{"permission", os.ErrPermission, http.StatusUnprocessableEntity, ""},
		{"other", errors.New("other error"), http.StatusUnprocessableEntity, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(response)
			h.sendFilesystemError(context, http.StatusUnprocessableEntity, test.err)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d", response.Code, test.status)
			}
			var result ErrorResponse
			if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if result.Code != test.code || result.Error != test.err.Error() {
				t.Fatalf("response = %+v", result)
			}
			if test.code == "" && strings.Contains(response.Body.String(), `"code"`) {
				t.Fatalf("unexpected code field: %s", response.Body.String())
			}
		})
	}
}
