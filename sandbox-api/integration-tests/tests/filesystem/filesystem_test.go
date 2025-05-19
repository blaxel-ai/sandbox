package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/gorilla/websocket"
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

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

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

// TestFileSystemWatch tests the streaming watch endpoint for file modifications
func TestFileSystemWatch(t *testing.T) {
	t.Parallel()

	dir := fmt.Sprintf("/tmp/test-watch-%d", time.Now().UnixNano())
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+dir, createDirRequest, &successResp)
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
		resp, err := common.MakeRequest("GET", "/watch/filesystem"+watchPath, nil)
		require.NoError(t, err)
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
	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+filePath, createFileRequest, &successResp)
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

// TestFileSystemWatchWebSocket tests the streaming watch endpoint over WebSocket for file modifications
func TestFileSystemWatchWebSocket(t *testing.T) {
	dir := fmt.Sprintf("/tmp/test-watchws-%d", time.Now().UnixNano())
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+dir, createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Build ws:// URL from API base URL
	apiBase := os.Getenv("API_BASE_URL")
	if apiBase == "" {
		apiBase = "http://localhost:8080"
	}
	u, err := url.Parse(apiBase)
	require.NoError(t, err)
	u.Scheme = "ws"
	u.Path = "/ws/watch/filesystem" + dir

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(t, err)
	defer ws.Close()

	fileName := "watchedws.txt"
	filePath := dir + "/" + fileName

	done := make(chan struct{})
	received := make(chan map[string]interface{}, 1)
	go func() {
		for {
			_, msg, err := ws.ReadMessage()

			if err != nil {
				close(done)
				return
			}
			var payload map[string]interface{}
			err = json.Unmarshal(msg, &payload)
			if err == nil {
				received <- payload
				return
			}
		}
	}()

	// Wait a moment to ensure watcher is ready
	time.Sleep(300 * time.Millisecond)

	// Create a file in the watched directory
	content := []byte("hello watch ws!")
	createFileRequest := map[string]interface{}{
		"content": string(content),
	}
	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+filePath, createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Wait for watcher to receive the event or timeout
	select {
	case event := <-received:
		assert.Equal(t, fileName, event["name"], "Watcher should receive the created file path in event")
		assert.Equal(t, dir, event["path"], "Watcher should receive the created file path in event")
		assert.Contains(t, event["op"], "CREATE")
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for file event from websocket watcher")
	}

	// Clean up
	_, _ = common.MakeRequest(http.MethodDelete, "/filesystem"+dir+"?recursive=true", nil)
}

// TestFileSystemWatchRecursive tests recursive streaming watch endpoint for file modifications in subdirectories
func TestFileSystemWatchRecursive(t *testing.T) {
	dir := fmt.Sprintf("/tmp/test-watch-recursive-%d", time.Now().UnixNano())
	subdir := dir + "/subdir"
	fileName := "watched.txt"
	filePath := subdir + "/" + fileName

	// Create parent directory
	createDirRequest := map[string]interface{}{
		"isDirectory": true,
	}
	var successResp handler.SuccessResponse
	resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+dir, createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Create subdirectory
	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+subdir, createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	watchPath := dir + "/**"
	done := make(chan struct{})
	received := make(chan map[string]interface{}, 5)

	// Start watcher goroutine
	go func() {
		resp, err := common.MakeRequest("GET", "/watch/filesystem"+watchPath, nil)
		require.NoError(t, err)
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
	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+filePath, createFileRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
	_ = waitForEvent("CREATE", fileName)

	// 2. Delete the file
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+filePath, nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	waitForEvent("REMOVE", fileName)

	// 3. Create a new subdirectory
	newSubdir := dir + "/subdir2"
	resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+newSubdir, createDirRequest, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	waitForEvent("CREATE", "subdir2")

	// 3. Delete the subdirectory
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+newSubdir, nil, &successResp)
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
		subdir := dir + "/subdir"
		fileName := "watched.txt"
		filePath := subdir + "/" + fileName
		ignoredFileName := "ignored.txt"
		ignoredFilePath := subdir + "/" + ignoredFileName

		createDirRequest := map[string]interface{}{
			"isDirectory": true,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+dir, createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+subdir, createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		watchPath := dir + "/**"
		done := make(chan struct{})
		received := make(chan map[string]interface{}, 10)

		// Start watcher goroutine with ignore=ignored.txt
		go func() {
			resp, err := common.MakeRequest("GET", "/watch/filesystem"+watchPath+"?ignore=ignored.txt", nil)
			require.NoError(t, err)
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
		resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+filePath, createFileRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Create a file that SHOULD be ignored
		createIgnoredFileRequest := map[string]interface{}{
			"content": "ignored",
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+ignoredFilePath, createIgnoredFileRequest, &successResp)
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
		ignoredSubdir := dir + "/ignored-folder"
		fileName := "file.txt"
		filePath := ignoredSubdir + "/" + fileName

		createDirRequest := map[string]interface{}{
			"isDirectory": true,
		}
		var successResp handler.SuccessResponse
		resp, err := common.MakeRequestAndParse(http.MethodPut, "/filesystem"+dir, createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()

		watchPath := dir + "/**"
		done := make(chan struct{})
		received := make(chan map[string]interface{}, 10)

		// Start watcher goroutine with ignore=ignored-folder
		go func() {
			resp, err := common.MakeRequest("GET", "/watch/filesystem"+watchPath+"?ignore=ignored-folder", nil)
			require.NoError(t, err)
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
		resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+ignoredSubdir, createDirRequest, &successResp)
		require.NoError(t, err)
		defer resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Contains(t, successResp.Message, "success")

		// Create a file inside the ignored subdirectory
		createFileRequest := map[string]interface{}{
			"content": "should be ignored",
		}
		resp, err = common.MakeRequestAndParse(http.MethodPut, "/filesystem"+filePath, createFileRequest, &successResp)
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
