package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

type terminationState struct {
	Status       string  `json:"status"`
	CompletedAt  *string `json:"completedAt"`
	ExitCode     int     `json:"exitCode"`
	Logs         string  `json:"logs"`
	RestartCount int     `json:"restartCount"`
}

func readTerminationState(t *testing.T, name string) terminationState {
	t.Helper()
	response, err := common.MakeRequest(http.MethodGet, "/process/"+name, nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var state terminationState
	require.NoError(t, json.NewDecoder(response.Body).Decode(&state))
	return state
}

func signalTerminationProcess(t *testing.T, name, suffix string) {
	t.Helper()
	response, err := common.MakeRequest(http.MethodDelete, "/process/"+name+suffix, nil)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
}

// These exercise real signals through HTTP: accepting a stop request must not
// be confused with observing the command's exit.
func TestProcessTerminationConfirmation(t *testing.T) {
	for _, test := range []struct {
		name    string
		command string
		ignore  bool
	}{
		{"ignores-term", `exec sh -c 'trap "" TERM; echo READY; while :; do sleep 1; done'`, true},
		{"graceful-exit", `exec sh -c 'trap "echo TERMINATING; sleep 1; echo FINAL; exit 7" TERM; echo READY; while :; do sleep 1; done'`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			name := fmt.Sprintf("termination-%s-%d", test.name, time.Now().UnixNano())
			response, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
				"name": name, "command": test.command, "restartOnFailure": true, "maxRestarts": 3,
			})
			require.NoError(t, err)
			response.Body.Close()
			require.Equal(t, http.StatusOK, response.StatusCode)
			t.Cleanup(func() {
				response, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
				if err == nil {
					response.Body.Close()
				}
			})
			require.Eventually(t, func() bool { return strings.Contains(readTerminationState(t, name).Logs, "READY") }, 5*time.Second, 20*time.Millisecond)
			signalTerminationProcess(t, name, "")
			// Observe the process throughout the delay, rather than only checking
			// immediately after the signal was sent.
			deadline := time.Now().Add(250 * time.Millisecond)
			for time.Now().Before(deadline) {
				state := readTerminationState(t, name)
				require.Equal(t, "running", state.Status)
				require.True(t, state.CompletedAt == nil || *state.CompletedAt == "")
				time.Sleep(20 * time.Millisecond)
			}
			wantStatus, wantExit := "stopped", 7
			if test.ignore {
				signalTerminationProcess(t, name, "/kill")
				wantStatus, wantExit = "killed", -1
			}
			require.Eventually(t, func() bool { return readTerminationState(t, name).Status == wantStatus }, 5*time.Second, 20*time.Millisecond)
			state := readTerminationState(t, name)
			require.NotNil(t, state.CompletedAt)
			require.NotEmpty(t, *state.CompletedAt)
			require.Equal(t, wantExit, state.ExitCode)
			require.Zero(t, state.RestartCount)
			if !test.ignore {
				require.Contains(t, state.Logs, "FINAL")
			}
		})
	}
}
