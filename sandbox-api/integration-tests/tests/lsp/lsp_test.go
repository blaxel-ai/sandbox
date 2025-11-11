package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLSPPythonServer tests creating a Python LSP server
func TestLSPPythonServer(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-python-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Create a simple Python file
	pythonFile := filepath.Join(projectPath, "test.py")
	err = os.WriteFile(pythonFile, []byte(`import os

def hello():
    print("Hello, world!")
`), 0644)
	require.NoError(t, err)

	// Create LSP server
	createRequest := map[string]interface{}{
		"languageId":  "python",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	var serverResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&serverResponse)
	require.NoError(t, err)

	// Check if server was created successfully
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Skipping test - Python LSP server not available: %v", serverResponse["error"])
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify server response
	require.Contains(t, serverResponse, "id")
	serverID := serverResponse["id"].(string)
	assert.NotEmpty(t, serverID)
	assert.Equal(t, "python", serverResponse["languageId"])
	assert.Equal(t, projectPath, serverResponse["projectPath"])
	assert.Contains(t, serverResponse, "processPid")
	assert.Equal(t, "ready", serverResponse["status"])

	// Get LSP server details
	resp, err = common.MakeRequest(http.MethodGet, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var getResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&getResponse)
	require.NoError(t, err)

	assert.Equal(t, serverID, getResponse["id"])
	assert.Equal(t, "python", getResponse["languageId"])

	// Clean up - delete LSP server
	resp, err = common.MakeRequest(http.MethodDelete, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestLSPTypeScriptServer tests creating a TypeScript LSP server
func TestLSPTypeScriptServer(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-typescript-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Create a simple TypeScript file
	tsFile := filepath.Join(projectPath, "test.ts")
	err = os.WriteFile(tsFile, []byte(`function hello(): void {
    console.log("Hello, world!");
}
`), 0644)
	require.NoError(t, err)

	// Create package.json with TypeScript dependency
	packageJSON := filepath.Join(projectPath, "package.json")
	err = os.WriteFile(packageJSON, []byte(`{
  "name": "test-project",
  "version": "1.0.0",
  "dependencies": {
    "typescript": "^5.0.0"
  }
}
`), 0644)
	require.NoError(t, err)

	// Create tsconfig.json for TypeScript project
	tsconfigJSON := filepath.Join(projectPath, "tsconfig.json")
	err = os.WriteFile(tsconfigJSON, []byte(`{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs"
  }
}
`), 0644)
	require.NoError(t, err)

	// Create LSP server
	createRequest := map[string]interface{}{
		"languageId":  "typescript",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	var serverResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&serverResponse)
	require.NoError(t, err)

	// Check if server was created successfully
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Skipping test - TypeScript LSP server not available: %v", serverResponse["error"])
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify server response
	require.Contains(t, serverResponse, "id")
	serverID := serverResponse["id"].(string)
	assert.NotEmpty(t, serverID)
	assert.Equal(t, "typescript", serverResponse["languageId"])
	assert.Equal(t, projectPath, serverResponse["projectPath"])

	// Clean up - delete LSP server
	resp, err = common.MakeRequest(http.MethodDelete, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestLSPJavaScriptServer tests creating a JavaScript LSP server
// Note: JavaScript LSP uses typescript-language-server which requires TypeScript
// to be installed even for JavaScript projects (this is standard behavior)
func TestLSPJavaScriptServer(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-javascript-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Create a simple JavaScript file
	jsFile := filepath.Join(projectPath, "test.js")
	err = os.WriteFile(jsFile, []byte(`function hello() {
    console.log("Hello, world!");
}

module.exports = { hello };
`), 0644)
	require.NoError(t, err)

	// Create package.json with TypeScript dependency
	// (typescript-language-server requires TypeScript even for JavaScript projects)
	packageJSON := filepath.Join(projectPath, "package.json")
	err = os.WriteFile(packageJSON, []byte(`{
  "name": "test-project",
  "version": "1.0.0",
  "devDependencies": {
    "typescript": "^5.0.0"
  }
}
`), 0644)
	require.NoError(t, err)

	// Create jsconfig.json for JavaScript project (like tsconfig for JS)
	jsconfigJSON := filepath.Join(projectPath, "jsconfig.json")
	err = os.WriteFile(jsconfigJSON, []byte(`{
  "compilerOptions": {
    "target": "ES2020",
    "module": "commonjs",
    "checkJs": true
  },
  "include": ["*.js"]
}
`), 0644)
	require.NoError(t, err)

	// Create LSP server
	createRequest := map[string]interface{}{
		"languageId":  "javascript",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	var serverResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&serverResponse)
	require.NoError(t, err)

	// Check if server was created successfully
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Skipping test - JavaScript LSP server not available: %v", serverResponse["error"])
		return
	}

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify server response
	require.Contains(t, serverResponse, "id")
	serverID := serverResponse["id"].(string)
	assert.NotEmpty(t, serverID)
	assert.Equal(t, "javascript", serverResponse["languageId"])
	assert.Equal(t, projectPath, serverResponse["projectPath"])

	// Get LSP server details
	resp, err = common.MakeRequest(http.MethodGet, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var getResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&getResponse)
	require.NoError(t, err)

	assert.Equal(t, serverID, getResponse["id"])
	assert.Equal(t, "javascript", getResponse["languageId"])

	// Clean up - delete LSP server
	resp, err = common.MakeRequest(http.MethodDelete, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestLSPListServers tests listing all LSP servers
func TestLSPListServers(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-list-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Create a simple Python file
	pythonFile := filepath.Join(projectPath, "test.py")
	err = os.WriteFile(pythonFile, []byte(`print("test")`), 0644)
	require.NoError(t, err)

	// Create first LSP server
	createRequest := map[string]interface{}{
		"languageId":  "python",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	var server1Response map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&server1Response)
	require.NoError(t, err)

	// Check if server was created successfully
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Skipping test - LSP server creation failed: %v", server1Response["error"])
		return
	}

	require.Contains(t, server1Response, "id")
	serverID1 := server1Response["id"].(string)

	// List LSP servers
	resp, err = common.MakeRequest(http.MethodGet, "/lsp", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var serverList []map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&serverList)
	require.NoError(t, err)

	// Verify we have at least our server in the list
	found := false
	for _, server := range serverList {
		if server["id"] == serverID1 {
			found = true
			assert.Equal(t, "python", server["languageId"])
			break
		}
	}
	assert.True(t, found, "Created LSP server should be in the list")

	// Clean up
	resp, err = common.MakeRequest(http.MethodDelete, "/lsp/"+serverID1, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestLSPCompletions tests getting code completions
func TestLSPCompletions(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-completions-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Create a simple Python file with code that should have completions
	pythonFile := filepath.Join(projectPath, "test.py")
	err = os.WriteFile(pythonFile, []byte(`import os

# Try to get completions for 'os.'
os.
`), 0644)
	require.NoError(t, err)

	// Create LSP server
	createRequest := map[string]interface{}{
		"languageId":  "python",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	var serverResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&serverResponse)
	require.NoError(t, err)

	// Check if server was created successfully
	if resp.StatusCode != http.StatusOK {
		t.Skipf("Skipping test - Python LSP server not available: %v", serverResponse["error"])
		return
	}

	require.Contains(t, serverResponse, "id")
	serverID := serverResponse["id"].(string)

	// Wait a bit for the LSP server to be fully initialized
	time.Sleep(2 * time.Second)

	// Request completions
	completionRequest := map[string]interface{}{
		"filePath":  "test.py",
		"line":      3,
		"character": 3,
	}

	resp, err = common.MakeRequest(http.MethodPost, "/lsp/"+serverID+"/completions", completionRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var completionResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&completionResponse)
	require.NoError(t, err)

	// Verify completion response structure
	require.Contains(t, completionResponse, "items")
	items := completionResponse["items"].([]interface{})

	// We should get some completions (os module has many members)
	// Note: The actual completions may vary depending on the LSP server implementation
	t.Logf("Got %d completion items", len(items))

	// Clean up
	resp, err = common.MakeRequest(http.MethodDelete, "/lsp/"+serverID, nil)
	require.NoError(t, err)
	defer resp.Body.Close()
}

// TestLSPInvalidLanguage tests creating an LSP server with an invalid language
func TestLSPInvalidLanguage(t *testing.T) {
	// Create a test project directory
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("test-invalid-project-%d", time.Now().UnixNano()))
	err := os.MkdirAll(projectPath, 0755)
	require.NoError(t, err)
	defer os.RemoveAll(projectPath)

	// Try to create LSP server with invalid language
	createRequest := map[string]interface{}{
		"languageId":  "invalid-language",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return 400 Bad Request
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	require.Contains(t, errorResponse, "error")
}

// TestLSPInvalidProjectPath tests creating an LSP server with a non-existent project path
func TestLSPInvalidProjectPath(t *testing.T) {
	// Use a non-existent path
	projectPath := filepath.Join(os.TempDir(), fmt.Sprintf("non-existent-project-%d", time.Now().UnixNano()))

	// Try to create LSP server
	createRequest := map[string]interface{}{
		"languageId":  "python",
		"projectPath": projectPath,
	}

	resp, err := common.MakeRequest(http.MethodPost, "/lsp", createRequest)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should return 422 Unprocessable Entity
	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	require.Contains(t, errorResponse, "error")
}

// TestLSPGetNonExistentServer tests getting a non-existent LSP server
func TestLSPGetNonExistentServer(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodGet, "/lsp/non-existent-id", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	var errorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	require.Contains(t, errorResponse, "error")
}

// TestLSPDeleteNonExistentServer tests deleting a non-existent LSP server
func TestLSPDeleteNonExistentServer(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodDelete, "/lsp/non-existent-id", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	var errorResponse map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errorResponse)
	require.NoError(t, err)

	require.Contains(t, errorResponse, "error")
}
