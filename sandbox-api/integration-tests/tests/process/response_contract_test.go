package tests

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func executeProcess(t *testing.T, request map[string]interface{}) map[string]interface{} {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPost, "/process", request)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var body map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return body
}

func TestProcessResponseReportsDefaultWorkingDir(t *testing.T) {
	body := executeProcess(t, map[string]interface{}{
		"command":           "pwd",
		"waitForCompletion": true,
	})
	workingDir, _ := body["workingDir"].(string)
	require.NotEmpty(t, workingDir)
	require.Equal(t, strings.TrimSpace(body["stdout"].(string)), workingDir)
}

func TestProcessExecuteStreamsWithNDJSONAccept(t *testing.T) {
	resp, err := makeRequestWithHeaders(http.MethodPost, "/process", map[string]interface{}{
		"command": "echo hello",
	}, map[string]string{"Accept": "application/x-ndjson"})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "application/x-ndjson", resp.Header.Get("Content-Type"))

	events, result := parseStreamEvents(t, resp)
	require.NotNil(t, result)
	var stdout string
	for _, event := range events {
		if event.Type == "stdout" {
			stdout += event.Data
		}
	}
	require.Contains(t, stdout, "hello")
}
