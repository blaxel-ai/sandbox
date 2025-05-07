package tests

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/beamlit/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestProcessOperations tests process operations
func TestProcessOperations(t *testing.T) {
	// Create a process
	processRequest := map[string]interface{}{
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
	require.Contains(t, processResponse, "id")
	processID := processResponse["id"].(string)

	// Get process details
	resp, err = common.MakeRequest(http.MethodGet, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var processDetails map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&processDetails)
	require.NoError(t, err)

	// Verify process status
	require.Contains(t, processDetails, "status")
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
	require.Contains(t, processResponse, "id")
	processID := processResponse["id"].(string)

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
