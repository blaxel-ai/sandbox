package tests

import (
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/beamlit/sandbox-api/integration_tests/common"
	"github.com/beamlit/sandbox-api/src/handler"
	"github.com/beamlit/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFileSystemOperations tests file system operations
func TestFileSystemOperations(t *testing.T) {
	// Use current user for test paths
	user := os.Getenv("USER")
	if user == "" {
		user = "test-user" // fallback if USER env var is not set
	}

	// Create test files in /tmp which should be accessible
	testContent := "Hello world"
	testPath := fmt.Sprintf("/tmp/test-%d", time.Now().Unix())
	testDir := fmt.Sprintf("/tmp/test2-%d", time.Now().Unix())
	testCopyPath := fmt.Sprintf("%s/test", testDir)

	// 1. Create a file with content
	createFileRequest := map[string]interface{}{
		"content": testContent,
	}

	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+testPath, createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 2. Get file content
	var fileResponse filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testPath, nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testContent, string(fileResponse.Content))
	assert.Equal(t, testPath, fileResponse.Path)

	// 3. List directory
	var dirResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem/tmp", nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, dirResponse.Files)

	// Check if test file exists in directory
	foundTestFile := false
	for _, file := range dirResponse.Files {
		if file.Path == testPath {
			foundTestFile = true
			break
		}
	}
	assert.True(t, foundTestFile, "Test file should exist in directory listing")

	// 4. Create a directory
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}

	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+testDir, createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 5. List newly created directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testDir, nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 6. Since there's no direct copy endpoint, we'll read the file and then write it to the new location
	// Read the content of the original file
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testPath, nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Write the content to the new path
	copyRequest := map[string]interface{}{
		"content": string(fileResponse.Content),
	}

	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+testCopyPath, copyRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 7. List directory after copy
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testDir, nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check if copied file exists in directory
	foundCopiedFile := false
	for _, file := range dirResponse.Files {
		if file.Path == testCopyPath {
			foundCopiedFile = true
			break
		}
	}
	assert.True(t, foundCopiedFile, "Copied file should exist in directory listing")

	// 8. Delete original file
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+testPath, nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 9. Try to delete directory without recursive flag - should fail
	resp, err = common.MakeRequest(http.MethodDelete, "/filesystem"+testDir, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// 10. Delete directory with recursive flag
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+testDir+"?recursive=true", nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}

// TestFileSystemTree tests the tree view functionality
func TestFileSystemTree(t *testing.T) {
	// Create a directory path with timestamp to avoid conflicts
	testDir := "/test-dir-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create the directory
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+testDir, map[string]interface{}{
		"isDirectory": true,
	}, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Create a file inside the directory
	testFileName := "test.txt"
	testFilePath := testDir + "/" + testFileName
	testContent := "test content"

	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+testFilePath, map[string]interface{}{
		"content": testContent,
	}, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Get the directory listing
	var dirResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testDir, nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify directory path and contents
	assert.Equal(t, testDir, dirResponse.Path)
	require.Len(t, dirResponse.Files, 1, "Directory should contain exactly one file")
	assert.Equal(t, testFilePath, dirResponse.Files[0].Path, "File path should match")

	// Get the tree view
	var treeResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem/tree"+testDir, nil, &treeResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testDir, treeResponse.Path)

	// Clean up - delete the directory (should recursively delete contents)
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+testDir+"?recursive=true", nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}
