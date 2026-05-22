package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// editFileOutput is the structured payload returned by the fsEditFile tool.
// Defined locally so tests decode it without importing the server package.
type editFileOutput struct {
	Path                string `json:"path"`
	OccurrencesReplaced int    `json:"occurrencesReplaced"`
}

// uniqueTestPath returns a /tmp path unique to this test invocation.
func uniqueTestPath(prefix string) string {
	return fmt.Sprintf("/tmp/fs-edit-%s-%d", prefix, time.Now().UnixNano())
}

// writeFileMCP creates a file via the fsWriteFile MCP tool.
// permissions may be empty (server default applies) or an octal string like "640".
func writeFileMCP(t *testing.T, session *mcp.ClientSession, path, content, permissions string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	args := map[string]any{
		"path":    path,
		"content": content,
	}
	if permissions != "" {
		args["permissions"] = permissions
	}

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fsWriteFile",
		Arguments: args,
	})
	require.NoError(t, err, "fsWriteFile transport error")
	require.False(t, result.IsError, "fsWriteFile tool error: %s", textOf(result))
}

// readFileMCP returns the current content of a file via the fsReadFile MCP tool.
func readFileMCP(t *testing.T, session *mcp.ClientSession, path string) (content string, permissions string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fsReadFile",
		Arguments: map[string]any{"path": path},
	})
	require.NoError(t, err, "fsReadFile transport error")
	require.False(t, result.IsError, "fsReadFile tool error: %s", textOf(result))

	var out struct {
		Content     string `json:"content"`
		Permissions string `json:"permissions"`
	}
	require.NoError(t, json.Unmarshal([]byte(textOf(result)), &out), "fsReadFile response not JSON")
	return out.Content, out.Permissions
}

// callEditFile invokes fsEditFile and returns the raw MCP result for the caller to inspect.
func callEditFile(t *testing.T, session *mcp.ClientSession, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "fsEditFile",
		Arguments: args,
	})
	require.NoError(t, err, "fsEditFile transport error")
	return result
}

// textOf extracts the first TextContent block from a tool result. Returns "" if none.
func textOf(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	tc, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		return ""
	}
	return tc.Text
}

// cleanup deletes a file after a test. Best-effort; ignores errors so tests don't mask failures.
func cleanupFile(t *testing.T, session *mcp.ClientSession, path string) {
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_, _ = session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "fsDeleteFileOrDirectory",
			Arguments: map[string]any{"path": path},
		})
	})
}

// --- Tests ---

// TestFsEditFile_HappyPath_UniqueMatch: single occurrence replaced, file content updated,
// structured output reports occurrencesReplaced=1.
func TestFsEditFile_HappyPath_UniqueMatch(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("happy")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "hello world\nfoo bar\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "foo bar",
		"newString": "foo BAZ",
	})
	require.False(t, result.IsError, "expected success, got error: %s", textOf(result))

	var out editFileOutput
	require.NoError(t, json.Unmarshal([]byte(textOf(result)), &out))
	assert.Equal(t, path, out.Path)
	assert.Equal(t, 1, out.OccurrencesReplaced)

	content, _ := readFileMCP(t, session, path)
	assert.Equal(t, "hello world\nfoo BAZ\n", content)
}

// TestFsEditFile_ZeroMatch: oldString absent from file. Returns an error result
// with a message that prompts the model to re-read the file.
func TestFsEditFile_ZeroMatch(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("zero")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "alpha\nbeta\ngamma\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "delta",
		"newString": "epsilon",
	})
	assert.True(t, result.IsError, "expected error when oldString not found")
	assert.Contains(t, textOf(result), "not found",
		"error message should indicate the string was not found")

	// File must be unchanged.
	content, _ := readFileMCP(t, session, path)
	assert.Equal(t, "alpha\nbeta\ngamma\n", content)
}

// TestFsEditFile_MultipleMatches_WithoutReplaceAll: ambiguous match must error, not silently
// pick the first occurrence. The error message names the count.
func TestFsEditFile_MultipleMatches_WithoutReplaceAll(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("ambiguous")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "x = 1\nx = 1\nx = 1\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "x = 1",
		"newString": "x = 2",
	})
	assert.True(t, result.IsError, "expected error on multiple matches without replaceAll")
	assert.Contains(t, textOf(result), "3",
		"error message should report the number of matches")

	// File must be unchanged.
	content, _ := readFileMCP(t, session, path)
	assert.Equal(t, "x = 1\nx = 1\nx = 1\n", content)
}

// TestFsEditFile_MultipleMatches_ReplaceAll: with replaceAll=true, every occurrence is replaced
// and occurrencesReplaced equals the original match count.
func TestFsEditFile_MultipleMatches_ReplaceAll(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("replaceall")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "x = 1\nx = 1\nx = 1\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":       path,
		"oldString":  "x = 1",
		"newString":  "x = 2",
		"replaceAll": true,
	})
	require.False(t, result.IsError, "expected success with replaceAll, got: %s", textOf(result))

	var out editFileOutput
	require.NoError(t, json.Unmarshal([]byte(textOf(result)), &out))
	assert.Equal(t, 3, out.OccurrencesReplaced)

	content, _ := readFileMCP(t, session, path)
	assert.Equal(t, "x = 2\nx = 2\nx = 2\n", content)
}

// TestFsEditFile_IdenticalStrings: oldString == newString is a no-op the caller didn't mean.
// Should fail loudly rather than silently rewrite the file.
func TestFsEditFile_IdenticalStrings(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("identical")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "content\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "content",
		"newString": "content",
	})
	assert.True(t, result.IsError, "expected error when oldString == newString")
	assert.Contains(t, textOf(result), "identical",
		"error message should mention that the strings are identical")
}

// TestFsEditFile_EmptyOldString: empty oldString is invalid (would match everywhere, no useful semantics).
func TestFsEditFile_EmptyOldString(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("empty")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "content\n", "")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "",
		"newString": "anything",
	})
	assert.True(t, result.IsError, "expected error when oldString is empty")
}

// TestFsEditFile_PreservesPermissions: editing must not change the file's mode bits.
// fsWriteFile defaults to 0644, so we explicitly set 0640 on the source file.
func TestFsEditFile_PreservesPermissions(t *testing.T) {
	_, session := setupMCPClient(t)
	path := uniqueTestPath("perms")
	cleanupFile(t, session, path)

	writeFileMCP(t, session, path, "before\n", "640")
	_, modeBefore := readFileMCP(t, session, path)
	require.Equal(t, "640", modeBefore, "test precondition: file should be 0640")

	result := callEditFile(t, session, map[string]any{
		"path":      path,
		"oldString": "before",
		"newString": "after",
	})
	require.False(t, result.IsError, "edit failed: %s", textOf(result))

	_, modeAfter := readFileMCP(t, session, path)
	assert.Equal(t, modeBefore, modeAfter, "fsEditFile must preserve the file mode")
}
