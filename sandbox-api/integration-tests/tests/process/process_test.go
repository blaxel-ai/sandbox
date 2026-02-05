package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessOperations tests process operations
func TestProcessOperations(t *testing.T) {
	// Create a process
	processName := "test-process"
	processRequest := map[string]interface{}{
		"name":    processName,
		"command": "echo 'hello world' && sleep 1",
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
	require.Contains(t, processResponse, "logs")
	require.IsType(t, nil, processResponse["logs"])

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

	// Verify the logs are returned
	require.Contains(t, processWaitForCompletionResponse, "logs")
	assert.Equal(t, "hello world\n", processWaitForCompletionResponse["logs"])

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
		"command":           "echo 'test output'; echo 'test error' >&2",
		"waitForCompletion": true,
		"cwd":               "/",
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
	require.Contains(t, processResponse, "pid")

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
	// logs contains both stdout and stderr, but order is non-deterministic
	logs := outputResponse["logs"].(string)
	assert.Contains(t, logs, "test output\n")
	assert.Contains(t, logs, "test error\n")
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

func TestProcessKillWithChildProcesses(t *testing.T) {
	// Test similar to the TypeScript example: start a dev-like process, stream logs, then kill
	// This test runs twice to verify that ports are properly freed after killing

	// Check if prerequisites exist (Next.js app, npm, etc.)
	if !checkNextJsPrerequisites(t) {
		t.Skip("Skipping Next.js test: prerequisites not available (requires /blaxel/app with npm)")
	}

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

// checkNextJsPrerequisites checks if the Next.js app and npm are available
func checkNextJsPrerequisites(t *testing.T) bool {
	// Try to execute a simple command to check if the working directory exists
	checkRequest := map[string]interface{}{
		"name":              "check-nextjs",
		"command":           "test -d /blaxel/app && test -f /blaxel/app/package.json",
		"workingDir":        "/",
		"waitForCompletion": true,
		"timeout":           5,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/process", checkRequest)
	if err != nil {
		t.Logf("Failed to check prerequisites: %v", err)
		return false
	}
	defer resp.Body.Close()

	// If the command failed (non-zero exit), prerequisites don't exist
	if resp.StatusCode != http.StatusOK {
		return false
	}

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	if err != nil {
		return false
	}

	// Check if the process completed successfully (exit code 0)
	if status, ok := processResponse["status"].(string); ok {
		return status == "completed" || status == "running"
	}

	return false
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

// TestProcessRestartOnFailure tests the restart on failure functionality
func TestProcessRestartOnFailure(t *testing.T) {
	t.Log("=== Testing process restart on failure ===")

	// Test a process that fails immediately and should restart
	processRequest := map[string]interface{}{
		"name":              "test-restart-on-failure",
		"command":           "exit 1", // This will fail immediately
		"cwd":               "/",
		"waitForCompletion": true,
		"restartOnFailure":  true,
		"maxRestarts":       3,
		"timeout":           10,
	}

	t.Log("Starting process that will fail and restart...")
	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	t.Logf("Response status code: %d", resp.StatusCode)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Process creation should succeed even if it restarts")

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	t.Logf("Process response: %+v", processResponse)

	// Verify process ID is returned
	require.Contains(t, processResponse, "pid", "Response should contain pid")
	require.Contains(t, processResponse, "name", "Response should contain name")
	require.Contains(t, processResponse, "status", "Response should contain status")
	require.Contains(t, processResponse, "restartCount", "Response should contain restartCount")

	// The process should have restarted maxRestarts times and then failed
	assert.Equal(t, "failed", processResponse["status"], "Process should be in failed state after all restarts")
	assert.Equal(t, float64(3), processResponse["restartCount"], "Process should have restarted 3 times")
	assert.Equal(t, float64(1), processResponse["exitCode"], "Exit code should be 1")

	// Check the logs to verify restart messages
	if logs, ok := processResponse["logs"].(string); ok {
		t.Logf("Process logs:\n%s", logs)
		assert.Contains(t, logs, "Attempting restart 1/3", "Logs should contain first restart message")
		assert.Contains(t, logs, "Attempting restart 2/3", "Logs should contain second restart message")
		assert.Contains(t, logs, "Attempting restart 3/3", "Logs should contain third restart message")
	} else {
		t.Log("No logs in response")
	}
}

// TestProcessRestartOnFailureEventualSuccess tests a process that fails a few times then succeeds
func TestProcessRestartOnFailureEventualSuccess(t *testing.T) {
	t.Log("=== Testing process restart on failure with eventual success ===")

	// Create a script that fails twice, then succeeds
	// We'll use a file to track attempts
	setupCmd := "rm -f /tmp/test_restart_counter.txt && echo 0 > /tmp/test_restart_counter.txt"
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command":           setupCmd,
		"waitForCompletion": true,
	})
	require.NoError(t, err)
	resp.Body.Close()

	// Command that fails the first 2 times, then succeeds
	processRequest := map[string]interface{}{
		"name":              "test-restart-eventual-success",
		"command":           "COUNT=$(cat /tmp/test_restart_counter.txt); NEW_COUNT=$((COUNT + 1)); echo $NEW_COUNT > /tmp/test_restart_counter.txt; echo \"Attempt $NEW_COUNT\"; if [ $NEW_COUNT -lt 3 ]; then exit 1; else exit 0; fi",
		"cwd":               "/",
		"waitForCompletion": true,
		"restartOnFailure":  true,
		"maxRestarts":       5,
		"timeout":           15,
	}

	t.Log("Starting process that will fail twice then succeed...")
	resp, err = common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	t.Logf("Response status code: %d", resp.StatusCode)
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Process creation should succeed")

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	// Verify the process eventually succeeded
	require.Contains(t, processResponse, "status", "Response should contain status")
	require.Contains(t, processResponse, "restartCount", "Response should contain restartCount")

	assert.Equal(t, "completed", processResponse["status"], "Process should eventually complete successfully")
	assert.Equal(t, float64(2), processResponse["restartCount"], "Process should have restarted 2 times before succeeding")
	assert.Equal(t, float64(0), processResponse["exitCode"], "Exit code should be 0 for success")

	// Check the logs
	if logs, ok := processResponse["logs"].(string); ok {
		t.Logf("Process logs:\n%s", logs)
		assert.Contains(t, logs, "Attempt 1", "Logs should contain first attempt")
		assert.Contains(t, logs, "Attempt 2", "Logs should contain second attempt")
		assert.Contains(t, logs, "Attempt 3", "Logs should contain third attempt")
		assert.Contains(t, logs, "Attempting restart", "Logs should contain restart messages")
	}
}

// TestProcessRestartPIDStaysTheSame tests that PIDs remain constant across restarts
func TestProcessRestartPIDStaysTheSame(t *testing.T) {
	t.Log("=== Testing PID stability across restarts ===")

	// Use a unique name to avoid conflicts with previous test runs
	processName := fmt.Sprintf("test-pid-stability-%d", time.Now().UnixNano())

	// Start a process without waiting for completion to get the PID
	processRequest := map[string]interface{}{
		"name":              processName,
		"command":           "exit 1", // Will fail immediately
		"cwd":               "/",
		"waitForCompletion": false,
		"restartOnFailure":  true,
		"maxRestarts":       2,
	}

	t.Log("Starting process without waiting for completion...")
	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	originalPID := processResponse["pid"].(string)
	t.Logf("Got PID: %s for process: %s", originalPID, processName)

	// Wait for the process to fail and restart multiple times
	time.Sleep(4 * time.Second)

	// Query the process - it should still have the same PID
	t.Logf("Querying process using PID: %s", originalPID)
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+originalPID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode, "PID should still be accessible")

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	t.Logf("Process details: %+v", processDetails)

	// The process should have completed all restarts
	assert.Equal(t, "failed", processDetails["status"], "Process should be failed after all restarts")
	assert.Equal(t, float64(2), processDetails["restartCount"], "Process should have restarted 2 times")

	// CRITICAL: The PID should remain the same across all restarts
	currentPID := processDetails["pid"].(string)
	assert.Equal(t, originalPID, currentPID, "PID should remain constant across restarts for user transparency")
	t.Logf("✓ PID remained constant: %s", currentPID)

	// List processes and verify it appears once with the same PID
	t.Log("Listing all processes to verify single entry with stable PID...")
	resp, err = common.MakeRequest(http.MethodGet, "/process", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	var processList []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processList)
	require.NoError(t, err)

	// Count how many times our process appears in the list
	count := 0
	for _, p := range processList {
		if p["name"].(string) == processName {
			count++
			assert.Equal(t, originalPID, p["pid"].(string), "Listed process should have the same PID")
		}
	}
	assert.Equal(t, 1, count, "Process should appear exactly once in the list")

	t.Log("✓ PID remains stable across restarts, completely transparent to users")
}

// TestProcessNonExistentWorkingDirectory tests error handling when working directory doesn't exist
func TestProcessNonExistentWorkingDirectory(t *testing.T) {
	t.Run("absolute path non-existent", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command":           "echo test",
			"workingDir":        "/this/folder/does/not/exist",
			"waitForCompletion": true,
		}

		resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should return an error
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

		var errorResponse map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&errorResponse)
		require.NoError(t, err)

		// Verify error message contains the command and folder path
		require.Contains(t, errorResponse, "error")
		errorMsg := errorResponse["error"].(string)
		assert.Contains(t, errorMsg, "could not execute command")
		assert.Contains(t, errorMsg, "echo test")
		assert.Contains(t, errorMsg, "/this/folder/does/not/exist")
		assert.Contains(t, errorMsg, "does not exist")
	})

	t.Run("relative path non-existent", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command":           "ls -la",
			"workingDir":        "nonexistent/relative/folder",
			"waitForCompletion": true,
		}

		resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should return an error
		assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

		var errorResponse map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&errorResponse)
		require.NoError(t, err)

		// Verify error message
		require.Contains(t, errorResponse, "error")
		errorMsg := errorResponse["error"].(string)
		assert.Contains(t, errorMsg, "could not execute command")
		assert.Contains(t, errorMsg, "ls -la")
		assert.Contains(t, errorMsg, "nonexistent/relative/folder")
		assert.Contains(t, errorMsg, "does not exist")
	})

	t.Run("valid working directory still works", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command":           "pwd",
			"workingDir":        "/tmp",
			"waitForCompletion": true,
		}

		resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		// Should succeed
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var processResponse map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&processResponse)
		require.NoError(t, err)

		// Verify process executed successfully
		assert.Equal(t, "completed", processResponse["status"])
		assert.Equal(t, float64(0), processResponse["exitCode"])

		// Verify logs contain /tmp (on Mac it might be /private/tmp)
		logs := processResponse["logs"].(string)
		assert.True(t, strings.Contains(logs, "/tmp"), "Logs should contain /tmp path")
	})
}

// StreamEvent represents a JSON streaming event
type StreamEvent struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

// parseStreamEvents reads newline-delimited JSON events from a response body
func parseStreamEvents(t *testing.T, resp *http.Response) ([]StreamEvent, map[string]interface{}) {
	reader := bufio.NewReader(resp.Body)
	events := []StreamEvent{}
	var result map[string]interface{}

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Logf("Failed to parse event: %s", line)
			continue
		}
		events = append(events, event)

		// If this is the result event, parse the nested JSON
		if event.Type == "result" {
			if err := json.Unmarshal([]byte(event.Data), &result); err != nil {
				t.Logf("Failed to parse result data: %s", event.Data)
			}
			break
		}
	}

	return events, result
}

// TestProcessExecuteWithStreaming tests the streaming execution endpoint
func TestProcessExecuteWithStreaming(t *testing.T) {
	t.Log("=== Testing process execution with streaming ===")

	t.Run("basic streaming execution", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command": "echo 'line1' && echo 'line2' && echo 'line3' && sleep 1 && echo 'line4' && echo 'error line' >&2",
			"cwd":     "/",
		}

		resp, err := makeRequestWithHeaders(http.MethodPost, "/process", processRequest, map[string]string{
			"Accept": "text/event-stream",
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))

		events, result := parseStreamEvents(t, resp)

		// Collect all stdout and stderr data from events
		var allStdoutData string
		var allStderrData string
		for _, event := range events {
			if event.Type == "stdout" {
				allStdoutData += event.Data
			}
			if event.Type == "stderr" {
				allStderrData += event.Data
			}
		}

		// Verify streamed stdout contains all lines (may come in single or multiple events)
		assert.Contains(t, allStdoutData, "line1", "streamed stdout should contain line1")
		assert.Contains(t, allStdoutData, "line2", "streamed stdout should contain line2")
		assert.Contains(t, allStdoutData, "line3", "streamed stdout should contain line3")
		assert.Contains(t, allStdoutData, "line4", "streamed stdout should contain line4")

		// Verify streamed stderr contains error line
		assert.Contains(t, allStderrData, "error line", "streamed stderr should contain error line")

		// Verify we received the result
		require.NotNil(t, result, "should receive result")

		assert.Contains(t, result, "pid")
		assert.Contains(t, result, "status")
		assert.Equal(t, "completed", result["status"])
		assert.Equal(t, float64(0), result["exitCode"])

		// Verify stdout, stderr, and logs are present separately in result
		assert.Contains(t, result, "stdout")
		assert.Contains(t, result, "stderr")
		assert.Contains(t, result, "logs")

		stdout := result["stdout"].(string)
		stderr := result["stderr"].(string)
		logs := result["logs"].(string)

		assert.Contains(t, stdout, "line1")
		assert.Contains(t, stdout, "line4")
		assert.Contains(t, stderr, "error line")
		assert.Contains(t, logs, "line1")
		assert.Contains(t, logs, "error line")
	})

	t.Run("streaming with stderr", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command": "echo 'stdout line' && echo 'stderr line' >&2",
			"cwd":     "/",
		}

		resp, err := makeRequestWithHeaders(http.MethodPost, "/process", processRequest, map[string]string{
			"Accept": "text/event-stream",
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		events, result := parseStreamEvents(t, resp)

		// Check for stdout and stderr events
		hasStdout := false
		hasStderr := false
		for _, event := range events {
			if event.Type == "stdout" && strings.Contains(event.Data, "stdout line") {
				hasStdout = true
			}
			if event.Type == "stderr" && strings.Contains(event.Data, "stderr line") {
				hasStderr = true
			}
		}

		assert.True(t, hasStdout, "should receive stdout event in stream")
		assert.True(t, hasStderr, "should receive stderr event in stream")
		require.NotNil(t, result, "should receive result")

		// Verify result contains separate stdout, stderr, logs
		assert.Contains(t, result, "stdout")
		assert.Contains(t, result, "stderr")
		assert.Contains(t, result, "logs")

		stdout := result["stdout"].(string)
		stderr := result["stderr"].(string)

		assert.Contains(t, stdout, "stdout line")
		assert.Contains(t, stderr, "stderr line")
		assert.NotContains(t, stdout, "stderr line", "stdout should not contain stderr content")
		assert.NotContains(t, stderr, "stdout line", "stderr should not contain stdout content")
	})

	t.Run("streaming with failing process", func(t *testing.T) {
		processRequest := map[string]interface{}{
			"command": "echo 'before fail' && exit 42",
			"cwd":     "/",
		}

		resp, err := makeRequestWithHeaders(http.MethodPost, "/process", processRequest, map[string]string{
			"Accept": "text/event-stream",
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		_, result := parseStreamEvents(t, resp)

		require.NotNil(t, result, "should receive result")

		assert.Equal(t, "failed", result["status"])
		assert.Equal(t, float64(42), result["exitCode"])
	})

	t.Run("streaming with named process", func(t *testing.T) {
		processName := fmt.Sprintf("test-stream-%d", time.Now().UnixNano())
		processRequest := map[string]interface{}{
			"name":    processName,
			"command": "echo 'hello streaming'",
			"cwd":     "/",
		}

		resp, err := makeRequestWithHeaders(http.MethodPost, "/process", processRequest, map[string]string{
			"Accept": "text/event-stream",
		})
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		_, result := parseStreamEvents(t, resp)

		require.NotNil(t, result, "should receive result")

		assert.Equal(t, processName, result["name"])
		assert.Equal(t, "completed", result["status"])
	})

	t.Run("non-streaming request still works", func(t *testing.T) {
		// Verify that regular requests without Accept: text/event-stream still return JSON
		processRequest := map[string]interface{}{
			"command":           "echo 'regular request'",
			"cwd":               "/",
			"waitForCompletion": true,
		}

		resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		// Should return JSON, not streaming
		assert.Contains(t, resp.Header.Get("Content-Type"), "application/json")

		var result map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&result)
		require.NoError(t, err)

		assert.Equal(t, "completed", result["status"])
	})
}

// TestWaitForPorts tests starting a process with waitForPorts and verifying the port is callable
func TestWaitForPorts(t *testing.T) {
	const port = 3010
	processName := "test-waitforports"

	// Kill the process if it already exists
	t.Logf("Cleaning up any existing process named %s", processName)
	resp, _ := common.MakeRequest(http.MethodDelete, "/process/"+processName+"/kill", nil)
	if resp != nil {
		resp.Body.Close()
	}
	time.Sleep(500 * time.Millisecond)

	t.Logf("Starting simple HTTP server on port %d with waitForPorts (server starts after 2s dela)", port)

	// Simple Python HTTP server that starts after 2 seconds and returns "OK" on root endpoint
	command := fmt.Sprintf(`sleep 2 && python3 -c "
from http.server import HTTPServer, BaseHTTPRequestHandler
class H(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-type', 'text/plain')
        self.end_headers()
        self.wfile.write(b'OK')
    def log_message(self, format, *args):
        pass
HTTPServer(('', %d), H).serve_forever()
"`, port)

	processRequest := map[string]interface{}{
		"name":         processName,
		"command":      command,
		"workingDir":   "/",
		"waitForPorts": []int{port},
		"timeout":      30,
	}

	// Use a client with longer timeout since waitForPorts waits for the port
	resp, err := common.MakeRequestWithTimeout(http.MethodPost, "/process", processRequest, 60*time.Second)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processResponse)
	require.NoError(t, err)

	require.Contains(t, processResponse, "pid")
	require.Contains(t, processResponse, "name")
	assert.Equal(t, processName, processResponse["name"])

	t.Logf("Process started with PID: %s, status: %v", processResponse["pid"], processResponse["status"])

	// The endpoint has returned, meaning waitForPorts has completed
	// Now immediately check that localhost:PORT is callable
	t.Logf("Checking if localhost:%d is callable...", port)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	httpResp, err := client.Get(fmt.Sprintf("http://localhost:%d", port))
	require.NoError(t, err, "localhost:%d should be callable immediately after waitForPorts returns", port)
	defer httpResp.Body.Close()

	t.Logf("Got HTTP response status: %d", httpResp.StatusCode)
	assert.Equal(t, http.StatusOK, httpResp.StatusCode, "Expected 200 OK from simple HTTP server")

	// Read response body
	body, err := io.ReadAll(httpResp.Body)
	require.NoError(t, err)
	assert.Equal(t, "OK", string(body), "Expected 'OK' response from simple HTTP server")

	// Cleanup: kill the process
	t.Logf("Cleaning up: killing process %s", processName)
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processName+"/kill", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for cleanup
	time.Sleep(500 * time.Millisecond)

	// Verify process is killed
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "killed", processDetails["status"], "Process should be killed after cleanup")
	t.Log("Test passed: HTTP server started successfully and port was immediately callable after waitForPorts returned")
}

// TestProcessTimeoutWithWaitForCompletion tests that when a process times out with waitForCompletion,
// the API returns an error but the process continues running and can be retrieved
func TestProcessTimeoutWithWaitForCompletion(t *testing.T) {
	t.Log("=== Testing process timeout with waitForCompletion ===")

	processName := fmt.Sprintf("test-timeout-%d", time.Now().UnixNano())

	// Start a long-running process with a short timeout
	processRequest := map[string]interface{}{
		"name":              processName,
		"command":           "sleep 30", // Will run longer than the timeout
		"cwd":               "/",
		"waitForCompletion": true,
		"timeout":           1, // Short timeout to trigger the timeout behavior
	}

	t.Log("Starting process with short timeout...")
	startTime := time.Now()
	resp, err := common.MakeRequestWithTimeout(http.MethodPost, "/process", processRequest, 10*time.Second)
	elapsed := time.Since(startTime)
	require.NoError(t, err)
	defer resp.Body.Close()

	t.Logf("Response received after %v", elapsed)

	// Verify the timeout actually triggered - response should come back in ~3 seconds, not 30
	assert.Less(t, elapsed, 2*time.Second, "Response should return within 4 seconds (timeout is 3s)")
	assert.GreaterOrEqual(t, elapsed, 1*time.Second, "Response should take at least 3 seconds (the timeout duration)")

	// The request should return 422 (Unprocessable Entity) on timeout
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode, "Should return 422 on timeout")

	var errorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	t.Logf("Error response: %+v", errorResponse)

	// Verify error message indicates timeout
	require.Contains(t, errorResponse, "error", "Response should contain error")
	errorMsg := errorResponse["error"].(string)
	assert.Contains(t, errorMsg, "timed out", "Error message should mention timeout")

	// The process should still be running even though the API returned an error
	// We can retrieve it by name
	t.Log("Verifying process is still accessible and running...")
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "running", processDetails["status"], "Process should still be running after timeout error")
	assert.Equal(t, processName, processDetails["name"], "Process name should match")

	pid := processDetails["pid"].(string)
	t.Logf("Process PID: %s, Status: %s", pid, processDetails["status"])

	// Cleanup: kill the process
	t.Log("Cleaning up: killing process...")
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processName+"/kill", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Wait for cleanup
	time.Sleep(500 * time.Millisecond)

	// Verify process is killed
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processName, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	assert.Equal(t, "killed", processDetails["status"], "Process should be killed after cleanup")
	t.Log("✓ Test passed: Process timeout returns error but process continues running and is accessible")
}

// makeRequestWithHeaders makes an HTTP request with custom headers
func makeRequestWithHeaders(method, path string, body interface{}, headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader

	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("error marshaling JSON: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, common.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Use a client with longer timeout for streaming
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	return client.Do(req)
}
