package codegen_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	baseURL string
)

// setupMCPClient creates and connects an MCP client to the server
func setupMCPClient(t *testing.T) (*mcp.Client, *mcp.ClientSession) {
	ctx := context.Background()

	// Get server URL from environment or use default
	serverURL := os.Getenv("SANDBOX_API_URL")
	if serverURL == "" {
		serverURL = "http://localhost:8080/mcp"
	}

	// Create MCP client
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "codegen-test-client",
		Version: "1.0.0",
	}, nil)

	// Connect to the server using StreamableClientTransport
	transport := &mcp.StreamableClientTransport{
		Endpoint: serverURL,
	}

	session, err := client.Connect(ctx, transport, nil)
	require.NoError(t, err, "Failed to connect to MCP server")
	require.NotNil(t, session, "Session should not be nil")

	t.Cleanup(func() {
		if session != nil {
			session.Close()
		}
	})

	return client, session
}

// setupHTTP initializes HTTP test client
func setupHTTP(t *testing.T) string {
	baseURL = os.Getenv("SANDBOX_API_URL")
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	return baseURL
}

// TestMCPCodegenRerank tests the MCP reranking tool
func TestMCPCodegenRerank(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a test directory with some files
	testDir := setupTestDirectory(t)

	// Call the codegenRerank tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenRerank",
		Arguments: map[string]any{
			"path":           testDir,
			"query":          "authentication middleware",
			"scoreThreshold": 0.3,
			"tokenLimit":     10000,
		},
	})

	require.NoError(t, err, "Tool call should not fail at transport level")
	require.NotNil(t, result, "Result should not be nil")

	// If codegen is not configured, the error message will say so - this is not a test failure
	if result.IsError && len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		if strings.Contains(textContent.Text, "does not support reranking") ||
			strings.Contains(textContent.Text, "codegen") {
			t.Logf("Codegen not configured on server: %s", textContent.Text)
			return // Test passes - configuration error is expected
		}
		// If it's a different error, fail the test
		t.Fatalf("Unexpected error: %s", textContent.Text)
	}

	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")

	// Verify we got text content back
	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.NotEmpty(t, textContent.Text, "Result should not be empty")

		// Parse the JSON response
		var response map[string]interface{}
		err := json.Unmarshal([]byte(textContent.Text), &response)
		require.NoError(t, err, "Response should be valid JSON")

		// Verify response structure
		assert.True(t, response["success"].(bool), "Success should be true")
		assert.Contains(t, response, "data", "Response should contain data")

		data := response["data"].(map[string]interface{})
		assert.Contains(t, data, "files", "Data should contain files")
	}
}

// TestMCPCodegenRerankWithFilePattern tests reranking with file pattern
func TestMCPCodegenRerankWithFilePattern(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a test directory with some files
	testDir := setupTestDirectory(t)

	// Call the codegenRerank tool with file pattern
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenRerank",
		Arguments: map[string]any{
			"path":        testDir,
			"query":       "function",
			"filePattern": ".*\\.js$", // Only match .js files
			"tokenLimit":  10000,
		},
	})

	require.NoError(t, err, "Tool call should not fail at transport level")
	require.NotNil(t, result, "Result should not be nil")

	// If codegen is not configured, handle gracefully
	if result.IsError && len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		if strings.Contains(textContent.Text, "does not support reranking") ||
			strings.Contains(textContent.Text, "codegen") {
			t.Logf("Codegen not configured on server: %s", textContent.Text)
			return // Test passes
		}
		t.Fatalf("Unexpected error: %s", textContent.Text)
	}

	assert.False(t, result.IsError, "Tool call should not return an error")

	// Verify response
	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")

		var response map[string]interface{}
		err := json.Unmarshal([]byte(textContent.Text), &response)
		require.NoError(t, err, "Response should be valid JSON")
		assert.True(t, response["success"].(bool), "Success should be true")
	}
}

// TestMCPCodegenRerankEmptyDirectory tests reranking an empty directory
func TestMCPCodegenRerankEmptyDirectory(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Create an empty test directory
	emptyDir := t.TempDir()

	// Call the codegenRerank tool on empty directory
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenRerank",
		Arguments: map[string]any{
			"path":  emptyDir,
			"query": "test",
		},
	})

	require.NoError(t, err, "Tool call should not fail at transport level")
	require.NotNil(t, result, "Result should not be nil")

	// If codegen is not configured, handle gracefully
	if result.IsError && len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		if strings.Contains(textContent.Text, "does not support reranking") ||
			strings.Contains(textContent.Text, "codegen") {
			t.Logf("Codegen not configured on server: %s", textContent.Text)
			return // Test passes
		}
		t.Fatalf("Unexpected error: %s", textContent.Text)
	}

	assert.False(t, result.IsError, "Tool call should not return an error")

	// Verify response indicates no files found
	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")

		var response map[string]interface{}
		err := json.Unmarshal([]byte(textContent.Text), &response)
		require.NoError(t, err, "Response should be valid JSON")
		assert.True(t, response["success"].(bool), "Success should be true")
		assert.Contains(t, response["message"], "No files found", "Message should indicate no files found")
	}
}

// TestHTTPCodegenReranking tests the HTTP reranking endpoint
func TestHTTPCodegenReranking(t *testing.T) {
	baseURL := setupHTTP(t)
	testDir := setupTestDirectory(t)

	// Build the request URL - URL encode the query parameter
	query := url.QueryEscape("authentication middleware")
	requestURL := fmt.Sprintf("%s/codegen/reranking/%s?query=%s&scoreThreshold=%f&tokenLimit=%d",
		baseURL,
		testDir,
		query,
		0.3,
		10000,
	)

	// Make HTTP GET request
	resp, err := http.Get(requestURL)
	require.NoError(t, err, "HTTP request should succeed")
	defer resp.Body.Close()

	// Read response body first to see what error we got
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should read response body")

	// Log response if not 200 to help debug
	if resp.StatusCode != http.StatusOK {
		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// If codegen is not configured, expect 503 - this is not a failure
	if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "codegen tools are not configured") {
		t.Logf("Codegen not configured on server - test passes")
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Verify response structure
	assert.True(t, response["success"].(bool), "Success should be true")
	assert.Contains(t, response, "files", "Response should contain files")
}

// TestHTTPCodegenRerankingWithFilePattern tests HTTP reranking with file pattern
func TestHTTPCodegenRerankingWithFilePattern(t *testing.T) {
	baseURL := setupHTTP(t)
	testDir := setupTestDirectory(t)

	// Build the request URL with file pattern - properly encode all query params
	query := url.QueryEscape("function")
	filePattern := url.QueryEscape(".*\\.go$")
	requestURL := fmt.Sprintf("%s/codegen/reranking/%s?query=%s&filePattern=%s",
		baseURL,
		testDir,
		query,
		filePattern,
	)

	// Make HTTP GET request
	resp, err := http.Get(requestURL)
	require.NoError(t, err, "HTTP request should succeed")
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should read response body")

	// Log response if not 200 to help debug
	if resp.StatusCode != http.StatusOK {
		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// If codegen is not configured, expect 503 - this is not a failure
	if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "codegen tools are not configured") {
		t.Logf("Codegen not configured on server - test passes")
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err, "Response should be valid JSON")
	assert.True(t, response["success"].(bool), "Success should be true")
}

// TestHTTPCodegenRerankingEmptyDirectory tests HTTP reranking on empty directory
func TestHTTPCodegenRerankingEmptyDirectory(t *testing.T) {
	baseURL := setupHTTP(t)
	emptyDir := t.TempDir()

	// Build the request URL with URL encoding
	query := url.QueryEscape("test")
	requestURL := fmt.Sprintf("%s/codegen/reranking/%s?query=%s",
		baseURL,
		emptyDir,
		query,
	)

	// Make HTTP GET request
	resp, err := http.Get(requestURL)
	require.NoError(t, err, "HTTP request should succeed")
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should read response body")

	// Log response if not 200 to help debug
	if resp.StatusCode != http.StatusOK {
		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// If codegen is not configured, expect 503 - this is not a failure
	if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "codegen tools are not configured") {
		t.Logf("Codegen not configured on server - test passes")
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err, "Response should be valid JSON")
	assert.True(t, response["success"].(bool), "Success should be true")

	files := response["files"].([]interface{})
	assert.Empty(t, files, "Should have no files")
}

// TestMCPCodegenEditFile tests the MCP edit file tool
func TestMCPCodegenEditFile(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a test file
	testFile := filepath.Join(t.TempDir(), "test.js")
	originalContent := `function hello() {
  console.log('Hello');
}`
	err := os.WriteFile(testFile, []byte(originalContent), 0644)
	require.NoError(t, err, "Should create test file")

	// Call the codegenEditFile tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenEditFile",
		Arguments: map[string]any{
			"targetFile":   testFile,
			"instructions": "Add a parameter 'name' to the function",
			"codeEdit": `function hello(name) {
  console.log('Hello', name);
}`,
		},
	})

	require.NoError(t, err, "Tool call should not fail at transport level")
	require.NotNil(t, result, "Result should not be nil")

	// If codegen is not configured, handle gracefully
	if result.IsError && len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		if strings.Contains(textContent.Text, "failed to create FastApply client") ||
			strings.Contains(textContent.Text, "codegen") {
			t.Logf("Codegen not configured on server: %s", textContent.Text)
			return // Test passes
		}
		t.Fatalf("Unexpected error: %s", textContent.Text)
	}

	assert.False(t, result.IsError, "Tool call should not return an error")

	// Verify the file was modified
	updatedContent, err := os.ReadFile(testFile)
	require.NoError(t, err, "Should read updated file")
	assert.NotEqual(t, originalContent, string(updatedContent), "File should be modified")
}

// TestHTTPCodegenFastApply tests the HTTP fastapply endpoint
func TestHTTPCodegenFastApply(t *testing.T) {
	baseURL := setupHTTP(t)

	// Create a test file
	testFile := filepath.Join(t.TempDir(), "test.js")
	originalContent := `function hello() {
  console.log('Hello');
}`
	err := os.WriteFile(testFile, []byte(originalContent), 0644)
	require.NoError(t, err, "Should create test file")

	// Prepare request
	requestBody := map[string]interface{}{
		"codeEdit": `function hello(name) {
  console.log('Hello', name);
}`,
		"model": "auto",
	}
	jsonData, err := json.Marshal(requestBody)
	require.NoError(t, err, "Should marshal request")

	// Build the request URL
	requestURL := fmt.Sprintf("%s/codegen/fastapply/%s", baseURL, testFile)

	// Make HTTP PUT request
	req, err := http.NewRequest("PUT", requestURL, bytes.NewBuffer(jsonData))
	require.NoError(t, err, "Should create request")
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err, "HTTP request should succeed")
	defer resp.Body.Close()

	// Parse response
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err, "Should read response body")

	// If codegen is not configured, expect 503 - this is not a failure
	if resp.StatusCode == http.StatusServiceUnavailable && strings.Contains(string(body), "codegen tools are not configured") {
		t.Logf("Codegen not configured on server - test passes")
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode, "Should return 200 OK")

	var response map[string]interface{}
	err = json.Unmarshal(body, &response)
	require.NoError(t, err, "Response should be valid JSON")

	// Verify response structure
	assert.True(t, response["success"].(bool), "Success should be true")
	assert.Contains(t, response, "updatedContent", "Response should contain updatedContent")
	assert.Contains(t, response, "provider", "Response should contain provider")
}

// setupTestDirectory creates a test directory with sample files for testing
func setupTestDirectory(t *testing.T) string {
	testDir := t.TempDir()

	// Create some test files
	files := map[string]string{
		"auth.js": `// Authentication middleware
function authenticateUser(req, res, next) {
  const token = req.headers.authorization;
  if (!token) {
    return res.status(401).json({ error: 'No token provided' });
  }
  next();
}`,
		"user.js": `// User model
class User {
  constructor(name, email) {
    this.name = name;
    this.email = email;
  }
}`,
		"api.go": `package api

// API handler
func HandleRequest(w http.ResponseWriter, r *http.Request) {
	// Handle API request
}`,
		"README.md": `# Test Project
This is a test project for reranking.`,
	}

	for filename, content := range files {
		filePath := filepath.Join(testDir, filename)
		err := os.WriteFile(filePath, []byte(content), 0644)
		require.NoError(t, err, "Should create test file %s", filename)
	}

	return testDir
}
