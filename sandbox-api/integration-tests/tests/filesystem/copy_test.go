package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/require"
)

func copyRoot(t *testing.T) string {
	t.Helper()
	root := fmt.Sprintf("/tmp/copy-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(root)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	return root
}

func copyRequest(t *testing.T, method, path string, body interface{}, status int) {
	t.Helper()
	resp, err := common.MakeRequest(method, path, body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, status, resp.StatusCode)
	if status == http.StatusConflict {
		var result handler.ErrorResponse
		require.NoError(t, common.ParseJSONResponse(resp, &result))
		require.Equal(t, "FILE_ALREADY_EXISTS", result.Code)
		require.NotEmpty(t, result.Error)
	}
}

func copyContent(t *testing.T, path, expected string) {
	t.Helper()
	var result filesystem.FileWithContent
	resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &result)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, expected, result.Content)
}

func TestCopy(t *testing.T) {
	root := copyRoot(t)
	copyRequest(t, http.MethodPut, common.EncodeFilesystemPath(root+"/source/nested/file"), map[string]interface{}{"content": "original"}, http.StatusOK)
	copyRequest(t, http.MethodPut, common.EncodeFilesystemPath(root+"/container"), map[string]interface{}{"isDirectory": true}, http.StatusOK)
	for _, test := range []struct {
		source, destination string
		status              int
	}{
		{"/source/nested/file", "/file", http.StatusOK},
		{"/source/nested/file", "/file", http.StatusConflict},
		{"/source", "/directory", http.StatusOK},
		// directory now exists, so the target is directory/source, like cp -r.
		{"/source", "/directory", http.StatusOK},
		{"/source", "/directory", http.StatusConflict},
		{"/source", "/container", http.StatusOK},
		{"/source", "/container", http.StatusConflict},
		{"/source", "/file", http.StatusConflict},
	} {
		copyRequest(t, http.MethodPost, "/filesystem-copy", map[string]interface{}{
			"source": root + test.source, "destination": root + test.destination, "noOverwrite": true,
		}, test.status)
	}
	copyContent(t, root+"/file", "original")
	copyContent(t, root+"/directory/nested/file", "original")
	copyContent(t, root+"/directory/source/nested/file", "original")
	copyContent(t, root+"/container/source/nested/file", "original")

	// Without noOverwrite, files are overwritten and directories merged.
	copyRequest(t, http.MethodPut, common.EncodeFilesystemPath(root+"/source/nested/file"), map[string]interface{}{"content": "default"}, http.StatusOK)
	for _, pair := range [][2]string{{"/source", "/container"}, {"/source/nested/file", "/file"}} {
		copyRequest(t, http.MethodPost, "/filesystem-copy", map[string]interface{}{
			"source": root + pair[0], "destination": root + pair[1],
		}, http.StatusOK)
	}
	copyContent(t, root+"/file", "default")
	copyContent(t, root+"/container/source/nested/file", "default")

	copyRequest(t, http.MethodPost, "/filesystem-copy", map[string]interface{}{
		"source": root + "/source", "destination": root + "/source/inside", "noOverwrite": true,
	}, http.StatusUnprocessableEntity)
	copyRequest(t, http.MethodPost, "/filesystem-copy", map[string]interface{}{}, http.StatusBadRequest)
}
