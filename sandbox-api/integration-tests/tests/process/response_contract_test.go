package tests

import (
	"bufio"
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

// A prompt with no newline must reach the client before the process writes
// anything else, and the newlines it writes must arrive untouched.
func TestProcessExecuteStreamSendsPartialLinesRightAway(t *testing.T) {
	name := uniqueProcessName("framing-stdin")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	resp, err := makeRequestWithHeaders(http.MethodPost, "/process", map[string]interface{}{
		"command": "printf 'prompt> '; read line; printf 'a\\nb\\n'",
		"name":    name, "stdin": true,
	}, map[string]string{"Accept": "application/x-ndjson"})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	var stdout string
	for scanner.Scan() {
		var event StreamEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil || event.Type != "stdout" {
			continue
		}
		if stdout == "" {
			require.Equal(t, "prompt> ", event.Data)
			release, err := common.MakeRawRequest(http.MethodPost, "/process/"+name+"/stdin", strings.NewReader("continue\n"), "application/octet-stream")
			require.NoError(t, err)
			release.Body.Close()
			require.Equal(t, http.StatusOK, release.StatusCode)
		}
		stdout += event.Data
	}
	require.Equal(t, "prompt> a\nb\n", stdout)
}

func TestProcessExecuteHonorsJSONPreference(t *testing.T) {
	for _, accept := range []string{"application/x-ndjson;q=0, application/json", "application/x-ndjson;q=0.2, application/json;q=0.9", "application/x-ndjson-invalid"} {
		t.Run(accept, func(t *testing.T) {
			resp, err := makeRequestWithHeaders(http.MethodPost, "/process", map[string]interface{}{"command": "printf hello", "waitForCompletion": true}, map[string]string{"Accept": accept})
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Contains(t, resp.Header.Get("Content-Type"), "application/json")
			var body map[string]interface{}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			require.Equal(t, "hello", body["stdout"])
		})
	}
}
