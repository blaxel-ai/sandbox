package integration_tests

import (
	"net/http"
	"os"
	"testing"

	"github.com/beamlit/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMain sets up the API server in Docker before running tests
func TestMain(m *testing.M) {
	setup()
	code := m.Run()
	teardown()
	os.Exit(code)
}

// setup starts the API server in Docker
func setup() {
	// Nothing to do here if we're using an external Docker container
	// If we started the container ourselves, we would start it here
}

// teardown cleans up any resources
func teardown() {
	// Nothing to do here if we're using an external Docker container
	// If we started the container ourselves, we would stop it here
}

// TestHealthEndpoint tests the health endpoint
func TestHealthEndpoint(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodGet, "/health", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var response map[string]interface{}
	err = common.ParseJSONResponse(resp, &response)
	require.NoError(t, err)

	assert.Equal(t, "ok", response["status"])
}
