package tests

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessOperations tests process operations
func TestProcessOperations(t *testing.T) {
	// Create a process
	processName := "test-process"
	processRequest := map[string]interface{}{
		"name":    processName,
		"command": "echo 'hello world'",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	// Verify process ID is returned
	require.Contains(t, processResponse, "pid")
	processID := processResponse["pid"].(string)
	require.Contains(t, processResponse, "name")

	// Test getting process details by PID
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	// Verify process status
	require.Contains(t, processDetails, "status")

	// Test getting process details by name
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	// Verify process details match when getting by name
	assert.Equal(t, processID, processDetails["pid"])
	assert.Equal(t, processName, processDetails["name"])

	var processList []map[string]interface{}
	resp, err = common.MakeRequest(http.MethodGet, "/process", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&processList)
	require.NoError(t, err)

	// Test stopping process by name
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait a bit for the process to stop
	time.Sleep(100 * time.Millisecond)

	// Verify process is stopped when getting by name
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "stopped", processDetails["status"])

	processWaitForCompletionName := "test-process-wait-for-completion"
	processWaitForCompletionRequest := map[string]interface{}{
		"name":              processWaitForCompletionName,
		"command":           "echo 'hello world'",
		"cwd":               "/",
		"waitForCompletion": true,
	}

	resp, err = common.MakeRequest(http.MethodPost, "/process", processWaitForCompletionRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processWaitForCompletionResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processWaitForCompletionResponse)
	require.NoError(t, err)

	// Verify process ID is returned
	require.Contains(t, processWaitForCompletionResponse, "pid")
	require.Contains(t, processWaitForCompletionResponse, "name")
	// Verify the exit code is 42 (the failure code we specified)
	assert.Equal(t, float64(0), processWaitForCompletionResponse["exitCode"]) // JSON unmarshals numbers as float64
	assert.Equal(t, "completed", processWaitForCompletionResponse["status"])
	fmt.Println("exit code", processWaitForCompletionResponse["exitCode"])
	fmt.Println("completed at", processWaitForCompletionResponse["completedAt"])

	// Test a failing process to ensure exit code is correctly set for failures
	processFailName := "test-process-fail"
	processFailRequest := map[string]interface{}{
		"name":              processFailName,
		"command":           "sh -c 'exit 42'", // This will exit with code 42
		"cwd":               "/",
		"waitForCompletion": true,
	}

	resp, err = common.MakeRequest(http.MethodPost, "/process", processFailRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processFailResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processFailResponse)
	require.NoError(t, err)

	// Verify process ID is returned
	require.Contains(t, processFailResponse, "pid")
	require.Contains(t, processFailResponse, "name")
	require.Contains(t, processFailResponse, "exitCode")
	require.Contains(t, processFailResponse, "status")

	// Verify the exit code is 42 (the failure code we specified)
	assert.Equal(t, float64(42), processFailResponse["exitCode"]) // JSON unmarshals numbers as float64
	assert.Equal(t, "failed", processFailResponse["status"])

	fmt.Println("fail exit code", processFailResponse["exitCode"])
	fmt.Println("fail status", processFailResponse["status"])
}

// TestLongRunningProcess tests starting, monitoring, and stopping a long-running process
func TestLongRunningProcess(t *testing.T) {
	// Create a long-running process
	processRequest := map[string]interface{}{
		"command": "sleep 10",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	// Verify process ID is returned
	require.Contains(t, processResponse, "pid")
	processID := processResponse["pid"].(string)

	// Give the process time to start
	time.Sleep(1 * time.Second)

	// Get process logs
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processID+"/logs", nil)
	require.NoError(t, err)
	resp.Body.Close()

	// This will depend on your API implementation, but generally should be OK
	assert.Contains(t, []int{http.StatusOK, http.StatusNoContent}, resp.StatusCode)

	// Stop the process
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Check that the process is stopped
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	var stoppedProcessDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&stoppedProcessDetails)
	require.NoError(t, err)

	// The status should indicate the process is no longer running
	// This might be "exited", "stopped", or something similar depending on your API
	status, ok := stoppedProcessDetails["status"].(string)
	require.True(t, ok, "Status should be a string")
	assert.NotEqual(t, "running", status)
}

func TestProcessKillByName(t *testing.T) {
	// Create a long-running process
	processRequest := map[string]interface{}{
		"command": "sleep 100",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	require.Contains(t, processResponse, "name")
	processName := processResponse["name"].(string)

	// Test killing process by name
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processName+"/kill", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait a bit for the process to be killed
	time.Sleep(100 * time.Millisecond)

	// Verify process is killed when getting by name
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "killed", processDetails["status"])
}

func TestProcessOutputByName(t *testing.T) {
	// Create a process with output
	processRequest := map[string]interface{}{
		"command": "echo 'test output' && echo 'test error' >&2",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	require.Contains(t, processResponse, "name")
	processName := processResponse["name"].(string)

	// Wait a bit for the process to complete
	time.Sleep(100 * time.Millisecond)

	// Test getting process output by name
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName+"/logs", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var outputResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&outputResponse)
	require.NoError(t, err)

	require.Contains(t, outputResponse, "logs")
	require.Contains(t, outputResponse, "stdout")
	require.Contains(t, outputResponse, "stderr")

	assert.Equal(t, "test output\n", outputResponse["stdout"])
	assert.Equal(t, "test error\n", outputResponse["stderr"])
	assert.Equal(t, "test output\ntest error\n", outputResponse["logs"])
}

func TestProcessStreamLogs(t *testing.T) {
	// Create a process that outputs 5 lines quickly
	processRequest := map[string]interface{}{
		"command": "for i in $(seq 1 5); do echo tick $i; sleep 0.05; done",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	require.Contains(t, processResponse, "name")
	processName := processResponse["name"].(string)

	// Start streaming logs
	streamResp, err := common.MakeRequest(http.MethodGet, "/process/"+processName+"/logs/stream", nil)
	require.NoError(t, err)
	defer streamResp.Body.Close()

	assert.Equal(t, http.StatusOK, streamResp.StatusCode)

	reader := bufio.NewReader(streamResp.Body)
	linesCh := make(chan string, 10)
	done := make(chan struct{})

	// Goroutine to read lines as they arrive
	go func() {
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(done)
				return
			}
			linesCh <- strings.TrimSpace(line)
		}
	}()

	// Collect lines for up to 0.5 seconds
	received := []string{}
	collectTimeout := time.After(500 * time.Millisecond)
collectLoop:
	for {
		select {
		case line := <-linesCh:
			if line != "" {
				received = append(received, line)
			}
		case <-done:
			break collectLoop
		case <-collectTimeout:
			break collectLoop
		}
	}

	// We expect at least 5 lines like "tick 1", ..., "tick 5"
	count := 0
	for _, line := range received {
		if strings.HasPrefix(line, "stdout:") && strings.Contains(line, "tick") {
			count++
		}
	}
	assert.GreaterOrEqual(t, count, 5, "should receive at least 5 tick lines from stream")
}

func TestProcessStreamLogsWebSocket(t *testing.T) {
	// Start a process that outputs several lines
	processRequest := map[string]interface{}{
		"command": "for i in $(seq 1 5); do echo wslog $i; sleep 0.05; done",
		"cwd":     "/",
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	processName, ok := processResponse["name"].(string)
	require.True(t, ok)

	// Build ws:// URL from API base URL
	apiBase := os.Getenv("API_BASE_URL")
	if apiBase == "" {
		apiBase = "http://localhost:8080"
	}
	u, err := url.Parse(apiBase)
	require.NoError(t, err)
	u.Scheme = "ws"
	u.Path = "/ws/process/" + processName + "/logs/stream"

	ws, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(t, err)
	defer ws.Close()

	logLines := []string{}
	done := make(chan struct{})
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
				if logVal, ok := payload["log"].(string); ok {
					logLines = append(logLines, logVal)
				}
			}
		}
	}()

	// Wait for logs or timeout
	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	count := 0
	for _, line := range logLines {
		if strings.Contains(line, "wslog") {
			count++
		}
	}
	assert.GreaterOrEqual(t, count, 5, "should receive at least 5 wslog lines from websocket stream")
}

func TestProcessKillWithChildProcesses(t *testing.T) {
	// Test similar to the TypeScript example: start a dev-like process, stream logs, then kill
	// This test runs twice to verify that ports are properly freed after killing

	// First run: Start Next.js, verify it binds to port 3000, then kill it
	t.Log("=== First run: Starting Next.js dev server ===")
	firstRunSuccess := runNextJsAndKill(t, "dev")

	// Verify the first run was successful and found localhost:3000
	assert.True(t, firstRunSuccess, "First run should successfully start Next.js and show localhost:3000")

	// Wait a moment to ensure cleanup is complete
	time.Sleep(2 * time.Second)

	// Second run: Start Next.js again to verify port 3000 is available
	t.Log("=== Second run: Starting Next.js dev server again ===")
	secondRunSuccess := runNextJsAndKill(t, "dev")

	// Verify the second run was also successful
	assert.True(t, secondRunSuccess, "Second run should also successfully start Next.js, proving port 3000 was freed")

	t.Log("=== Test passed: Process group killing properly frees ports ===")
}

// runNextJsAndKill starts a Next.js dev process, waits for it to show localhost:3000, then kills it
// Returns true if localhost:3000 was found in the logs
func runNextJsAndKill(t *testing.T, processName string) bool {
	processRequest := map[string]interface{}{
		"name":              processName,
		"command":           "npm run dev",
		"env":               map[string]string{"PORT": "3000"},
		"workingDir":        "/blaxel/app",
		"waitForCompletion": false,
	}

	// Start the process
	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	require.Contains(t, processResponse, "name")
	actualProcessName := processResponse["name"].(string)
	assert.Equal(t, processName, actualProcessName)

	// Start streaming logs
	streamResp, err := common.MakeRequest(http.MethodGet, "/process/"+processName+"/logs/stream", nil)
	require.NoError(t, err)
	defer streamResp.Body.Close()

	assert.Equal(t, http.StatusOK, streamResp.StatusCode)

	// Read logs and look for localhost:3000
	reader := bufio.NewReader(streamResp.Body)
	logLines := []string{}
	foundLocalhost3000 := false

	// Give Next.js up to 30 seconds to start up and show localhost:3000
	logTimeout := time.After(30 * time.Second)
	logDone := make(chan struct{})
	var closeOnce sync.Once

	go func() {
		for {
			select {
			case <-logTimeout:
				closeOnce.Do(func() { close(logDone) })
				return
			default:
				line, err := reader.ReadString('\n')
				if err != nil {
					closeOnce.Do(func() { close(logDone) })
					return
				}
				if strings.TrimSpace(line) != "" {
					logLines = append(logLines, strings.TrimSpace(line))
					t.Logf("[%s] %s", processName, strings.TrimSpace(line))

					// Look for the specific localhost:3000 indicator
					if strings.Contains(line, "http://localhost:3000") {
						foundLocalhost3000 = true
						t.Logf("[%s] Found localhost:3000 in logs! Next.js is running.", processName)
						// Give it a moment more to fully start, then signal completion
						go func() {
							time.Sleep(5 * time.Second)
							closeOnce.Do(func() { close(logDone) })
						}()
					}
				}
			}
		}
	}()

	<-logDone

	// Log what we found
	t.Logf("[%s] Collected %d log lines", processName, len(logLines))
	if foundLocalhost3000 {
		t.Logf("[%s] ✓ Successfully found http://localhost:3000 in logs", processName)
	} else {
		t.Logf("[%s] ✗ Did not find http://localhost:3000 in logs", processName)
		t.Logf("[%s] All logs: %v", processName, logLines)
	}

	// Verify process is running before killing
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "running", processDetails["status"], "Process should be running before kill")

	// Kill the process - this should kill the entire process group including Next.js
	t.Logf("[%s] Killing process...", processName)
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processName+"/kill", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var killResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&killResponse)
	require.NoError(t, err)

	assert.Contains(t, killResponse["message"], "killed successfully")
	t.Logf("[%s] Process killed successfully", processName)

	// Wait for the kill to take effect and for all child processes to be terminated
	time.Sleep(2 * time.Second)

	// Verify process is killed
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "killed", processDetails["status"], "Process should be killed after kill command")
	t.Logf("[%s] Verified process status is 'killed'", processName)

	return foundLocalhost3000
}
