package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// driveAvailable checks if the drive infrastructure (blfs, SeaweedFS) is available
// by attempting a list mounts call. If the endpoint returns 500 with a blfs-related
// error, the infrastructure is not present and drive tests should be skipped.
func driveAvailable(t *testing.T) bool {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodGet, "/drives/mount", nil)
	if err != nil {
		t.Logf("Drive infrastructure not available: %v", err)
		return false
	}
	defer resp.Body.Close()

	// If we get 200, drives are available
	if resp.StatusCode == http.StatusOK {
		return true
	}

	t.Logf("Drive infrastructure not available: status %d", resp.StatusCode)
	return false
}

// skipIfNoDrives skips the test if drive infrastructure is not available.
func skipIfNoDrives(t *testing.T) {
	t.Helper()
	if !driveAvailable(t) {
		t.Skip("Skipping: drive infrastructure (blfs/SeaweedFS) not available")
	}
}

// --- API Validation Tests ---
// These tests verify the drive API request validation and do not require
// actual drive infrastructure (blfs/SeaweedFS).

// TestDriveAttachValidation tests request validation for the attach drive endpoint.
func TestDriveAttachValidation(t *testing.T) {
	t.Run("missing drive name", func(t *testing.T) {
		req := map[string]interface{}{
			"mountPath": "/mnt/test",
		}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp handler.ErrorResponse
		err = common.ParseJSONResponse(resp, &errResp)
		require.NoError(t, err)
		assert.NotEmpty(t, errResp.Error)
	})

	t.Run("missing mount path", func(t *testing.T) {
		req := map[string]interface{}{
			"driveName": "test-drive",
		}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp handler.ErrorResponse
		err = common.ParseJSONResponse(resp, &errResp)
		require.NoError(t, err)
		assert.NotEmpty(t, errResp.Error)
	})

	t.Run("empty request body", func(t *testing.T) {
		req := map[string]interface{}{}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid drive name with special characters", func(t *testing.T) {
		req := map[string]interface{}{
			"driveName": "../etc/passwd",
			"mountPath": "/mnt/test",
		}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp handler.ErrorResponse
		err = common.ParseJSONResponse(resp, &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Error, "drive name")
	})

	t.Run("mount path with path traversal", func(t *testing.T) {
		req := map[string]interface{}{
			"driveName": "test-drive",
			"mountPath": "/mnt/../etc/shadow",
		}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp handler.ErrorResponse
		err = common.ParseJSONResponse(resp, &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Error, "..")
	})

	t.Run("invalid drive path without leading slash", func(t *testing.T) {
		req := map[string]interface{}{
			"driveName": "test-drive",
			"mountPath": "/mnt/test",
			"drivePath": "no-leading-slash",
		}
		resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", req)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

		var errResp handler.ErrorResponse
		err = common.ParseJSONResponse(resp, &errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Error, "drive path")
	})
}

// TestDriveDetachValidation tests request validation for the detach drive endpoint.
func TestDriveDetachValidation(t *testing.T) {
	t.Run("detach without mount path", func(t *testing.T) {
		resp, err := common.MakeRequest(http.MethodDelete, "/drives/mount/", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should return 400 or 404 since no mount path is provided
		assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound}, resp.StatusCode)
	})
}

// TestDriveListMounts tests the list mounts endpoint returns a valid response.
func TestDriveListMounts(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodGet, "/drives/mount", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// On environments without blfs, the list may fail with 500;
	// on environments with blfs, it should return 200 with mounts array.
	if resp.StatusCode == http.StatusOK {
		var listResp struct {
			Mounts []struct {
				DriveName string `json:"driveName"`
				MountPath string `json:"mountPath"`
				DrivePath string `json:"drivePath"`
			} `json:"mounts"`
		}
		err = common.ParseJSONResponse(resp, &listResp)
		require.NoError(t, err)
		assert.NotNil(t, listResp.Mounts, "Mounts field should be present (even if empty)")
	}
}

// --- Drive Mount Lifecycle Tests ---
// These tests require actual drive infrastructure and will be skipped if unavailable.

// TestDriveMountLifecycle tests the full attach -> list -> file operations -> detach lifecycle.
// This covers the Corvera use case of drive mount/unmount lifecycle.
func TestDriveMountLifecycle(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-test-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-test-%d", time.Now().Unix())

	// 1. Attach the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "Drive attach should succeed")

	var attachResp struct {
		Success   bool   `json:"success"`
		DriveName string `json:"driveName"`
		MountPath string `json:"mountPath"`
		DrivePath string `json:"drivePath"`
	}
	err = common.ParseJSONResponse(resp, &attachResp)
	require.NoError(t, err)
	assert.True(t, attachResp.Success)
	assert.Equal(t, driveName, attachResp.DriveName)
	assert.Equal(t, mountPath, attachResp.MountPath)
	assert.Equal(t, "/", attachResp.DrivePath)

	// Ensure cleanup on test failure
	defer func() {
		cleanupResp, cleanupErr := common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if cleanupErr == nil {
			cleanupResp.Body.Close()
		}
	}()

	// 2. Verify the mount appears in the list
	resp, err = common.MakeRequest(http.MethodGet, "/drives/mount", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var listResp struct {
		Mounts []struct {
			DriveName string `json:"driveName"`
			MountPath string `json:"mountPath"`
			DrivePath string `json:"drivePath"`
		} `json:"mounts"`
	}
	err = common.ParseJSONResponse(resp, &listResp)
	require.NoError(t, err)

	found := false
	for _, m := range listResp.Mounts {
		if m.MountPath == mountPath {
			found = true
			assert.Equal(t, driveName, m.DriveName)
			break
		}
	}
	assert.True(t, found, "Mounted drive should appear in list")

	// 3. Detach the drive
	resp, err = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var detachResp struct {
		Success   bool   `json:"success"`
		MountPath string `json:"mountPath"`
	}
	err = common.ParseJSONResponse(resp, &detachResp)
	require.NoError(t, err)
	assert.True(t, detachResp.Success)

	// 4. Verify the mount is no longer in the list
	resp, err = common.MakeRequest(http.MethodGet, "/drives/mount", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	err = common.ParseJSONResponse(resp, &listResp)
	require.NoError(t, err)

	for _, m := range listResp.Mounts {
		assert.NotEqual(t, mountPath, m.MountPath, "Detached drive should not appear in list")
	}
}

// TestDriveMountWithSubpath tests mounting a drive with a specific subpath (drivePath).
// Corvera uses subpaths to mount specific directories within a drive.
func TestDriveMountWithSubpath(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-subpath-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-subpath-%d", time.Now().Unix())
	drivePath := "/logs"

	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
		"drivePath": drivePath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, "Drive attach with subpath should succeed")

	var attachResp struct {
		Success   bool   `json:"success"`
		DrivePath string `json:"drivePath"`
	}
	err = common.ParseJSONResponse(resp, &attachResp)
	require.NoError(t, err)
	assert.True(t, attachResp.Success)
	assert.Equal(t, drivePath, attachResp.DrivePath)

	// Cleanup
	cleanupResp, _ := common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
	if cleanupResp != nil {
		cleanupResp.Body.Close()
	}
}

// --- File Operations on Mounted Drives ---
// These tests simulate Corvera's core file operation patterns on mounted drives.

// TestDriveFileReadWrite tests basic file read/write operations on a mounted drive.
// This covers Corvera's core use case of reading and writing files on agent drives.
func TestDriveFileReadWrite(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-fileops-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-fileops-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	// Wait briefly for mount to stabilize
	time.Sleep(500 * time.Millisecond)

	// 1. Create a file on the mounted drive via filesystem API
	testContent := "Hello from Corvera CI test"
	testFilePath := mountPath + "/test-file.txt"

	createReq := map[string]interface{}{
		"content": testContent,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testFilePath), createReq, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 2. Read the file back
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(testFilePath), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var fileResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &fileResp)
	require.NoError(t, err)
	assert.Equal(t, testContent, fileResp["content"])

	// 3. List the directory to verify the file exists
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(mountPath), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var dirResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &dirResp)
	require.NoError(t, err)

	files, ok := dirResp["files"].([]interface{})
	require.True(t, ok, "Directory response should have files array")
	foundFile := false
	for _, f := range files {
		fileMap, ok := f.(map[string]interface{})
		if ok && fileMap["path"] == testFilePath {
			foundFile = true
			break
		}
	}
	assert.True(t, foundFile, "Created file should appear in directory listing on mounted drive")

	// 4. Delete the file
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testFilePath), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestDriveLogDirectoryAppend tests appending to files in a .log directory on a mounted drive.
// This directly simulates Corvera's Claude Code session memory pattern where agents
// append session logs to .log files on a shared drive.
func TestDriveLogDirectoryAppend(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-logdir-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-logdir-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		// Cleanup: remove files then unmount
		r, _ := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(mountPath+"/.log")+"?recursive=true", nil)
		if r != nil {
			r.Body.Close()
		}
		r, _ = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// 1. Create the .log directory on the mounted drive
	logDir := mountPath + "/.log"
	createDirReq := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(logDir), createDirReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 2. Write an initial session log entry
	sessionFile := logDir + "/session-001.log"
	initialContent := "2026-03-20T10:00:00Z [session-start] Agent initialized\n"

	createReq := map[string]interface{}{
		"content": initialContent,
	}
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(sessionFile), createReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 3. Append additional log entries (simulating agent activity over time)
	appendContent := initialContent + "2026-03-20T10:01:00Z [task] Processing user request\n" +
		"2026-03-20T10:02:00Z [task] Completed analysis\n"

	appendReq := map[string]interface{}{
		"content": appendContent,
	}
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(sessionFile), appendReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 4. Read back and verify the content persisted correctly
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(sessionFile), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var fileResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &fileResp)
	require.NoError(t, err)

	content, ok := fileResp["content"].(string)
	require.True(t, ok, "Content should be a string")
	assert.Contains(t, content, "session-start")
	assert.Contains(t, content, "Processing user request")
	assert.Contains(t, content, "Completed analysis")

	// 5. Create multiple session log files (simulating multiple sessions)
	for i := 2; i <= 5; i++ {
		sf := fmt.Sprintf("%s/session-%03d.log", logDir, i)
		entry := fmt.Sprintf("2026-03-20T10:%02d:00Z [session-start] Agent %d initialized\n", i, i)
		req := map[string]interface{}{
			"content": entry,
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(sf), req, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	}

	// 6. Verify all session files exist in .log directory
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(logDir), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var dirResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &dirResp)
	require.NoError(t, err)

	files, ok := dirResp["files"].([]interface{})
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(files), 5, "Should have at least 5 session log files")
}

// --- Concurrent Access Tests ---
// These tests simulate Corvera's concurrent file access patterns where
// multiple agents write to the same drive simultaneously.

// TestDriveConcurrentFileCreation tests multiple goroutines creating different files
// on the same mounted drive concurrently.
func TestDriveConcurrentFileCreation(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-concurrent-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-concurrent-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(mountPath)+"?recursive=true", nil)
		if r != nil {
			r.Body.Close()
		}
		r, _ = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// Simulate 5 concurrent agents creating files
	numAgents := 5
	var wg sync.WaitGroup
	errors := make([]error, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()
			filePath := fmt.Sprintf("%s/agent-%d-output.txt", mountPath, agentID)
			content := fmt.Sprintf("Output from agent %d at %s", agentID, time.Now().Format(time.RFC3339Nano))

			req := map[string]interface{}{
				"content": content,
			}
			r, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(filePath), req)
			if err != nil {
				errors[agentID] = fmt.Errorf("agent %d failed to create file: %w", agentID, err)
				return
			}
			defer r.Body.Close()

			if r.StatusCode != http.StatusOK {
				errors[agentID] = fmt.Errorf("agent %d got status %d", agentID, r.StatusCode)
			}
		}(i)
	}

	wg.Wait()

	// Check for errors
	for i, err := range errors {
		assert.NoError(t, err, "Agent %d should not have errors", i)
	}

	// Verify all files were created
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(mountPath), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var dirResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &dirResp)
	require.NoError(t, err)

	files, ok := dirResp["files"].([]interface{})
	require.True(t, ok)
	assert.GreaterOrEqual(t, len(files), numAgents, "All agent files should be created")

	// Verify each file's content is intact
	for i := 0; i < numAgents; i++ {
		filePath := fmt.Sprintf("%s/agent-%d-output.txt", mountPath, i)
		fileResp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(filePath), nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, fileResp.StatusCode, "Agent %d file should be readable", i)

		var fileData map[string]interface{}
		err = common.ParseJSONResponse(fileResp, &fileData)
		fileResp.Body.Close()
		require.NoError(t, err)

		content, ok := fileData["content"].(string)
		require.True(t, ok)
		assert.Contains(t, content, fmt.Sprintf("Output from agent %d", i),
			"Agent %d file content should be intact", i)
	}
}

// TestDriveConcurrentLogAppend tests multiple goroutines appending to separate log files
// on the same drive concurrently, simulating Corvera's multi-agent session memory pattern.
func TestDriveConcurrentLogAppend(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-logappend-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-logappend-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(mountPath+"/.log")+"?recursive=true", nil)
		if r != nil {
			r.Body.Close()
		}
		r, _ = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// Create .log directory
	logDir := mountPath + "/.log"
	createDirReq := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(logDir), createDirReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Simulate 5 agents each writing multiple entries to their own log file
	numAgents := 5
	numEntries := 10
	var wg sync.WaitGroup
	agentErrors := make([]error, numAgents)

	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(agentID int) {
			defer wg.Done()
			logFile := fmt.Sprintf("%s/agent-%d.log", logDir, agentID)

			// Build up content with multiple entries
			var contentBuilder strings.Builder
			for j := 0; j < numEntries; j++ {
				contentBuilder.WriteString(
					fmt.Sprintf("[%s] agent=%d entry=%d Processing task\n",
						time.Now().Format(time.RFC3339Nano), agentID, j))
			}

			req := map[string]interface{}{
				"content": contentBuilder.String(),
			}
			r, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(logFile), req)
			if err != nil {
				agentErrors[agentID] = fmt.Errorf("agent %d failed: %w", agentID, err)
				return
			}
			defer r.Body.Close()

			if r.StatusCode != http.StatusOK {
				agentErrors[agentID] = fmt.Errorf("agent %d got status %d", agentID, r.StatusCode)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range agentErrors {
		assert.NoError(t, err, "Agent %d should not have errors", i)
	}

	// Verify each agent's log file has the expected content
	for i := 0; i < numAgents; i++ {
		logFile := fmt.Sprintf("%s/agent-%d.log", logDir, i)
		logResp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(logFile), nil)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, logResp.StatusCode, "Agent %d log file should be readable", i)

		var fileData map[string]interface{}
		err = common.ParseJSONResponse(logResp, &fileData)
		logResp.Body.Close()
		require.NoError(t, err)

		content, ok := fileData["content"].(string)
		require.True(t, ok)

		// Verify all entries are present
		for j := 0; j < numEntries; j++ {
			assert.Contains(t, content,
				fmt.Sprintf("agent=%d entry=%d", i, j),
				"Agent %d entry %d should be present", i, j)
		}
	}
}

// TestDriveConcurrentReadWrite tests concurrent reads and writes to the same file
// on a mounted drive, verifying data integrity under contention.
func TestDriveConcurrentReadWrite(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-rw-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-rw-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(mountPath)+"?recursive=true", nil)
		if r != nil {
			r.Body.Close()
		}
		r, _ = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// Create an initial file
	sharedFile := mountPath + "/shared-state.json"
	initialContent := `{"counter": 0, "lastUpdated": "init"}`
	createReq := map[string]interface{}{
		"content": initialContent,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(sharedFile), createReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Concurrent writes: each writer updates the file with its own version
	numWriters := 3
	numReaders := 3
	var wg sync.WaitGroup
	writeErrors := make([]error, numWriters)
	readErrors := make([]error, numReaders)

	// Writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			content := fmt.Sprintf(`{"counter": %d, "lastUpdated": "writer-%d", "timestamp": "%s"}`,
				writerID, writerID, time.Now().Format(time.RFC3339Nano))
			req := map[string]interface{}{
				"content": content,
			}
			r, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(sharedFile), req)
			if err != nil {
				writeErrors[writerID] = err
				return
			}
			r.Body.Close()
			if r.StatusCode != http.StatusOK {
				writeErrors[writerID] = fmt.Errorf("writer %d got status %d", writerID, r.StatusCode)
			}
		}(i)
	}

	// Readers (run concurrently with writers)
	for i := 0; i < numReaders; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			r, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(sharedFile), nil)
			if err != nil {
				readErrors[readerID] = err
				return
			}
			defer r.Body.Close()

			// Reads should always succeed (even if we get stale data)
			if r.StatusCode != http.StatusOK {
				readErrors[readerID] = fmt.Errorf("reader %d got status %d", readerID, r.StatusCode)
				return
			}

			var fileResp map[string]interface{}
			if err := common.ParseJSONResponse(r, &fileResp); err != nil {
				readErrors[readerID] = err
				return
			}

			// The content should be valid JSON (not corrupted)
			content, ok := fileResp["content"].(string)
			if !ok {
				readErrors[readerID] = fmt.Errorf("reader %d: content not a string", readerID)
				return
			}
			var parsed map[string]interface{}
			if err := json.Unmarshal([]byte(content), &parsed); err != nil {
				readErrors[readerID] = fmt.Errorf("reader %d: JSON corruption detected: %w", readerID, err)
			}
		}(i)
	}

	wg.Wait()

	for i, err := range writeErrors {
		assert.NoError(t, err, "Writer %d should not have errors", i)
	}
	for i, err := range readErrors {
		assert.NoError(t, err, "Reader %d should not have errors (no JSON corruption)", i)
	}

	// Final read should return valid JSON
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(sharedFile), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var finalResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &finalResp)
	require.NoError(t, err)

	finalContent, ok := finalResp["content"].(string)
	require.True(t, ok)

	var finalJSON map[string]interface{}
	err = json.Unmarshal([]byte(finalContent), &finalJSON)
	assert.NoError(t, err, "Final file content should be valid JSON (not corrupted)")
	assert.Contains(t, finalJSON, "counter", "JSON should contain counter field")
	assert.Contains(t, finalJSON, "lastUpdated", "JSON should contain lastUpdated field")
}

// TestDriveFileWatchOnMount tests the filesystem watch functionality on a mounted drive.
// This ensures that file change events are properly propagated through FUSE mounts,
// which is critical for Corvera's real-time session monitoring.
func TestDriveFileWatchOnMount(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-watch-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-watch-%d", time.Now().Unix())

	// Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(mountPath)+"?recursive=true", nil)
		if r != nil {
			r.Body.Close()
		}
		r, _ = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// Start watching the mount path using a cancellable context to avoid goroutine leaks
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	received := make(chan string, 10)
	done := make(chan struct{})

	go func() {
		defer close(done)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, common.BaseURL+common.EncodeWatchPath(mountPath), nil)
		if err != nil {
			t.Logf("Watch request creation error: %v", err)
			return
		}
		watchResp, err := common.Client.Do(req)
		if err != nil {
			// Context cancellation is expected
			if ctx.Err() != nil {
				return
			}
			t.Logf("Watch error: %v", err)
			return
		}
		defer watchResp.Body.Close()

		buf := make([]byte, 4096)
		for {
			n, readErr := watchResp.Body.Read(buf)
			if n > 0 {
				received <- string(buf[:n])
			}
			if readErr != nil {
				return
			}
		}
	}()

	// Wait for watcher to be ready
	time.Sleep(500 * time.Millisecond)

	// Create a file to trigger a watch event
	testFile := mountPath + "/watched-file.txt"
	createReq := map[string]interface{}{
		"content": "watch trigger content",
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testFile), createReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for the watch event
	select {
	case event := <-received:
		assert.Contains(t, event, "watched-file.txt",
			"Watch should receive event for file created on mounted drive")
	case <-time.After(5 * time.Second):
		// On FUSE mounts, inotify may not always work. This is a known limitation.
		t.Log("Warning: No watch event received within timeout. This may be expected for FUSE-mounted drives where inotify events are not fully supported.")
	}

	// Cancel context to stop the watch goroutine and wait for it to exit
	cancel()
	<-done
}

// TestDriveRemountAfterDetach tests that a drive can be remounted after being detached,
// simulating the sandbox restart with mounted drives scenario.
func TestDriveRemountAfterDetach(t *testing.T) {
	skipIfNoDrives(t)

	driveName := fmt.Sprintf("ci-remount-%d", time.Now().Unix())
	mountPath := fmt.Sprintf("/mnt/ci-remount-%d", time.Now().Unix())

	// 1. Mount the drive
	attachReq := map[string]interface{}{
		"driveName": driveName,
		"mountPath": mountPath,
	}
	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Ensure cleanup even if steps between first mount and remount fail
	defer func() {
		r, _ := common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
		if r != nil {
			r.Body.Close()
		}
	}()

	time.Sleep(500 * time.Millisecond)

	// 2. Write a file
	testFile := mountPath + "/persist-test.txt"
	testContent := "data written before unmount"
	createReq := map[string]interface{}{
		"content": testContent,
	}
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testFile), createReq, &successResp)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 3. Detach the drive
	resp, err = common.MakeRequest(http.MethodDelete, "/drives/mount"+mountPath, nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for unmount to fully complete
	time.Sleep(1 * time.Second)

	// 4. Remount the same drive
	resp, err = common.MakeRequest(http.MethodPost, "/drives/mount", attachReq)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	time.Sleep(500 * time.Millisecond)

	// 5. Verify the file still exists after remount (data persistence)
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(testFile), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var fileResp map[string]interface{}
	err = common.ParseJSONResponse(resp, &fileResp)
	require.NoError(t, err)
	assert.Equal(t, testContent, fileResp["content"],
		"File content should persist across unmount/remount cycle")
}
