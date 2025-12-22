package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
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
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 2. Get file content
	var fileResponse filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testContent, string(fileResponse.Content))
	assert.Equal(t, testPath, fileResponse.Path)

	// 3. List directory
	var dirResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath("/tmp"), nil, &dirResponse)
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

	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testDir), createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 5. List newly created directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testDir), nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 6. Since there's no direct copy endpoint, we'll read the file and then write it to the new location
	// Read the content of the original file
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Write the content to the new path
	copyRequest := map[string]interface{}{
		"content": string(fileResponse.Content),
	}

	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testCopyPath), copyRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 7. List directory after copy
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testDir), nil, &dirResponse)
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
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// 9. Try to delete directory without recursive flag - should fail
	resp, err = common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(testDir), nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	// 10. Delete directory with recursive flag
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testDir)+"?recursive=true", nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}

// TestFileSystemTree tests the tree view functionality
func TestFileSystemTree(t *testing.T) {
	// Create a directory path with timestamp to avoid conflicts
	testDir := "/tmp/test-dir-" + fmt.Sprintf("%d", time.Now().Unix())

	// Create the directory
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testDir), map[string]interface{}{
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

	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testFilePath), map[string]interface{}{
		"content": testContent,
	}, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Get the directory listing
	var dirResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testDir), nil, &dirResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify directory path and contents
	assert.Equal(t, testDir, dirResponse.Path)
	require.Len(t, dirResponse.Files, 1, "Directory should contain exactly one file")
	assert.Equal(t, testFilePath, dirResponse.Files[0].Path, "File path should match")

	// Get the tree view
	var treeResponse filesystem.Directory
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeTreePath(testDir), nil, &treeResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testDir, treeResponse.Path)

	// Clean up - delete the directory (should recursively delete contents)
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testDir)+"?recursive=true", nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}

// TestFileSystemWatch tests the streaming watch endpoint for file modifications
func TestFileSystemWatch(t *testing.T) {
	t.Parallel()

	dir := fmt.Sprintf("/tmp/test-watch-%d", time.Now().UnixNano())

	// Ensure cleanup happens even if test fails
	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(dir), createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	watchPath := dir
	fileName := "watched.txt"
	filePath := dir + "/" + fileName

	done := make(chan struct{})
	received := make(chan string, 1)

	// Start watcher goroutine
	go func() {
		resp, err := common.MakeRequest("GET", common.EncodeWatchPath(watchPath), nil)
		if err != nil {
			t.Errorf("Error in watcher goroutine: %v", err)
			return
		}
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			if line != "" {
				received <- line
				break
			}
		}
		close(done)
	}()

	// Wait a moment to ensure watcher is ready
	time.Sleep(300 * time.Millisecond)

	// Create a file in the watched directory
	content := []byte("hello watch!")
	createFileRequest := map[string]interface{}{
		"content": string(content),
	}
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(filePath), createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Wait for watcher to receive the event or timeout
	select {
	case event := <-received:
		assert.Contains(t, event, watchPath, "Watcher should receive the created file path")
		assert.Contains(t, event, fileName, "Watcher should receive the created file name")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for file event from watcher")
	}

	<-done
}

// TestFileSystemWatchRecursive tests recursive streaming watch endpoint for file modifications in subdirectories
func TestFileSystemWatchRecursive(t *testing.T) {
	dir := fmt.Sprintf("/tmp/test-watch-recursive-%d", time.Now().UnixNano())

	// Ensure cleanup happens even if test fails
	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	subdir := dir + "/subdir"
	fileName := "watched.txt"
	filePath := subdir + "/" + fileName

	// Create parent directory
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(dir), createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Create subdirectory
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(subdir), createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	watchPath := dir + "/**"
	done := make(chan struct{})
	received := make(chan map[string]interface{}, 5)

	// Start watcher goroutine
	go func() {
		resp, err := common.MakeRequest("GET", common.EncodeWatchPath(watchPath), nil)
		if err != nil {
			t.Errorf("Error in watcher goroutine: %v", err)
			return
		}
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				break
			}
			line = strings.TrimSpace(line)
			if line != "" {
				var event map[string]interface{}
				err := json.Unmarshal([]byte(line), &event)
				if err == nil {
					received <- event
				}
			}
		}
	}()

	// Wait a moment to ensure watcher is ready
	time.Sleep(300 * time.Millisecond)

	// Helper to wait for a specific op and name
	waitForEvent := func(op, name string) map[string]interface{} {
		timeout := time.After(50 * time.Millisecond)
		for {
			select {
			case event := <-received:
				if strings.Contains(fmt.Sprint(event["op"]), op) && event["name"] == name {
					return event
				}
			case <-timeout:
				t.Fatalf("Timeout waiting for %s event for %s", op, name)
			}
		}
	}

	// 1. Create a file in the subdirectory
	content := []byte("hello recursive watch!")
	createFileRequest := map[string]interface{}{
		"content": string(content),
	}
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(filePath), createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
	_ = waitForEvent("CREATE", fileName)

	// 2. Delete the file
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(filePath), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	waitForEvent("REMOVE", fileName)

	// 3. Create a new subdirectory
	newSubdir := dir + "/subdir2"
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(newSubdir), createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	waitForEvent("CREATE", "subdir2")

	// 3. Delete the subdirectory
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(newSubdir), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	waitForEvent("REMOVE", "subdir2")
	// Clean up
	close(done)

	// --- Ignore pattern test ---
	t.Run("ignore pattern", func(t *testing.T) {
		// Setup for ignore test
		dir := fmt.Sprintf("/tmp/test-watch-ignore-%d", time.Now().UnixNano())

		// Ensure cleanup happens even if test fails
		defer func() {
			var successResp handler.SuccessResponse
			if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil, &successResp); err == nil {
				resp.Body.Close()
			}
		}()

		subdir := dir + "/subdir"
		fileName := "watched.txt"
		filePath := subdir + "/" + fileName
		ignoredFileName := "ignored.txt"
		ignoredFilePath := subdir + "/" + ignoredFileName

		createDirRequest := map[string]interface{}{
			"isDirectory": true,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(dir), createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(subdir), createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		watchPath := dir + "/**"
		done := make(chan struct{})
		received := make(chan map[string]interface{}, 10)

		// Start watcher goroutine with ignore=ignored.txt
		go func() {
			resp, err := common.MakeRequest("GET", common.EncodeWatchPath(watchPath)+"?ignore=ignored.txt", nil)
			if err != nil {
				t.Errorf("Error in watcher goroutine: %v", err)
				return
			}
			defer resp.Body.Close()
			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				line = strings.TrimSpace(line)
				if line != "" {
					var event map[string]interface{}
					err := json.Unmarshal([]byte(line), &event)
					if err == nil {
						received <- event
					}
				}
			}
		}()

		// Wait a moment to ensure watcher is ready
		time.Sleep(100 * time.Millisecond)

		// Create a file that should NOT be ignored
		createFileRequest := map[string]interface{}{
			"content": "not ignored",
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(filePath), createFileRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Create a file that SHOULD be ignored
		createIgnoredFileRequest := map[string]interface{}{
			"content": "ignored",
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(ignoredFilePath), createIgnoredFileRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Wait for watcher to receive the event or timeout
		var gotNotIgnored, gotIgnored bool
		timeout := time.After(1 * time.Second)
		timeoutReached := false
		for !gotNotIgnored && !gotIgnored && !timeoutReached {
			select {
			case event := <-received:
				if event["name"] == fileName {
					gotNotIgnored = true
				} else if event["name"] == ignoredFileName {
					gotIgnored = true
				}
			case <-timeout:
				timeoutReached = true
			}
		}
		assert.True(t, gotNotIgnored, "Should receive event for not-ignored file")
		assert.False(t, gotIgnored, "Should NOT receive event for ignored file")
		close(done)
	})

	// --- Ignore folder pattern test ---
	t.Run("ignore folder pattern", func(t *testing.T) {
		dir := fmt.Sprintf("/tmp/test-watch-ignore-folder-%d", time.Now().UnixNano())

		// Ensure cleanup happens even if test fails
		defer func() {
			var successResp handler.SuccessResponse
			if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil, &successResp); err == nil {
				resp.Body.Close()
			}
		}()

		ignoredSubdir := dir + "/ignored-folder"
		fileName := "file.txt"
		filePath := ignoredSubdir + "/" + fileName

		createDirRequest := map[string]interface{}{
			"isDirectory": true,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(dir), createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		watchPath := dir + "/**"
		done := make(chan struct{})
		received := make(chan map[string]interface{}, 10)

		// Start watcher goroutine with ignore=ignored-folder
		go func() {
			resp, err := common.MakeRequest("GET", common.EncodeWatchPath(watchPath)+"?ignore=ignored-folder", nil)
			if err != nil {
				t.Errorf("Error in watcher goroutine: %v", err)
				return
			}
			defer resp.Body.Close()
			reader := bufio.NewReader(resp.Body)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					break
				}
				line = strings.TrimSpace(line)
				if line != "" {
					var event map[string]interface{}
					err := json.Unmarshal([]byte(line), &event)
					if err == nil {
						received <- event
					}
				}
			}
		}()

		// Wait a moment to ensure watcher is ready
		time.Sleep(100 * time.Millisecond)

		// Create the ignored subdirectory
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(ignoredSubdir), createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Create a file inside the ignored subdirectory
		createFileRequest := map[string]interface{}{
			"content": "should be ignored",
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(filePath), createFileRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Wait for watcher to receive the event or timeout
		var gotEvent bool
		timeout := time.After(1 * time.Second)
		timeoutReached := false
		for !gotEvent && !timeoutReached {
			select {
			case event := <-received:
				if event["name"] == "ignored-folder" || event["name"] == fileName {
					gotEvent = true
				}
			case <-timeout:
				timeoutReached = true
			}
		}
		assert.False(t, gotEvent, "Should NOT receive event for ignored folder or its file")

		close(done)
	})
}

// TestFileSystemWatchRecursiveNpmInstall tests that the recursive watcher captures all file events
// during npm install, comparing with inotify as a baseline. This validates the race condition fix
// where files are created in new directories before the watch can be established.
func TestFileSystemWatchRecursiveNpmInstall(t *testing.T) {
	sessionID := time.Now().UnixNano()
	projectDir := fmt.Sprintf("/tmp/npm_watch_%d", sessionID)
	inotifyLogFile := fmt.Sprintf("/tmp/inotify_npm_%d.log", sessionID)

	// Ensure cleanup happens even if test fails
	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(projectDir)+"?recursive=true", nil, &successResp); err == nil {
			resp.Body.Close()
		}
		// Clean up inotify log file
		common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": fmt.Sprintf("rm -f %s", inotifyLogFile),
		})
	}()

	// Create project directory
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(projectDir), createDirRequest, &successResp)
	require.NoError(t, err)
	resp.Body.Close()

	// Create package.json with dependencies
	packageJSON := `{
		"name": "test-project",
		"version": "1.0.0",
		"dependencies": {
			"lodash": "^4.17.21",
			"axios": "^1.6.0"
		}
	}`
	resp, err = common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(projectDir+"/package.json"), map[string]interface{}{
		"content": packageJSON,
	}, &successResp)
	require.NoError(t, err)
	resp.Body.Close()

	// ========== Test 1: Blaxel native watchFolder during npm install ==========
	t.Log("\n========== Starting Blaxel watchFolder test ==========")

	watchPath := projectDir + "/**"
	blaxelEvents := make([]map[string]interface{}, 0, 1000)
	var blaxelEventsMu sync.Mutex

	blaxelStartTime := time.Now()

	// Start watcher goroutine
	go func() {
		resp, err := common.MakeRequest("GET", common.EncodeWatchPath(watchPath), nil)
		if err != nil {
			t.Errorf("Error in watcher goroutine: %v", err)
			return
		}
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSpace(line)
			if line != "" && !strings.Contains(line, "keepalive") {
				var event map[string]interface{}
				if json.Unmarshal([]byte(line), &event) == nil {
					blaxelEventsMu.Lock()
					blaxelEvents = append(blaxelEvents, event)
					blaxelEventsMu.Unlock()
				}
			}
		}
	}()

	// Wait for watcher to establish (same pattern as existing tests)
	time.Sleep(500 * time.Millisecond)

	// Run npm install
	blaxelInstallStart := time.Now()
	resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command": fmt.Sprintf("cd %s && npm install --silent 2>&1", projectDir),
		"timeout": 180000,
	})
	require.NoError(t, err)
	resp.Body.Close()
	blaxelInstallEnd := time.Now()

	// Wait for events to propagate
	time.Sleep(3 * time.Second)

	blaxelEndTime := time.Now()

	// ========== Test 2: inotify-based watching during npm install ==========
	// Check if inotifywait is available
	var checkResp map[string]interface{}
	resp, err = common.MakeRequestAndParse(http.MethodPost, "/process", map[string]interface{}{
		"command": "which inotifywait",
	}, &checkResp)
	inotifyAvailable := false
	if err == nil {
		resp.Body.Close()
		if exitCode, ok := checkResp["exitCode"].(float64); ok && exitCode == 0 {
			inotifyAvailable = true
		}
	}

	var inotifyLines []string
	inotifyPathToEvents := make(map[string]map[string]bool)
	var inotifyInstallStart, inotifyInstallEnd, inotifyStartTime, inotifyEndTime time.Time

	if !inotifyAvailable {
		t.Log("\n========== Skipping inotify test (inotifywait not installed) ==========")
	} else {
		t.Log("\n========== Starting inotify test ==========")

		// Clean node_modules
		resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": fmt.Sprintf("rm -rf %s/node_modules %s/package-lock.json", projectDir, projectDir),
		})
		require.NoError(t, err)
		resp.Body.Close()

		inotifyStartTime = time.Now()

		// Create the log file first
		resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": fmt.Sprintf("touch %s", inotifyLogFile),
		})
		require.NoError(t, err)
		resp.Body.Close()

		// Start inotify watcher in background
		watchCmd := fmt.Sprintf(`nohup inotifywait -m -r --timefmt "%%s" --format "%%T|%%e|%%w%%f" --event modify,create,delete,move %s >> %s 2>&1 & echo $!`, projectDir, inotifyLogFile)
		resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": watchCmd,
		})
		require.NoError(t, err)
		resp.Body.Close()

		// Wait for watcher to establish
		time.Sleep(500 * time.Millisecond)

		// Run npm install again
		inotifyInstallStart = time.Now()
		resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": fmt.Sprintf("cd %s && npm install --silent 2>&1", projectDir),
			"timeout": 180000,
		})
		require.NoError(t, err)
		resp.Body.Close()
		inotifyInstallEnd = time.Now()

		// Wait for events to be logged
		time.Sleep(3 * time.Second)

		// Stop inotify
		resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
			"command": "pkill -f inotifywait || true",
		})
		require.NoError(t, err)
		resp.Body.Close()
		time.Sleep(500 * time.Millisecond)

		inotifyEndTime = time.Now()

		// Read inotify log
		var inotifyLogResp filesystem.FileWithContent
		resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(inotifyLogFile), nil, &inotifyLogResp)
		inotifyLogContent := ""
		if err == nil {
			inotifyLogContent = string(inotifyLogResp.Content)
			resp.Body.Close()
		} else {
			t.Logf("Warning: Could not read inotify log file: %v", err)
		}

		// Parse inotify events
		inotifyLines = strings.Split(strings.TrimSpace(inotifyLogContent), "\n")
		for _, line := range inotifyLines {
			if line == "" {
				continue
			}
			parts := strings.Split(line, "|")
			if len(parts) >= 3 {
				event := parts[1]
				path := parts[2]
				if _, exists := inotifyPathToEvents[path]; !exists {
					inotifyPathToEvents[path] = make(map[string]bool)
				}
				inotifyPathToEvents[path][event] = true
			}
		}
	}

	// Build Blaxel path to events map
	blaxelPathToEvents := make(map[string]map[string]bool)
	blaxelEventsMu.Lock()
	for _, event := range blaxelEvents {
		path, _ := event["path"].(string)
		name, _ := event["name"].(string)
		op, _ := event["op"].(string)
		fullPath := path + "/" + name
		if _, exists := blaxelPathToEvents[fullPath]; !exists {
			blaxelPathToEvents[fullPath] = make(map[string]bool)
		}
		blaxelPathToEvents[fullPath][op] = true
	}
	blaxelEventsMu.Unlock()

	// Compute set differences
	var onlyInBlaxel, onlyInInotify, inBoth []string
	for path := range blaxelPathToEvents {
		if _, exists := inotifyPathToEvents[path]; exists {
			inBoth = append(inBoth, path)
		} else {
			onlyInBlaxel = append(onlyInBlaxel, path)
		}
	}
	for path := range inotifyPathToEvents {
		if _, exists := blaxelPathToEvents[path]; !exists {
			onlyInInotify = append(onlyInInotify, path)
		}
	}

	// Get unique event types for Blaxel
	blaxelEventTypes := make(map[string]bool)
	for _, events := range blaxelPathToEvents {
		for evt := range events {
			blaxelEventTypes[evt] = true
		}
	}

	// ========== Results comparison ==========
	t.Log("\n========== npm install Watch Comparison ==========")
	t.Log("")
	t.Log("--- Blaxel Native watchFolder ---")
	t.Logf("  Total events: %d", len(blaxelEvents))
	t.Logf("  Unique paths: %d", len(blaxelPathToEvents))
	t.Logf("  Install time: %v", blaxelInstallEnd.Sub(blaxelInstallStart))
	t.Logf("  Total time (including setup): %v", blaxelEndTime.Sub(blaxelStartTime))
	eventTypesList := make([]string, 0, len(blaxelEventTypes))
	for evt := range blaxelEventTypes {
		eventTypesList = append(eventTypesList, evt)
	}
	t.Logf("  Event types: %s", strings.Join(eventTypesList, ", "))
	t.Log("")

	if inotifyAvailable {
		t.Log("--- inotify-based watching ---")
		t.Logf("  Total events: %d", len(inotifyLines))
		t.Logf("  Unique paths: %d", len(inotifyPathToEvents))
		t.Logf("  Install time: %v", inotifyInstallEnd.Sub(inotifyInstallStart))
		t.Logf("  Total time (including setup): %v", inotifyEndTime.Sub(inotifyStartTime))
		t.Log("")

		// Calculate ratios
		if len(inotifyLines) > 0 {
			eventRatio := float64(len(blaxelEvents)) / float64(len(inotifyLines))
			t.Logf("  Event count ratio (Blaxel/inotify): %.2fx", eventRatio)
		}
		if len(inotifyPathToEvents) > 0 {
			pathRatio := float64(len(blaxelPathToEvents)) / float64(len(inotifyPathToEvents))
			t.Logf("  Unique paths ratio (Blaxel/inotify): %.2fx", pathRatio)
		}
		t.Log("")
		t.Log("--- Path Differences ---")
		t.Logf("  Paths in both: %d", len(inBoth))
		t.Logf("  Only in Blaxel: %d", len(onlyInBlaxel))
		t.Logf("  Only in inotify: %d", len(onlyInInotify))
		t.Log("")

		// Show sample of paths only in each
		sampleLimit := 20
		if len(onlyInBlaxel) > 0 {
			t.Logf("  Sample paths only in Blaxel (first %d):", min(sampleLimit, len(onlyInBlaxel)))
			for i, p := range onlyInBlaxel {
				if i >= sampleLimit {
					t.Logf("    ... and %d more", len(onlyInBlaxel)-sampleLimit)
					break
				}
				events := make([]string, 0)
				for evt := range blaxelPathToEvents[p] {
					events = append(events, evt)
				}
				t.Logf("    - [%s] %s", strings.Join(events, ", "), p)
			}
		}
		t.Log("")
		if len(onlyInInotify) > 0 {
			t.Logf("  Sample paths only in inotify (first %d):", min(sampleLimit, len(onlyInInotify)))
			for i, p := range onlyInInotify {
				if i >= sampleLimit {
					t.Logf("    ... and %d more", len(onlyInInotify)-sampleLimit)
					break
				}
				events := make([]string, 0)
				for evt := range inotifyPathToEvents[p] {
					events = append(events, evt)
				}
				t.Logf("    - [%s] %s", strings.Join(events, ", "), p)
			}
		}

		// Show event type breakdown for differences
		blaxelOnlyEventTypes := make(map[string]int)
		for _, p := range onlyInBlaxel {
			for evt := range blaxelPathToEvents[p] {
				blaxelOnlyEventTypes[evt]++
			}
		}
		inotifyOnlyEventTypes := make(map[string]int)
		for _, p := range onlyInInotify {
			for evt := range inotifyPathToEvents[p] {
				inotifyOnlyEventTypes[evt]++
			}
		}

		t.Log("")
		t.Log("--- Event Type Breakdown for Differences ---")
		if len(blaxelOnlyEventTypes) > 0 {
			t.Log("  Events only in Blaxel by type:")
			for evt, count := range blaxelOnlyEventTypes {
				t.Logf("    %s: %d", evt, count)
			}
		}
		if len(inotifyOnlyEventTypes) > 0 {
			t.Log("  Events only in inotify by type:")
			for evt, count := range inotifyOnlyEventTypes {
				t.Logf("    %s: %d", evt, count)
			}
		}
	}

	// Assertions - Blaxel should capture significant activity
	assert.Greater(t, len(blaxelEvents), 100, "Blaxel should capture at least 100 events")

	if inotifyAvailable {
		// The key assertion: after our fix, Blaxel should capture everything that inotify captures
		// onlyInInotify should be 0 - meaning no paths were missed by Blaxel
		assert.Equal(t, 0, len(onlyInInotify), "Blaxel should capture all paths that inotify captures. Missing %d paths.", len(onlyInInotify))
	}
}

// TestFileSystemDownload tests the file download functionality with different Accept headers and query parameters
func TestFileSystemDownload(t *testing.T) {
	// Create test files with different extensions
	testFiles := []struct {
		name         string
		content      string
		extension    string
		expectedType string
	}{
		{"test-txt", "Hello, World!", ".txt", "text/plain"},
		{"test-json", `{"key": "value"}`, ".json", "application/json"},
		{"test-html", "<html><body>Test</body></html>", ".html", "text/html"},
		{"test-js", "console.log('test');", ".js", "application/javascript"},
		{"test-css", "body { color: red; }", ".css", "text/css"},
		{"test-binary", "binary content here", ".bin", "application/octet-stream"},
	}

	t.Run("download with Accept header", func(t *testing.T) {
		for _, tf := range testFiles {
			t.Run(tf.name, func(t *testing.T) {
				// Create the test file
				testPath := fmt.Sprintf("/tmp/%s-%d%s", tf.name, time.Now().UnixNano(), tf.extension)

				createFileRequest := map[string]interface{}{
					"content": tf.content,
				}
				var successResp handler.SuccessResponse
				resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
				require.NoError(t, err)
				resp.Body.Close()
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Make a request with Accept: application/octet-stream header
				req, err := http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(testPath), nil)
				require.NoError(t, err)
				req.Header.Set("Accept", "application/octet-stream")

				resp, err = common.Client.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				// Verify status code
				assert.Equal(t, http.StatusOK, resp.StatusCode)

				// Verify Content-Type header
				contentType := resp.Header.Get("Content-Type")
				assert.Equal(t, tf.expectedType, contentType, "Content-Type should match expected type")

				// Verify Content-Disposition header
				contentDisposition := resp.Header.Get("Content-Disposition")
				assert.Contains(t, contentDisposition, "attachment", "Content-Disposition should indicate attachment")
				assert.Contains(t, contentDisposition, tf.extension, "Content-Disposition should contain file extension")

				// Verify content
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assert.Equal(t, tf.content, string(body), "Downloaded content should match original content")

				// Clean up
				resp2, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
				require.NoError(t, err)
				resp2.Body.Close()
			})
		}
	})

	t.Run("download with query parameter", func(t *testing.T) {
		// Create a test file
		testContent := "Download via query parameter"
		testPath := fmt.Sprintf("/tmp/test-download-%d.txt", time.Now().UnixNano())

		createFileRequest := map[string]interface{}{
			"content": testContent,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Make a request with download=true query parameter
		resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(testPath)+"?download=true", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Verify status code
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Verify Content-Type header
		contentType := resp.Header.Get("Content-Type")
		assert.Equal(t, "text/plain", contentType)

		// Verify Content-Disposition header
		contentDisposition := resp.Header.Get("Content-Disposition")
		assert.Contains(t, contentDisposition, "attachment")

		// Verify content
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.Equal(t, testContent, string(body))

		// Clean up
		resp2, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
		require.NoError(t, err)
		resp2.Body.Close()
	})

	t.Run("JSON mode by default", func(t *testing.T) {
		// Create a test file
		testContent := "Default JSON mode"
		testPath := fmt.Sprintf("/tmp/test-json-mode-%d.txt", time.Now().UnixNano())

		createFileRequest := map[string]interface{}{
			"content": testContent,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Make a request without special headers (should return JSON)
		var fileResponse filesystem.FileWithContent
		resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResponse)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Verify status code and JSON response
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, testContent, string(fileResponse.Content))
		assert.Equal(t, testPath, fileResponse.Path)
		assert.NotEmpty(t, fileResponse.Permissions)
		assert.NotEmpty(t, fileResponse.Owner)

		// Clean up
		resp2, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
		require.NoError(t, err)
		resp2.Body.Close()
	})

	t.Run("JSON mode with explicit Accept header", func(t *testing.T) {
		// Create a test file
		testContent := "Explicit JSON mode"
		testPath := fmt.Sprintf("/tmp/test-explicit-json-%d.txt", time.Now().UnixNano())

		createFileRequest := map[string]interface{}{
			"content": testContent,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Make a request with Accept: application/json header
		req, err := http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(testPath), nil)
		require.NoError(t, err)
		req.Header.Set("Accept", "application/json")

		resp, err = common.Client.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Verify status code
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		// Parse JSON response
		var fileResponse filesystem.FileWithContent
		err = common.ParseResponse(resp, &fileResponse)
		require.NoError(t, err)

		assert.Equal(t, testContent, string(fileResponse.Content))
		assert.Equal(t, testPath, fileResponse.Path)

		// Clean up
		resp2, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
		require.NoError(t, err)
		resp2.Body.Close()
	})

	t.Run("download with various file types", func(t *testing.T) {
		fileTypes := []struct {
			extension string
			mimeType  string
		}{
			{".pdf", "application/pdf"},
			{".zip", "application/zip"},
			{".tar", "application/x-tar"},
			{".gz", "application/gzip"},
			{".jpg", "image/jpeg"},
			{".jpeg", "image/jpeg"},
			{".png", "image/png"},
			{".gif", "image/gif"},
			{".svg", "image/svg+xml"},
			{".xml", "application/xml"},
		}

		for _, ft := range fileTypes {
			t.Run(ft.extension, func(t *testing.T) {
				testContent := "test content for " + ft.extension
				testPath := fmt.Sprintf("/tmp/test-mime-%d%s", time.Now().UnixNano(), ft.extension)

				createFileRequest := map[string]interface{}{
					"content": testContent,
				}
				var successResp handler.SuccessResponse
				resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testPath), createFileRequest, &successResp)
				require.NoError(t, err)
				resp.Body.Close()

				// Request with Accept: application/octet-stream
				req, err := http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(testPath), nil)
				require.NoError(t, err)
				req.Header.Set("Accept", "application/octet-stream")

				resp, err = common.Client.Do(req)
				require.NoError(t, err)
				defer resp.Body.Close()

				// Verify Content-Type
				contentType := resp.Header.Get("Content-Type")
				assert.Equal(t, ft.mimeType, contentType)

				// Clean up
				resp2, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp)
				require.NoError(t, err)
				resp2.Body.Close()
			})
		}
	})
}

func TestFileSystemFind(t *testing.T) {
	// Create a unique test directory
	testDir := fmt.Sprintf("/tmp/find-test-%d", time.Now().UnixNano())

	// Create test directory structure
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse

	// Create main test directory
	resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testDir), createDirRequest, &successResp)
	require.NoError(t, err)
	resp.Body.Close()

	// Create subdirectories
	subdirs := []string{"src", "src/utils", "docs", ".hidden", "node_modules"}
	for _, subdir := range subdirs {
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testDir+"/"+subdir), createDirRequest, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Create test files
	testFiles := []struct {
		path    string
		content string
	}{
		{"file1.go", "package main"},
		{"file2.go", "package utils"},
		{"readme.md", "# Readme"},
		{"src/main.go", "package main"},
		{"src/utils/helper.go", "package utils"},
		{"docs/guide.md", "# Guide"},
		{".hidden/secret.txt", "secret"},
		{".hiddenfile", "hidden content"},
		{"node_modules/package.json", "{}"},
	}

	for _, tf := range testFiles {
		createFileRequest := map[string]interface{}{
			"content": tf.content,
		}
		resp, err := common.MakeRequestAndParse(http.MethodPut, common.EncodeFilesystemPath(testDir+"/"+tf.path), createFileRequest, &successResp)
		require.NoError(t, err)
		resp.Body.Close()
	}

	// Cleanup function
	defer func() {
		resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testDir), nil, &successResp)
		if err == nil {
			resp.Body.Close()
		}
	}()

	t.Run("find all Go files", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.go", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: file1.go, file2.go, src/main.go, src/utils/helper.go (4 files)
		assert.Equal(t, 4, findResp.Total)

		// Verify all matches are Go files
		for _, match := range findResp.Matches {
			assert.True(t, strings.HasSuffix(match.Path, ".go"), "Expected .go file, got: %s", match.Path)
			assert.Equal(t, "file", match.Type)
		}
	})

	t.Run("find markdown files", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.md", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: readme.md, docs/guide.md (2 files)
		assert.Equal(t, 2, findResp.Total)
	})

	t.Run("find directories", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?type=directory", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		fmt.Println(findResp)
		// Should find: . (self), src, src/utils, docs (4 directories - node_modules and .hidden are excluded by default)
		assert.Equal(t, 4, findResp.Total)

		// Verify all matches are directories
		for _, match := range findResp.Matches {
			assert.Equal(t, "directory", match.Type)
		}
	})

	t.Run("maxResults limits results", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.go&maxResults=2", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, 2, findResp.Total)
		assert.Len(t, findResp.Matches, 2)
	})

	t.Run("custom excludeDirs", func(t *testing.T) {
		var findResp handler.FindResponse
		// Exclude only 'docs' directory, include node_modules (which is excluded by default)
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.md,*.json&excludeDirs=docs", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: readme.md, node_modules/package.json (2 files - docs/guide.md excluded)
		assert.Equal(t, 2, findResp.Total)

		// Verify docs/guide.md is not in results
		for _, match := range findResp.Matches {
			assert.False(t, strings.HasPrefix(match.Path, "docs/"), "docs directory should be excluded")
		}
	})

	t.Run("include hidden files with excludeHidden=false", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.txt&excludeHidden=false&excludeDirs=node_modules", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: .hidden/secret.txt (1 file)
		assert.Equal(t, 1, findResp.Total)
	})

	t.Run("hidden files excluded by default", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.txt", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find nothing - .hidden/secret.txt is in hidden directory
		assert.Equal(t, 0, findResp.Total)
	})

	t.Run("multiple patterns", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?patterns=*.go,*.md", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: 4 .go files + 2 .md files = 6 files
		assert.Equal(t, 6, findResp.Total)
	})

	t.Run("invalid search type returns error", func(t *testing.T) {
		resp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemFindPath(testDir)+"?type=invalid", nil)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("find in subdirectory", func(t *testing.T) {
		var findResp handler.FindResponse
		resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemFindPath(testDir+"/src")+"?patterns=*.go", nil, &findResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should find: main.go, utils/helper.go (2 files in src directory)
		assert.Equal(t, 2, findResp.Total)
	})
}
