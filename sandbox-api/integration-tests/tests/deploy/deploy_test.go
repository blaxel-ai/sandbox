//go:build deploy

// Deploy tests are excluded from the default integration test run because they take a long time.
// To run deploy tests, use: go test -tags deploy -v ./tests/deploy/...

package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DeployStatusEvent represents a streaming event from the deploy endpoint
type DeployStatusEvent struct {
	Type    string          `json:"type"`
	Status  string          `json:"status,omitempty"`
	Message string          `json:"message,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// DeployResult represents the final result of a deployment
type DeployResult struct {
	Status   string          `json:"status"`
	Logs     []string        `json:"logs"`
	Error    string          `json:"error,omitempty"`
	Metadata *DeployMetadata `json:"metadata,omitempty"`
}

// DeployMetadata contains deployment metadata
type DeployMetadata struct {
	URL            string `json:"url,omitempty"`
	Name           string `json:"name"`
	Type           string `json:"type"`
	Workspace      string `json:"workspace"`
	CallbackSecret string `json:"callbackSecret,omitempty"`
}

// getDeployCredentials returns deployment credentials from environment variables
// Returns apiKey, workspace, and a boolean indicating if credentials are available
func getDeployCredentials() (apiKey, workspace string, ok bool) {
	apiKey = os.Getenv("BL_API_KEY")
	workspace = os.Getenv("BL_WORKSPACE")

	if apiKey == "" || workspace == "" {
		return "", "", false
	}
	return apiKey, workspace, true
}

// createTestTypeScriptApp creates a temporary directory with a simple TypeScript app
func createTestTypeScriptApp(t *testing.T) string {
	// Create a temporary directory
	tmpDir, err := os.MkdirTemp("", "deploy-test-*")
	require.NoError(t, err)

	// Create package.json
	packageJSON := `{
  "name": "deploy-test-agent",
  "version": "1.0.0",
  "description": "Test agent for deploy integration test",
  "main": "index.js",
  "scripts": {
    "start": "node index.js"
  },
  "dependencies": {}
}
`
	err = os.WriteFile(filepath.Join(tmpDir, "package.json"), []byte(packageJSON), 0644)
	require.NoError(t, err)

	// Create index.js - a simple HTTP server using Node.js built-in http module
	indexJS := `const http = require('http');
const url = require('url');

const PORT = process.env.PORT || 8080;
const HOST = process.env.HOST || '0.0.0.0';

const server = http.createServer((req, res) => {
  const timestamp = new Date().toISOString();
  const startTime = Date.now();

  // Parse the URL to get pathname without query string
  const parsedUrl = url.parse(req.url, true);
  const pathname = parsedUrl.pathname;

  // Log incoming request
  console.log('[' + timestamp + '] [REQUEST] ' + req.method + ' ' + req.url + ' (pathname: ' + pathname + ')');
  console.log('  Headers: ' + JSON.stringify(req.headers));

  // Capture response to log
  const originalEnd = res.end.bind(res);
  res.end = function(...args) {
    const duration = Date.now() - startTime;
    console.log('[' + new Date().toISOString() + '] [RESPONSE] ' + req.method + ' ' + req.url + ' - Status: ' + res.statusCode + ' (took ' + duration + 'ms)');
    return originalEnd(...args);
  };

  // Route based on pathname, not full URL
  if (pathname === '/') {
    res.writeHead(200, { 'Content-Type': 'text/plain' });
    res.end('Hello World!');
  } else {
    res.writeHead(404, { 'Content-Type': 'text/plain' });
    res.end('Not Found');
  }
});

server.listen(PORT, HOST, () => {
  console.log('[' + new Date().toISOString() + '] Server running on http://' + HOST + ':' + PORT);
});
`
	err = os.WriteFile(filepath.Join(tmpDir, "index.js"), []byte(indexJS), 0644)
	require.NoError(t, err)

	return tmpDir
}

// makeDeployRequest makes a streaming request to the deploy endpoint
func makeDeployRequest(deployRequest map[string]interface{}) (*http.Response, error) {
	jsonData, err := json.Marshal(deployRequest)
	if err != nil {
		return nil, fmt.Errorf("error marshaling JSON: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, common.BaseURL+"/deploy", bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/x-ndjson")

	// Use a client with very long timeout for deployments (can take 10+ minutes)
	client := &http.Client{
		Timeout: 20 * time.Minute,
	}

	return client.Do(req)
}

// parseDeployEvents reads and parses all events from a deploy response
func parseDeployEvents(t *testing.T, resp *http.Response) ([]DeployStatusEvent, *DeployResult) {
	reader := bufio.NewReader(resp.Body)
	events := []DeployStatusEvent{}
	var result *DeployResult

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				t.Logf("Error reading response: %v", err)
			}
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var event DeployStatusEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Logf("Failed to parse event: %s (error: %v)", line, err)
			continue
		}
		events = append(events, event)

		// Log the event
		switch event.Type {
		case "status":
			t.Logf("[STATUS] %s: %s", event.Status, event.Message)
		case "log":
			t.Logf("[LOG] %s", event.Message)
		case "error":
			t.Logf("[ERROR] %s", event.Message)
		case "result":
			t.Log("[RESULT] Received final result")
			// Parse the result data
			var deployResult DeployResult
			if err := json.Unmarshal(event.Data, &deployResult); err != nil {
				t.Logf("Failed to parse result data: %v", err)
			} else {
				result = &deployResult
			}
		}
	}

	return events, result
}

// TestDeployTypeScriptAgent tests deploying a simple TypeScript agent
func TestDeployTypeScriptAgent(t *testing.T) {
	// Get credentials from environment
	apiKey, workspace, ok := getDeployCredentials()
	if !ok {
		t.Skip("Skipping deploy test: BL_API_KEY and BL_WORKSPACE environment variables required")
	}

	t.Log("=== Testing TypeScript agent deployment ===")

	// Create the test app
	tmpDir := createTestTypeScriptApp(t)
	defer os.RemoveAll(tmpDir)
	t.Logf("Created test app in: %s", tmpDir)

	// List files in the directory for debugging
	files, _ := os.ReadDir(tmpDir)
	for _, f := range files {
		t.Logf("  - %s", f.Name())
	}

	// Generate a unique name for the test agent
	agentName := fmt.Sprintf("deploy-test-%d", time.Now().Unix())

	// Create deploy request
	deployRequest := map[string]interface{}{
		"authMethod": "apikey",
		"apiKey":     apiKey,
		"workspace":  workspace,
		"name":       agentName,
		"type":       "agent",
		"directory":  tmpDir,
		"public":     true,
		"runtime": map[string]interface{}{
			"memory": 512,
		},
	}

	t.Logf("Starting deployment of agent: %s", agentName)

	// Make the deploy request
	resp, err := makeDeployRequest(deployRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response headers
	assert.Equal(t, http.StatusOK, resp.StatusCode, "Deploy request should return 200 OK")
	assert.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"), "Response should be NDJSON")

	// Parse all events
	events, result := parseDeployEvents(t, resp)

	// Verify we received events
	assert.NotEmpty(t, events, "Should receive at least some events")

	// Log summary of events
	statusEvents := 0
	logEvents := 0
	errorEvents := 0
	for _, e := range events {
		switch e.Type {
		case "status":
			statusEvents++
		case "log":
			logEvents++
		case "error":
			errorEvents++
		}
	}
	t.Logf("Received %d status events, %d log events, %d error events", statusEvents, logEvents, errorEvents)

	// Verify we got a result
	require.NotNil(t, result, "Should receive a final result")

	t.Logf("Deployment result: status=%s", result.Status)
	if result.Error != "" {
		t.Logf("Deployment error: %s", result.Error)
	}
	if result.Metadata != nil {
		t.Logf("Deployment URL: %s", result.Metadata.URL)
	}

	// Check if deployment succeeded
	if result.Status == "success" {
		t.Log("Deployment succeeded!")

		// Verify metadata
		require.NotNil(t, result.Metadata, "Successful deployment should have metadata")
		assert.Equal(t, agentName, result.Metadata.Name, "Metadata name should match")
		assert.Equal(t, "agent", result.Metadata.Type, "Metadata type should be agent")
		assert.Equal(t, workspace, result.Metadata.Workspace, "Metadata workspace should match")
		assert.NotEmpty(t, result.Metadata.URL, "Metadata should have URL")

		t.Logf("Agent deployed successfully!")
		t.Logf("  Name: %s", result.Metadata.Name)
		t.Logf("  URL: %s", result.Metadata.URL)
		if result.Metadata.CallbackSecret != "" {
			t.Logf("  Callback Secret: %s", result.Metadata.CallbackSecret)
		}

		// Test the deployed agent by making a request to it
		t.Log("Testing deployed agent...")
		agentURL := result.Metadata.URL
		t.Logf("Making request to: %s", agentURL)

		// Create request to the deployed agent
		agentReq, err := http.NewRequest(http.MethodGet, agentURL, nil)
		require.NoError(t, err, "Should create request to agent")
		agentReq.Header.Set("Authorization", "Bearer "+apiKey)

		agentClient := &http.Client{Timeout: 30 * time.Second}
		agentResp, err := agentClient.Do(agentReq)
		require.NoError(t, err, "Should be able to call deployed agent")
		defer agentResp.Body.Close()

		t.Logf("Agent response status: %d", agentResp.StatusCode)

		// Read response body
		agentBody, err := io.ReadAll(agentResp.Body)
		require.NoError(t, err, "Should read agent response body")
		t.Logf("Agent response body: %s", string(agentBody))

		// Verify the response
		assert.Equal(t, http.StatusOK, agentResp.StatusCode, "Agent should return 200 OK")
		assert.Equal(t, "Hello World!", string(agentBody), "Agent should return 'Hello World!'")
	} else {
		// Log failure details for debugging
		t.Logf("Deployment failed with status: %s", result.Status)
		t.Logf("Error: %s", result.Error)
		t.Logf("Logs (%d entries):", len(result.Logs))
		for i, log := range result.Logs {
			t.Logf("  [%d] %s", i, log)
		}

		// Don't fail the test on deployment failure if it's an infrastructure issue
		// (e.g., quota exceeded, region unavailable, etc.)
		if strings.Contains(result.Error, "quota") ||
			strings.Contains(result.Error, "unavailable") ||
			strings.Contains(result.Error, "rate limit") {
			t.Skipf("Skipping due to infrastructure limitation: %s", result.Error)
		}

		// For actual code/config issues, fail the test
		t.Errorf("Deployment failed: %s", result.Error)
	}
}

// TestDeployWithClientCredentials tests deploying with client credentials auth
func TestDeployWithClientCredentials(t *testing.T) {
	clientID := os.Getenv("BL_CLIENT_ID")
	clientSecret := os.Getenv("BL_CLIENT_SECRET")
	workspace := os.Getenv("BL_WORKSPACE")

	if clientID == "" || clientSecret == "" || workspace == "" {
		t.Skip("Skipping client credentials deploy test: BL_CLIENT_ID, BL_CLIENT_SECRET, and BL_WORKSPACE environment variables required")
	}

	t.Log("=== Testing deployment with client credentials ===")

	// Create the test app
	tmpDir := createTestTypeScriptApp(t)
	defer os.RemoveAll(tmpDir)

	agentName := fmt.Sprintf("deploy-cc-test-%d", time.Now().Unix())

	deployRequest := map[string]interface{}{
		"authMethod":   "client_credentials",
		"clientId":     clientID,
		"clientSecret": clientSecret,
		"workspace":    workspace,
		"name":         agentName,
		"type":         "agent",
		"directory":    tmpDir,
	}

	t.Logf("Starting deployment with client credentials: %s", agentName)

	resp, err := makeDeployRequest(deployRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	events, result := parseDeployEvents(t, resp)
	assert.NotEmpty(t, events)
	require.NotNil(t, result)

	t.Logf("Deployment result: status=%s", result.Status)
}

// TestDeployValidation tests that the deploy endpoint validates requests properly
func TestDeployValidation(t *testing.T) {
	t.Run("missing workspace", func(t *testing.T) {
		deployRequest := map[string]interface{}{
			"authMethod": "apikey",
			"apiKey":     "test-key",
			// Missing workspace
			"name":      "test-agent",
			"directory": "/tmp",
		}

		resp, err := makeDeployRequest(deployRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("missing name", func(t *testing.T) {
		deployRequest := map[string]interface{}{
			"authMethod": "apikey",
			"apiKey":     "test-key",
			"workspace":  "test-workspace",
			// Missing name
			"directory": "/tmp",
		}

		resp, err := makeDeployRequest(deployRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("invalid directory", func(t *testing.T) {
		deployRequest := map[string]interface{}{
			"authMethod": "apikey",
			"apiKey":     "test-key",
			"workspace":  "test-workspace",
			"name":       "test-agent",
			"directory":  "/nonexistent/path/that/does/not/exist",
		}

		resp, err := makeDeployRequest(deployRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	t.Run("missing api key for apikey auth", func(t *testing.T) {
		tmpDir, _ := os.MkdirTemp("", "deploy-test-*")
		defer os.RemoveAll(tmpDir)

		deployRequest := map[string]interface{}{
			"authMethod": "apikey",
			// Missing apiKey
			"workspace": "test-workspace",
			"name":      "test-agent",
			"directory": tmpDir,
		}

		resp, err := makeDeployRequest(deployRequest)
		require.NoError(t, err)
		defer resp.Body.Close()

		// The request should succeed initially (200 OK) but stream an error event
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		_, result := parseDeployEvents(t, resp)
		require.NotNil(t, result)
		assert.Equal(t, "failed", result.Status)
		assert.Contains(t, result.Error, "API key is required")
	})
}

// TestDeployStatusTransitions tests that the deploy endpoint sends proper status transitions
func TestDeployStatusTransitions(t *testing.T) {
	apiKey, workspace, ok := getDeployCredentials()
	if !ok {
		t.Skip("Skipping deploy status test: BL_API_KEY and BL_WORKSPACE environment variables required")
	}

	t.Log("=== Testing deployment status transitions ===")

	tmpDir := createTestTypeScriptApp(t)
	defer os.RemoveAll(tmpDir)

	agentName := fmt.Sprintf("deploy-status-test-%d", time.Now().Unix())

	deployRequest := map[string]interface{}{
		"authMethod": "apikey",
		"apiKey":     apiKey,
		"workspace":  workspace,
		"name":       agentName,
		"type":       "agent",
		"directory":  tmpDir,
	}

	resp, err := makeDeployRequest(deployRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	events, result := parseDeployEvents(t, resp)

	// Verify status transitions
	statusSequence := []string{}
	for _, e := range events {
		if e.Type == "status" && e.Status != "" {
			statusSequence = append(statusSequence, e.Status)
		}
	}

	t.Logf("Status sequence: %v", statusSequence)

	// Verify we have expected status transitions
	assert.NotEmpty(t, statusSequence, "Should have at least some status events")

	// First status should be "starting"
	if len(statusSequence) > 0 {
		assert.Equal(t, "starting", statusSequence[0], "First status should be 'starting'")
	}

	// Should have authentication status
	hasAuthenticated := false
	for _, s := range statusSequence {
		if s == "authenticated" {
			hasAuthenticated = true
			break
		}
	}
	assert.True(t, hasAuthenticated, "Should have 'authenticated' status")

	// Should have compressing status
	hasCompressing := false
	for _, s := range statusSequence {
		if s == "compressing" {
			hasCompressing = true
			break
		}
	}
	assert.True(t, hasCompressing, "Should have 'compressing' status")

	require.NotNil(t, result, "Should receive final result")
}
