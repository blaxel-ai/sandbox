package tests

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A command at or above Linux MAX_ARG_STRLEN (131072 bytes) used to fail with
// 422 "fork/exec /bin/sh: argument list too long".
func TestProcessRunsCommandLongerThanArgvAllows(t *testing.T) {
	command := "X='" + strings.Repeat("a", 204800) + "'\necho ${#X} | tr -d ' '\npwd"

	resp, err := common.MakeRequestWithTimeout(http.MethodPost, "/process", map[string]any{
		"command":           command,
		"workingDir":        "/tmp",
		"waitForCompletion": true,
	}, 30*time.Second)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var proc map[string]any
	require.NoError(t, common.ParseJSONResponse(resp, &proc))
	assert.Equal(t, "completed", proc["status"])
	assert.Equal(t, float64(0), proc["exitCode"])
	assert.Equal(t, "204800\n/tmp\n", proc["logs"])
}

func TestProcessLongCommandKeepsStreamedStdin(t *testing.T) {
	name := "large-command-stdin"
	startWithStdin(t, name, "#"+strings.Repeat("x", 200*1024)+"\nread line; echo \"got:$line\"")
	lines := stdoutLines(t, name)

	require.Equal(t, http.StatusOK, writeStdin(t, name, "hello").StatusCode)

	select {
	case line := <-lines:
		assert.Equal(t, "got:hello", line)
	case <-time.After(30 * time.Second):
		t.Fatal("no output from the process")
	}
}
