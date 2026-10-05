package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func requireProcessStrings(t *testing.T, body map[string]interface{}) {
	t.Helper()
	for _, key := range []string{"completedAt", "logs", "stdout", "stderr"} {
		value, exists := body[key]
		require.True(t, exists, "missing %s", key)
		_, ok := value.(string)
		require.True(t, ok, "%s must be a string, got %#v", key, value)
	}
}

func TestProcessRequiredStringsAcrossEndpoints(t *testing.T) {
	name := fmt.Sprintf("required-strings-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	created := executeProcess(t, map[string]interface{}{"command": "cat", "name": name, "stdin": true})
	requireProcessStrings(t, created)
	require.Equal(t, "running", created["status"])
	require.Equal(t, "", created["completedAt"])
	checkGetAndList := func(identifier string, running bool) {
		t.Helper()
		var detail map[string]interface{}
		resp, err := common.MakeRequestAndParse(http.MethodGet, "/process/"+identifier, nil, &detail)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		requireProcessStrings(t, detail)
		var list []map[string]interface{}
		resp, err = common.MakeRequestAndParse(http.MethodGet, "/process", nil, &list)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		found := false
		for _, item := range list {
			if item["name"] == identifier {
				found = true
				requireProcessStrings(t, item)
				if running {
					require.Equal(t, "", item["completedAt"])
				} else {
					require.NotEmpty(t, item["completedAt"])
					require.Equal(t, "out\n", item["stdout"])
					require.Equal(t, "err\n", item["stderr"])
				}
			}
		}
		require.True(t, found, "process missing from list")
		if running {
			require.Equal(t, "", detail["completedAt"])
		} else {
			require.NotEmpty(t, detail["completedAt"])
			require.Equal(t, "out\n", detail["stdout"])
			require.Equal(t, "err\n", detail["stderr"])
		}
	}
	checkGetAndList(name, true)
	completedName := name + "-completed"
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, "/process/"+completedName+"/kill", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	completed := executeProcess(t, map[string]interface{}{"command": "printf 'out\\n'; printf 'err\\n' >&2", "name": completedName, "waitForCompletion": true})
	requireProcessStrings(t, completed)
	require.NotEmpty(t, completed["completedAt"])
	require.Equal(t, "out\n", completed["stdout"])
	require.Equal(t, "err\n", completed["stderr"])
	require.Contains(t, completed["logs"], "out\n")
	require.Contains(t, completed["logs"], "err\n")
	checkGetAndList(completedName, false)
}

func TestProcessStreamResultRequiredStrings(t *testing.T) {
	resp, err := makeRequestWithHeaders(http.MethodPost, "/process", map[string]interface{}{"command": "printf 'stream-out\\n'; printf 'stream-err\\n' >&2"}, map[string]string{"Accept": "text/event-stream"})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_, result := parseStreamEvents(t, resp)
	require.NotNil(t, result)
	requireProcessStrings(t, result)
	require.NotEmpty(t, result["completedAt"])
	require.Equal(t, "stream-out\n", result["stdout"])
	require.Equal(t, "stream-err\n", result["stderr"])
}
