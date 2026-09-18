package tests

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startWithStdin starts a named process with a stdin pipe and kills it on cleanup.
func startWithStdin(t *testing.T, name, command string) {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{
		"name":    name,
		"command": command,
		"stdin":   true,
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var proc map[string]any
	require.NoError(t, common.ParseJSONResponse(resp, &proc))
	assert.Equal(t, true, proc["stdin"], "response should report the stdin pipe")
	t.Cleanup(func() {
		r, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
		if err == nil {
			r.Body.Close()
		}
	})
}

// stdoutLines follows the process log stream and yields raw stdout lines: the
// "stdout:" tag stripped, stderr and keepalives dropped. This is exactly what a
// stdio client has to do on its side.
func stdoutLines(t *testing.T, name string) <-chan string {
	t.Helper()
	resp, err := common.MakeRequestWithTimeout(http.MethodGet, "/process/"+name+"/logs/stream", nil, 60*time.Second)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() { resp.Body.Close() })

	out := make(chan string, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			if line, ok := strings.CutPrefix(sc.Text(), "stdout:"); ok {
				out <- line
			}
		}
	}()
	return out
}

func writeStdin(t *testing.T, name, line string) *http.Response {
	t.Helper()
	resp, err := common.MakeRawRequest(http.MethodPost, "/process/"+name+"/stdin", strings.NewReader(line+"\n"), "application/octet-stream")
	require.NoError(t, err)
	resp.Body.Close()
	return resp
}

// waitJSONRPC returns the first JSON-RPC message on the stream with the given id.
func waitJSONRPC(t *testing.T, lines <-chan string, id int) map[string]any {
	t.Helper()
	deadline := time.After(30 * time.Second)
	for {
		select {
		case line, ok := <-lines:
			require.True(t, ok, "stream closed before response id %d", id)
			var msg map[string]any
			if json.Unmarshal([]byte(line), &msg) == nil && msg["id"] == float64(id) {
				return msg
			}
		case <-deadline:
			t.Fatalf("no JSON-RPC response with id %d", id)
		}
	}
}

func processStatus(t *testing.T, name string) string {
	t.Helper()
	var proc map[string]any
	resp, err := common.MakeRequestAndParse(http.MethodGet, "/process/"+name, nil, &proc)
	require.NoError(t, err)
	resp.Body.Close()
	status, _ := proc["status"].(string)
	return status
}

// A deterministic stdio server: a shell loop answering every request with a
// fixed result. No network, no runtime beyond sh, so it runs in CI.
func TestStdinJSONRPCRoundTrip(t *testing.T) {
	name := uniqueProcessName("stdin-echo")
	startWithStdin(t, name, `while IFS= read -r l; do id=$(printf '%s' "$l" | sed -n 's/.*"id":\([0-9]*\).*/\1/p'); echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"echo\":$l}}"; done`)
	lines := stdoutLines(t, name)

	resp := writeStdin(t, name, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	msg := waitJSONRPC(t, lines, 1)
	result := msg["result"].(map[string]any)
	// The request came back intact inside the reply: no echo, no escape codes.
	assert.Equal(t, "ping", result["echo"].(map[string]any)["method"])

	// Order is preserved across sequential writes.
	writeStdin(t, name, `{"jsonrpc":"2.0","id":2,"method":"a"}`)
	writeStdin(t, name, `{"jsonrpc":"2.0","id":3,"method":"b"}`)
	assert.Equal(t, float64(2), waitJSONRPC(t, lines, 2)["id"])
	assert.Equal(t, float64(3), waitJSONRPC(t, lines, 3)["id"])

	// EOF ends the loop: the documented shutdown path.
	resp, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/stdin", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Eventually(t, func() bool { return processStatus(t, name) == "completed" },
		10*time.Second, 200*time.Millisecond, "process should exit on stdin EOF")

	// After EOF the pipe is gone for good.
	resp = writeStdin(t, name, "late")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

// A process started without stdin refuses writes with 409, not 500.
func TestStdinNotEnabled(t *testing.T) {
	name := uniqueProcessName("no-stdin")
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{"name": name, "command": "sleep 5"})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() {
		if r, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil); err == nil {
			r.Body.Close()
		}
	})

	resp = writeStdin(t, name, "x")
	assert.Equal(t, http.StatusConflict, resp.StatusCode)

	resp = writeStdin(t, uniqueProcessName("ghost"), "x")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// The real thing: an MCP server over stdio, driven through the API. Needs node
// and network access in the image, so it is opt-in.
func TestStdinRealMCPServer(t *testing.T) {
	if os.Getenv("MCP_STDIO_TEST") == "" {
		t.Skip("set MCP_STDIO_TEST=1 to run against a real stdio MCP server (needs node + network)")
	}
	name := uniqueProcessName("mcp-fs")
	startWithStdin(t, name, "npx -y @modelcontextprotocol/server-filesystem /tmp")
	lines := stdoutLines(t, name)

	writeStdin(t, name, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"sandbox-api-it","version":"0"}}}`)
	init := waitJSONRPC(t, lines, 1)
	require.NotNil(t, init["result"], "initialize failed: %v", init["error"])
	assert.NotEmpty(t, init["result"].(map[string]any)["serverInfo"])

	writeStdin(t, name, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeStdin(t, name, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	toolsResp := waitJSONRPC(t, lines, 2)
	require.NotNil(t, toolsResp["result"], "tools/list failed: %v", toolsResp["error"])
	assert.NotEmpty(t, toolsResp["result"].(map[string]any)["tools"])

	resp, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/stdin", nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Eventually(t, func() bool { return processStatus(t, name) != "running" },
		15*time.Second, 200*time.Millisecond, "MCP server should shut down on stdin EOF")
}
