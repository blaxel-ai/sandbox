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

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(path), map[string]interface{}{"content": content})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func uniqueTestDir(prefix string) string {
	return fmt.Sprintf("/tmp/%s-%d", prefix, time.Now().UnixNano())
}

func TestFileWithContentHasBaseName(t *testing.T) {
	dir := uniqueTestDir("fs-name")
	path := dir + "/hello.txt"
	writeTestFile(t, path, "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	var file filesystem.FileWithContent
	resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &file)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "hello.txt", file.Name)
}

func encodeRoute(prefix, path string) string {
	return prefix + "%2F" + strings.TrimPrefix(path, "/")
}

func TestSearchesReturnEmptyMatchesArray(t *testing.T) {
	dir := uniqueTestDir("fs-empty-search")
	writeTestFile(t, dir+"/file.txt", "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	for _, target := range []string{
		encodeRoute("/filesystem-content-search", dir) + "?query=nothing-matches-this",
		encodeRoute("/filesystem-find", dir) + "?patterns=*.nothing",
		encodeRoute("/filesystem-search", dir) + "?query=nothing-matches-this",
	} {
		resp, err := common.MakeRequest(http.MethodGet, target, nil)
		require.NoError(t, err)
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, target)
		require.Equal(t, "[]", string(body["matches"]), target)
	}
}
