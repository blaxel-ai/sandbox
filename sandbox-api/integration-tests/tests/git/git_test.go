package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	gitpkg "github.com/blaxel-ai/sandbox-api/src/handler/git"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGitClone tests cloning a git repository
func TestGitClone(t *testing.T) {
	// Create a unique test path
	testPath := fmt.Sprintf("test-repo-%d", time.Now().Unix())

	// Clone request
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}

	var cloneResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/git/clone", cloneRequest, &cloneResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, cloneResp.Message, "success")

	// Verify the repository was cloned by checking status
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, statusResp.CurrentBranch)

	// Cleanup: delete the cloned repository
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitCloneWithBranch tests cloning a specific branch
func TestGitCloneWithBranch(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-branch-%d", time.Now().Unix())

	cloneRequest := map[string]interface{}{
		"url":    "https://github.com/blaxel-ai/sdk-typescript",
		"path":   testPath,
		"branch": "main",
	}

	var cloneResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/git/clone", cloneRequest, &cloneResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify we're on the correct branch
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "main", statusResp.CurrentBranch)

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitStatus tests getting repository status
func TestGitStatus(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-status-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Get status
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, statusResp.CurrentBranch)
	assert.GreaterOrEqual(t, statusResp.Ahead, 0)
	assert.GreaterOrEqual(t, statusResp.Behind, 0)

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitBranches tests listing branches
func TestGitBranches(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-branches-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// List branches
	var branchesResp gitpkg.BranchInfo
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "branches"), nil, &branchesResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, branchesResp.Branches)

	// Should have at least the main branch
	foundMainOrMaster := false
	for _, branch := range branchesResp.Branches {
		if branch == "main" || branch == "master" {
			foundMainOrMaster = true
			break
		}
	}
	assert.True(t, foundMainOrMaster, "Should have main or master branch")

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitCreateAndDeleteBranch tests creating and deleting a branch
func TestGitCreateAndDeleteBranch(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-create-branch-%d", time.Now().Unix())
	branchName := fmt.Sprintf("test-branch-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Create a new branch
	createBranchRequest := map[string]interface{}{
		"name": branchName,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPost, common.EncodeGitPath(testPath, "branches"), createBranchRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Verify branch was created
	var branchesResp gitpkg.BranchInfo
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "branches"), nil, &branchesResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	foundBranch := false
	for _, branch := range branchesResp.Branches {
		if branch == branchName {
			foundBranch = true
			break
		}
	}
	assert.True(t, foundBranch, "New branch should exist in branch list")

	// Delete the branch
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeGitBranchPath(testPath, branchName, "branch"), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify branch was deleted
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "branches"), nil, &branchesResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	foundBranch = false
	for _, branch := range branchesResp.Branches {
		if branch == branchName {
			foundBranch = true
			break
		}
	}
	assert.False(t, foundBranch, "Deleted branch should not exist in branch list")

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitCheckoutBranch tests switching branches
func TestGitCheckoutBranch(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-checkout-%d", time.Now().Unix())
	branchName := fmt.Sprintf("test-checkout-branch-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Create a new branch
	createBranchRequest := map[string]interface{}{
		"name": branchName,
	}
	resp, err = common.MakeRequest(http.MethodPost, common.EncodeGitPath(testPath, "branches"), createBranchRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Checkout the new branch
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeGitBranchPath(testPath, branchName, "branch"), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Verify we're on the new branch
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, branchName, statusResp.CurrentBranch)

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitAddAndCommit tests staging files and creating a commit
func TestGitAddAndCommit(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-commit-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Create a test file in the repository
	testFileName := "test-file.txt"
	testContent := "Test content for git integration test"
	createFileRequest := map[string]interface{}{
		"content": testContent,
	}

	resp, err = common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(testPath+"/"+testFileName), createFileRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Stage the file
	addRequest := map[string]interface{}{
		"files": []string{testFileName},
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPost, common.EncodeGitPath(testPath, "add"), addRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Create a commit
	commitRequest := map[string]interface{}{
		"message": "Test commit from integration test",
		"author":  "Test User",
		"email":   "test@example.com",
	}

	type CommitResponse struct {
		CommitHash string `json:"commitHash"`
		Message    string `json:"message"`
	}

	var commitResp CommitResponse
	resp, err = common.MakeRequestAndParse(http.MethodPost, common.EncodeGitPath(testPath, "commit"), commitRequest, &commitResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, commitResp.CommitHash)
	assert.Contains(t, commitResp.Message, "success")

	// Verify the commit by checking status (should have no modified files now)
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// The file we created and committed should not appear as modified
	for _, fileStatus := range statusResp.FileStatus {
		if fileStatus.Name == testFileName {
			// File shouldn't be in the status at all, or should be unmodified
			assert.Equal(t, "unmodified", fileStatus.Worktree, "Committed file should not be modified")
		}
	}

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitAddAll tests staging all files at once
func TestGitAddAll(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-add-all-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Create multiple test files
	for i := 1; i <= 3; i++ {
		fileName := fmt.Sprintf("test-file-%d.txt", i)
		createFileRequest := map[string]interface{}{
			"content": fmt.Sprintf("Test content %d", i),
		}
		resp, err = common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(testPath+"/"+fileName), createFileRequest)
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Stage all files using "."
	addRequest := map[string]interface{}{
		"files": []string{"."},
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPost, common.EncodeGitPath(testPath, "add"), addRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitInvalidURL tests error handling for invalid URLs
func TestGitInvalidURL(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-invalid-%d", time.Now().Unix())

	// Try to clone with invalid URL
	cloneRequest := map[string]interface{}{
		"url":  "invalid-url-not-git",
		"path": testPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestGitInvalidPath tests error handling for invalid paths
func TestGitInvalidPath(t *testing.T) {
	// Try to clone with path containing directory traversal
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": "../../../etc/passwd",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestGitNonExistentRepo tests error handling for non-existent repositories
func TestGitNonExistentRepo(t *testing.T) {
	testPath := "non-existent-repo"

	// Try to get status of non-existent repo
	resp, err := common.MakeRequest(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
}

// TestGitCloneExistingDirectory tests error handling when cloning to existing directory
func TestGitCloneExistingDirectory(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-existing-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Try to clone again to the same path
	resp, err = common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestGitDeleteCurrentBranch tests error handling when trying to delete current branch
func TestGitDeleteCurrentBranch(t *testing.T) {
	testPath := fmt.Sprintf("test-repo-delete-current-%d", time.Now().Unix())

	// Clone repository first
	cloneRequest := map[string]interface{}{
		"url":  "https://github.com/blaxel-ai/sdk-typescript",
		"path": testPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/git/clone", cloneRequest)
	require.NoError(t, err)
	resp.Body.Close()

	// Get current branch
	var statusResp gitpkg.RepositoryStatus
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeGitPath(testPath, "status"), nil, &statusResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	currentBranch := statusResp.CurrentBranch

	// Try to delete current branch
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeGitBranchPath(testPath, currentBranch, "branch"), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return error status
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// Cleanup
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testPath)+"?recursive=true", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}
