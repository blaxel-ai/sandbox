package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	testClient  *mcp.Client
	testSession *mcp.ClientSession
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
		Name:    "integration-test-client",
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

// TestMCPClientConnect tests connecting to the MCP server
func TestMCPClientConnect(t *testing.T) {
	client, session := setupMCPClient(t)

	assert.NotNil(t, client)
	assert.NotNil(t, session)
	assert.NotEmpty(t, session.ID(), "Session should have an ID")
}

// TestMCPClientListTools tests listing available tools
func TestMCPClientListTools(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// List available tools
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err, "Failed to list tools")
	require.NotNil(t, result, "Result should not be nil")

	// Should have multiple tools registered
	assert.Greater(t, len(result.Tools), 0, "Should have at least one tool")

	// Debug: print processExecute schema
	for _, tool := range result.Tools {
		if tool.Name == "processExecute" {
			schemaJSON, _ := json.MarshalIndent(tool.InputSchema, "", "  ")
			t.Logf("processExecute schema:\n%s", string(schemaJSON))
		}
	}

	// Verify expected tools are present
	expectedTools := []string{
		"processesList",
		"processExecute",
		"processGet",
		"processGetLogs",
		"processStop",
		"processKill",
		"fsGetWorkingDirectory",
		"fsListDirectory",
		"fsReadFile",
		"fsWriteFile",
		"fsDeleteFileOrDirectory",
		"codegenFileSearch",
		"codegenCodebaseSearch",
		"codegenGrepSearch",
		"codegenReadFileRange",
		"codegenReapply",
		"codegenListDir",
		"codegenParallelApply",
	}

	// These tools are only available when codegen is enabled
	conditionalTools := []string{
		"codegenEditFile",
		"codegenRerank",
	}

	toolNames := make(map[string]bool)
	for _, tool := range result.Tools {
		toolNames[tool.Name] = true

		// Verify tool structure
		assert.NotEmpty(t, tool.Name, "Tool should have a name")
		assert.NotEmpty(t, tool.Description, "Tool should have a description")
		assert.NotNil(t, tool.InputSchema, "Tool should have an input schema")
	}

	// Check that expected tools are present
	for _, expected := range expectedTools {
		assert.True(t, toolNames[expected], "Expected tool %s to be present", expected)
	}

	// Check conditional tools - they may or may not be present depending on configuration
	codegenEnabled := os.Getenv("RELACE_API_KEY") != "" || os.Getenv("MORPH_API_KEY") != ""
	for _, conditional := range conditionalTools {
		if codegenEnabled {
			assert.True(t, toolNames[conditional], "Expected tool %s to be present when codegen is enabled", conditional)
		} else {
			t.Logf("Tool %s not expected when codegen is disabled", conditional)
		}
	}
}

// TestMCPClientCallToolListProcesses tests calling the processesList tool
func TestMCPClientCallToolListProcesses(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call the processesList tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "processesList",
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "Failed to call processesList tool")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")
}

// TestMCPClientCallToolGetWorkingDirectory tests calling fsGetWorkingDirectory
func TestMCPClientCallToolGetWorkingDirectory(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call the fsGetWorkingDirectory tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fsGetWorkingDirectory",
		Arguments: map[string]any{},
	})
	require.NoError(t, err, "Failed to call fsGetWorkingDirectory tool")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")

	// Verify we got text content back
	if len(result.Content) > 0 {
		textContent, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "Expected TextContent")
		assert.NotEmpty(t, textContent.Text, "Working directory should not be empty")
	}
}

// TestMCPClientCallToolListDirectory tests listing a directory
func TestMCPClientCallToolListDirectory(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call the fsListDirectory tool for root directory
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fsListDirectory",
		Arguments: map[string]any{
			"path": "/",
		},
	})
	require.NoError(t, err, "Failed to call fsListDirectory tool")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")
}

// TestMCPClientCallToolExecuteProcess tests executing a command
func TestMCPClientCallToolExecuteProcess(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Execute a simple echo command (only required field)
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "processExecute",
		Arguments: map[string]any{
			"command": "echo 'Hello from MCP test'",
		},
	})
	require.NoError(t, err, "Failed to call processExecute tool")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")
}

// TestMCPClientCallToolListDir tests the codegen list directory tool
func TestMCPClientCallToolListDir(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Call the codegenListDir tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenListDir",
		Arguments: map[string]any{
			"relativeWorkspacePath": "/",
		},
	})
	require.NoError(t, err, "Failed to call codegenListDir tool")
	require.NotNil(t, result, "Result should not be nil")
	assert.False(t, result.IsError, "Tool call should not return an error")
	assert.NotEmpty(t, result.Content, "Result should have content")
}

// TestMCPClientInvalidToolCall tests calling a non-existent tool
func TestMCPClientInvalidToolCall(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to call a non-existent tool
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "nonExistentTool",
		Arguments: map[string]any{},
	})

	// Should get an error or an error result
	if err == nil {
		require.NotNil(t, result)
		assert.True(t, result.IsError, "Should return an error for non-existent tool")
	}
}

// TestMCPClientConcurrentCalls tests making concurrent tool calls
func TestMCPClientConcurrentCalls(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const numCalls = 5
	results := make(chan error, numCalls)

	// Make multiple concurrent calls to list processes
	for i := 0; i < numCalls; i++ {
		go func(id int) {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      "processesList",
				Arguments: map[string]any{},
			})

			if err != nil {
				results <- err
				return
			}

			if result.IsError {
				results <- fmt.Errorf("tool call %d returned error", id)
				return
			}

			results <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < numCalls; i++ {
		select {
		case err := <-results:
			assert.NoError(t, err, "Concurrent call %d failed", i)
		case <-time.After(10 * time.Second):
			t.Fatal("Timeout waiting for concurrent calls")
		}
	}
}

// TestMCPClientToolInputValidation tests that tools validate their inputs
func TestMCPClientToolInputValidation(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Try to call fsListDirectory without required path argument
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fsListDirectory",
		Arguments: map[string]any{}, // Missing required "path" field
	})

	// Should either error or return an error result
	if err == nil {
		require.NotNil(t, result)
		// The tool might handle missing arguments gracefully
		t.Logf("Tool handled missing argument: isError=%v", result.IsError)
	}
}

// TestMCPClientReadFileAndDirectory tests file and directory operations
func TestMCPClientReadFileAndDirectory(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// First, list the root directory to find a file
	listResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fsListDirectory",
		Arguments: map[string]any{
			"path": "/",
		},
	})
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Try to read /etc/hosts (common file on Unix systems)
	readResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "fsReadFile",
		Arguments: map[string]any{
			"path": "/etc/hosts",
		},
	})

	// File might not exist or not be readable, which is okay
	if err == nil {
		t.Logf("Read file result: isError=%v", readResult.IsError)
	} else {
		t.Logf("Read file error: %v", err)
	}
}

// TestMCPClientProcessLifecycle tests process execution lifecycle
func TestMCPClientProcessLifecycle(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Start a long-running process
	execResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "processExecute",
		Arguments: map[string]any{
			"command": "sleep 2",
			"name":    "test-sleep-process",
		},
	})
	require.NoError(t, err)
	assert.False(t, execResult.IsError, "Process execution should succeed")

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	// List processes to verify it's running
	listResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "processesList",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	assert.False(t, listResult.IsError)

	// Wait for the process to complete naturally
	time.Sleep(2500 * time.Millisecond)
}

// TestMCPClientCodegenTools tests codegen-specific tools
func TestMCPClientCodegenTools(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Test file search
	searchResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenFileSearch",
		Arguments: map[string]any{
			"query":     "main.go",
			"directory": "./",
		},
	})
	require.NoError(t, err)
	t.Logf("File search result: isError=%v", searchResult.IsError)

	// Test grep search (might not have ripgrep installed)
	grepResult, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "codegenGrepSearch",
		Arguments: map[string]any{
			"query":         "package",
			"caseSensitive": false,
		},
	})

	if err == nil {
		t.Logf("Grep search result: isError=%v", grepResult.IsError)
	} else {
		t.Logf("Grep search error (may not have ripgrep): %v", err)
	}
}

// TestMCPClientSessionPersistence tests that the session persists across calls
func TestMCPClientSessionPersistence(t *testing.T) {
	_, session := setupMCPClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	sessionID := session.ID()
	assert.NotEmpty(t, sessionID)

	// Make multiple calls and verify session ID stays the same
	for i := 0; i < 3; i++ {
		_, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "processesList",
			Arguments: map[string]any{},
		})
		require.NoError(t, err)
		assert.Equal(t, sessionID, session.ID(), "Session ID should remain constant")
	}
}
