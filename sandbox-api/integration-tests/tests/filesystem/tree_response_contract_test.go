package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/require"
)

func TestFilesystemTreeResponseContract(t *testing.T) {
	root := fmt.Sprintf("/tmp/tree-contract-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if response, err := common.MakeRequest(http.MethodDelete, common.EncodeTreePath(root)+"?recursive=true", nil); err == nil {
			response.Body.Close()
		}
	})
	const rootContent = "root content\n"
	const nestedContent = "nested content\n"
	response, err := common.MakeRequest(http.MethodPut, common.EncodeTreePath(root), map[string]any{"files": map[string]string{"root.txt": rootContent, "nested/child.txt": nestedContent}})
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	require.Contains(t, response.Header.Get("Content-Type"), "application/json")
	var written filesystem.Directory
	require.NoError(t, json.NewDecoder(response.Body).Decode(&written))
	require.Equal(t, root, written.Path)
	require.Len(t, written.Files, 1)
	require.Equal(t, root+"/root.txt", written.Files[0].Path)
	require.Len(t, written.Subdirectories, 1)
	require.Equal(t, root+"/nested", written.Subdirectories[0].Path)

	// Both directory endpoints must preserve the Directory response shape.
	for _, path := range []string{common.EncodeTreePath(root), common.EncodeFilesystemPath(root)} {
		response, err := common.MakeRequest(http.MethodGet, path, nil)
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var listed filesystem.Directory
		require.NoError(t, json.NewDecoder(response.Body).Decode(&listed))
		require.Equal(t, written, listed)
	}

	response, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(root+"/root.txt"), nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var file filesystem.FileWithContent
	require.NoError(t, json.NewDecoder(response.Body).Decode(&file))
	require.Equal(t, root+"/root.txt", file.Path)
	require.Equal(t, rootContent, string(file.Content))

	request, err := http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(root+"/nested/child.txt"), nil)
	require.NoError(t, err)
	request.Header.Set("Accept", "application/octet-stream")
	response, err = common.Client.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.Equal(t, nestedContent, string(body))
}
