package tests

import (
	"fmt"
	"net/http"
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
