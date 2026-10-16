package tests

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func startSignalTestProcess(t *testing.T, name, command string) {
	t.Helper()
	response, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{"name": name, "command": command})
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	t.Cleanup(func() {
		response, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
		if err == nil {
			response.Body.Close()
		}
	})
}

// The host announces a standby by sending SIGUSR2 to sandbox-api, which passes
// it on to the processes that handle it and leaves the others alone.
func TestSIGUSR2IsForwardedToProcessesThatHandleIt(t *testing.T) {
	if os.Getenv("WORKLOAD_USER") != "" {
		t.Skip("a workload user cannot signal the API it runs under")
	}
	suffix := time.Now().Format("150405.000000")
	handler, plain, sender := "usr2-handler-"+suffix, "usr2-plain-"+suffix, "usr2-sender-"+suffix

	startSignalTestProcess(t, handler, `trap 'echo GOT_USR2' USR2; echo READY; while :; do sleep 0.1; done`)
	startSignalTestProcess(t, plain, `exec sleep 30`)
	require.Eventually(t, func() bool { return strings.Contains(readTerminationState(t, handler).Logs, "READY") }, 5*time.Second, 20*time.Millisecond)

	// A process started by the API is its child, so $PPID is the API.
	startSignalTestProcess(t, sender, `kill -USR2 $PPID`)

	require.Eventually(t, func() bool { return strings.Contains(readTerminationState(t, handler).Logs, "GOT_USR2") }, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, "running", readTerminationState(t, handler).Status)
	require.Equal(t, "running", readTerminationState(t, plain).Status, "a process without a SIGUSR2 handler must not be signalled")
}
