package tests

import (
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/beamlit/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileSystemOperations tests file system operations
func TestFileSystemOperations(t *testing.T) {
	// Test content
	testContent := "test content"
	testPath := "/test-file-" + fmt.Sprintf("%d", time.Now().Unix()) + ".txt"

	// Create a file
	createFileRequest := map[string]interface{}{
		"content": testContent,
	}

	resp, err := common.MakeRequest(http.MethodPut, "/filesystem"+testPath, createFileRequest)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Get the file
	resp, err = common.MakeRequest(http.MethodGet, "/filesystem"+testPath, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	content, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, testContent, string(content))

	// Delete the file
	resp, err = common.MakeRequest(http.MethodDelete, "/filesystem"+testPath, nil)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify file is deleted
	resp, err = common.MakeRequest(http.MethodGet, "/filesystem"+testPath, nil)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestFileSystemTree tests the tree view functionality
func TestFileSystemTree(t *testing.T) {
	// Create a directory path with timestamp to avoid conflicts
	testDir := "/test-dir-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create the directory
	resp, err := common.MakeRequest(http.MethodPut, "/filesystem"+testDir, map[string]interface{}{
		"isDirectory": true,
	})
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Create a file inside the directory
	testFilePath := testDir + "/test.txt"
	resp, err = common.MakeRequest(http.MethodPut, "/filesystem"+testFilePath, map[string]interface{}{
		"content": "test content",
	})
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Get the tree view
	resp, err = common.MakeRequest(http.MethodGet, "/filesystem/tree"+testDir, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Clean up - delete the directory (should recursively delete contents)
	resp, err = common.MakeRequest(http.MethodDelete, "/filesystem"+testDir, nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
