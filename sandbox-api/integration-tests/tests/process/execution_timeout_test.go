package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func executeTimeoutCommand(t *testing.T, request map[string]interface{}) terminationState {
	t.Helper()
	response, err := common.MakeRequest(http.MethodPost, "/process", request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var state terminationState
	require.NoError(t, json.NewDecoder(response.Body).Decode(&state))
	return state
}

func cleanupTimeoutCommand(t *testing.T, name string) {
	t.Helper()
	t.Cleanup(func() {
		response, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
		if err == nil {
			response.Body.Close()
		}
	})
}

func requireTimeoutCommandDead(t *testing.T, name string) {
	t.Helper()
	require.Eventually(t, func() bool {
		return readTerminationState(t, name).Status == "killed"
	}, 5*time.Second, 50*time.Millisecond)
	state := readTerminationState(t, name)
	require.NotNil(t, state.CompletedAt)
	require.NotEmpty(t, *state.CompletedAt)
	require.Equal(t, -1, state.ExitCode)

	// The public process ID can remain stable across restarts. Read the actual
	// shell PID instead, then check the OS through a separate API command.
	pid, err := strconv.Atoi(strings.TrimSpace(state.Logs))
	require.NoError(t, err)
	require.Positive(t, pid)
	probe := executeTimeoutCommand(t, map[string]interface{}{
		"command":           fmt.Sprintf("if kill -0 %d 2>/dev/null; then echo ALIVE; else echo DEAD; fi", pid),
		"waitForCompletion": true,
	})
	require.Equal(t, "DEAD\n", probe.Logs, "timeout must kill the command, not merely stop waiting for it")
}

func TestProcessExecutionTimeout(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		for _, wait := range []bool{false, true} {
			t.Run(fmt.Sprintf("keepAlive=%t/waitForCompletion=%t", keepAlive, wait), func(t *testing.T) {
				name := fmt.Sprintf("execution-timeout-%d", time.Now().UnixNano())
				cleanupTimeoutCommand(t, name)
				response, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
					"name": name, "command": "echo $$; exec sleep 30",
					"timeout": 1, "keepAlive": keepAlive, "waitForCompletion": wait,
					"restartOnFailure": true, "maxRestarts": 3,
				})
				require.NoError(t, err)
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				require.NoError(t, err)
				if wait && response.StatusCode == http.StatusUnprocessableEntity {
					// The existing synchronous wait can expire just before the
					// execution timer confirms death. Its HTTP error is unchanged.
					require.Contains(t, string(body), "process timed out after 1 seconds")
				} else {
					require.Equal(t, http.StatusOK, response.StatusCode, string(body))
				}
				requireTimeoutCommandDead(t, name)
				require.Zero(t, readTerminationState(t, name).RestartCount)
			})
		}
	}
}

func TestProcessExecutionTimeoutZeroAndEarlyCompletion(t *testing.T) {
	for _, keepAlive := range []bool{false, true} {
		for _, test := range []struct {
			name    string
			timeout int
			command string
		}{
			{"zero-is-unlimited", 0, "sleep 2; echo DONE"},
			{"early-completion", 1, "echo DONE"},
		} {
			t.Run(fmt.Sprintf("%s/keepAlive=%t", test.name, keepAlive), func(t *testing.T) {
				name := fmt.Sprintf("execution-completion-%d", time.Now().UnixNano())
				cleanupTimeoutCommand(t, name)
				state := executeTimeoutCommand(t, map[string]interface{}{
					"name": name, "command": test.command, "timeout": test.timeout,
					"keepAlive": keepAlive, "waitForCompletion": true,
				})
				require.Equal(t, "completed", state.Status)
				require.Zero(t, state.ExitCode)
				require.Equal(t, "DONE\n", state.Logs)
				if test.timeout > 0 {
					// A completed process must stay completed after its old deadline.
					time.Sleep(1200 * time.Millisecond)
					require.Equal(t, "completed", readTerminationState(t, name).Status)
				}
			})
		}
	}
}

func TestProcessExecutionTimeoutAfterClientDisconnect(t *testing.T) {
	name := fmt.Sprintf("execution-disconnect-%d", time.Now().UnixNano())
	cleanupTimeoutCommand(t, name)
	response, err := common.MakeRequestWithTimeout(http.MethodPost, "/process", map[string]interface{}{
		"name": name, "command": "echo $$; exec sleep 30",
		"timeout": 2, "keepAlive": false, "waitForCompletion": true,
	}, 500*time.Millisecond)
	if response != nil {
		response.Body.Close()
	}
	require.Error(t, err, "the HTTP client must disconnect before the execution deadline")
	require.Equal(t, "running", readTerminationState(t, name).Status,
		"a client disconnect must not become the execution timeout")
	requireTimeoutCommandDead(t, name)
}
