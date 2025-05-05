package tests

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/beamlit/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNetworkOperations tests network operations
func TestNetworkOperations(t *testing.T) {
	// First create a process that listens on a port
	// We'll use netcat to listen on a port
	port := 9999

	// Run netcat in the background to listen on the port
	processRequest := map[string]interface{}{
		"command": "nc -l " + strconv.Itoa(port) + " > /dev/null",
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
	require.Contains(t, processResponse, "pid")

	// Convert pid to int
	pidFloat, ok := processResponse["pid"].(float64)
	require.True(t, ok, "PID should be a number")
	pid := int(pidFloat)

	// Give the process time to start
	time.Sleep(2 * time.Second)

	// Get ports for the process
	resp, err = common.MakeRequest(http.MethodGet, "/network/process/"+strconv.Itoa(pid)+"/ports", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// The ports endpoint might return success even if no ports are found
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Start monitoring ports
	monitorRequest := map[string]interface{}{
		"interval": 1,
	}

	resp, err = common.MakeRequest(http.MethodPost, "/network/process/"+strconv.Itoa(pid)+"/monitor", monitorRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Give monitoring time to detect the port
	time.Sleep(2 * time.Second)

	// Stop monitoring
	resp, err = common.MakeRequest(http.MethodDelete, "/network/process/"+strconv.Itoa(pid)+"/monitor", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Stop the process
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
