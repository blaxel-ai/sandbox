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

// Private spool paths prevent a workload from opening a second descriptor and
// taking flock on it, while inherited descriptors must still accept output.
func TestProcessLogPathsArePrivate(t *testing.T) {
	marker := fmt.Sprintf("/tmp/identity-log-restart-%d", time.Now().UnixNano())
	t.Cleanup(func() { runCommand(t, "rm -f "+marker+"; printf cleaned") })
	command := `stdout=$(readlink /proc/$$/fd/1)
 stderr=$(readlink /proc/$$/fd/2)
 journal=${stdout%.stdout.log}.log
 for path in "$stdout" "$stderr" "$journal"; do
   if [ ! -f "$path" ]; then printf 'missing\n'; continue; fi
   printf 'mode:%s\n' "$(stat -c %a "$path")"
   if (exec 3<"$path") 2>/dev/null; then printf 'open:allowed\n'; else printf 'open:denied\n'; fi
 done
 printf 'inherited-stdout\n'
 printf 'inherited-stderr\n' >&2
 if [ ! -f ` + marker + ` ]; then touch ` + marker + `; exit 1; fi
 printf 'second-run-complete\n'`
	response, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command": command, "restartOnFailure": true, "maxRestarts": 1,
	})
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	var process struct {
		PID string `json:"pid"`
	}
	require.NoError(t, json.NewDecoder(response.Body).Decode(&process))
	require.NotEmpty(t, process.PID)
	t.Cleanup(func() {
		if r, err := common.MakeRequest(http.MethodDelete, "/process/"+process.PID+"/kill", nil); err == nil {
			r.Body.Close()
		}
	})
	var logs struct {
		Stdout string `json:"stdout"`
		Stderr string `json:"stderr"`
	}
	require.Eventually(t, func() bool {
		r, err := common.MakeRequest(http.MethodGet, "/process/"+process.PID+"/logs", nil)
		if err != nil {
			return false
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK || json.NewDecoder(r.Body).Decode(&logs) != nil {
			return false
		}
		return strings.Contains(logs.Stdout, "second-run-complete\n")
	}, 15*time.Second, 100*time.Millisecond, "inherited output must remain readable through the API after restart")
	require.NotContains(t, logs.Stdout, "missing\n")
	require.Equal(t, 6, strings.Count(logs.Stdout, "mode:600\n"), "stdout, stderr and journal must stay private on both runs: %s", logs.Stdout)
	expected, unexpected := "open:allowed\n", "open:denied\n"
	if workloadUser() != "" {
		expected, unexpected = unexpected, expected
	}
	require.Equal(t, 6, strings.Count(logs.Stdout, expected), "unexpected pathname permissions: %s", logs.Stdout)
	require.NotContains(t, logs.Stdout, unexpected)
	require.Equal(t, 2, strings.Count(logs.Stdout, "inherited-stdout\n"))
	require.Equal(t, "inherited-stderr\ninherited-stderr\n", logs.Stderr)
}
