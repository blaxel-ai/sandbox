package tests

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"strings"
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
