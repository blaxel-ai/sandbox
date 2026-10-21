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

func runAndReadLogs(t *testing.T, name, command string) string {
	t.Helper()
	response, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"name": name, "command": command, "waitForCompletion": true,
	})
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var result struct {
		Logs string `json:"logs"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&result))
	return strings.TrimSpace(result.Logs)
}

// sandbox-api is PID 1: a background job whose shell has exited is handed to
// it, and must not stay a zombie once it exits.
func TestOrphanedBackgroundJobDoesNotStayAZombie(t *testing.T) {
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	pid := runAndReadLogs(t, "orphan-spawn-"+suffix, `sleep 300 & echo $!`)
	require.Regexp(t, `^\d+$`, pid)

	// The orphan is killed, then given a moment to be collected: a zombie is
	// still listed in /proc, in state Z.
	outcome := runAndReadLogs(t, "orphan-kill-"+suffix, fmt.Sprintf(
		`kill %[1]s; for i in $(seq 50); do [ -e /proc/%[1]s ] || { echo REAPED; exit 0; }; sleep 0.1; done; cat /proc/%[1]s/stat`, pid))
	require.Equal(t, "REAPED", outcome)
}
