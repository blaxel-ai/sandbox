package tests

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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

	// Verify process information is returned
	require.Contains(t, processResponse, "name")
	processID := processResponse["name"].(string)
	require.Contains(t, processResponse, "pid")

	// Get pid as string
	pidStr := processResponse["pid"].(string)
	require.NotEmpty(t, pidStr, "PID should not be empty")

	// Convert string pid to int for use in API requests
	pid, err := strconv.Atoi(pidStr)
	require.NoError(t, err, "Failed to convert PID from string to int")

	// Give the process time to start
	time.Sleep(2 * time.Second)

	// Get ports for the process
	resp, err = common.MakeRequest(http.MethodGet, "/network/process/"+strconv.Itoa(pid)+"/ports", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Decode the response - it might be an error if netstat is not available
	var portsResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&portsResponse)
	require.NoError(t, err)

	// Check if this is an error response
	if resp.StatusCode != http.StatusOK {
		// If this is an error about netstat or ss not being available, we'll skip this test
		if errorMsg, ok := portsResponse["error"].(string); ok {
			if strings.Contains(errorMsg, "netstat") || strings.Contains(errorMsg, "ss") {
				t.Skip("Skipping test because network tools (netstat/ss) are not available")
			}
		}
		t.Fatalf("Failed to get ports: %v", portsResponse)
	}

	// Validate the ports response structure
	assert.Contains(t, portsResponse, "pid")
	assert.Equal(t, float64(pid), portsResponse["pid"])
	assert.Contains(t, portsResponse, "ports")

	// Start monitoring ports
	monitorRequest := map[string]interface{}{
		"callback": "http://localhost:8080/test-callback",
	}

	resp, err = common.MakeRequest(http.MethodPost, "/network/process/"+strconv.Itoa(pid)+"/monitor", monitorRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Handle error responses for monitor similar to the ports endpoint
	var monitorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&monitorResponse)
	require.NoError(t, err)

	// Check if this is an error response
	if resp.StatusCode != http.StatusOK {
		if errorMsg, ok := monitorResponse["error"].(string); ok {
			if strings.Contains(errorMsg, "netstat") || strings.Contains(errorMsg, "ss") {
				t.Skip("Skipping test because network tools (netstat/ss) are not available")
			}
		}
		t.Fatalf("Failed to start monitoring: %v", monitorResponse)
	}

	// Verify the monitoring response
	assert.Contains(t, monitorResponse, "message")
	assert.Contains(t, monitorResponse["message"], "started")

	// Give monitoring time to detect the port
	time.Sleep(2 * time.Second)

	// Stop monitoring
	resp, err = common.MakeRequest(http.MethodDelete, "/network/process/"+strconv.Itoa(pid)+"/monitor", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Similarly, handle potential errors for the stop monitoring endpoint
	if resp.StatusCode != http.StatusOK {
		var stopMonitorResponse map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&stopMonitorResponse)
		require.NoError(t, err)

		if errorMsg, ok := stopMonitorResponse["error"].(string); ok {
			if strings.Contains(errorMsg, "netstat") || strings.Contains(errorMsg, "ss") {
				t.Skip("Skipping test because network tools (netstat/ss) are not available")
			}
		}
		// For a 404, we'll just log it and continue, as the process might have already been stopped
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("Failed to stop monitoring: %v", stopMonitorResponse)
		}
	} else {
		// Verify the stop monitoring response
		var stopMonitorResponse map[string]interface{}
		err = json.NewDecoder(resp.Body).Decode(&stopMonitorResponse)
		require.NoError(t, err)
		assert.Contains(t, stopMonitorResponse, "message")
		assert.Contains(t, stopMonitorResponse["message"], "stopped")
	}

	// Stop the process
	resp, err = common.MakeRequest(http.MethodDelete, "/process/"+processID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
