package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/require"
)

// TestFilesystemTreeReadWithContent reads a whole tree, with contents, in one request.
func TestFilesystemTreeReadWithContent(t *testing.T) {
	root := fmt.Sprintf("/tmp/tree-read-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		if response, err := common.MakeRequest(http.MethodDelete, common.EncodeTreePath(root)+"?recursive=true", nil); err == nil {
			response.Body.Close()
		}
	})
	files := map[string]string{"a.json": `{"a":1}`, "nested/b.json": `{"b":2}`, "nested/notes.md": "skip", "node_modules/c.json": "skip"}
	response, err := common.MakeRequest(http.MethodPut, common.EncodeTreePath(root), map[string]any{"files": files})
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	response, err = common.MakeRequest(http.MethodGet, common.EncodeTreePath(root)+"?recursive=true&content=true&patterns=*.json&excludeDirs=node_modules", nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var tree filesystem.Directory
	require.NoError(t, json.NewDecoder(response.Body).Decode(&tree))
	got := map[string]string{}
	for _, file := range tree.Files {
		require.NotNil(t, file.Content, file.Path)
		got[strings.TrimPrefix(file.Path, root+"/")] = *file.Content
	}
	require.Equal(t, map[string]string{"a.json": `{"a":1}`, "nested/b.json": `{"b":2}`}, got)

	response, err = common.MakeRequest(http.MethodGet, common.EncodeTreePath(root)+"?recursive=true&maxFiles=3", nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusUnprocessableEntity, response.StatusCode)
}
