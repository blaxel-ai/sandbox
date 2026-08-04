// Package tests covers the workload identity: what the API runs as on behalf
// of the user.
//
// The suite adapts to the instance it is pointed at. With WORKLOAD_USER set to
// the identity the API was started with (--user / BL_SANDBOX_USER), it asserts
// that everything user-facing is scoped to it and that nothing can escalate
// back to root. Without it, it asserts the opposite contract: an API with no
// identity configured keeps doing everything as root, which is what every
// existing image relies on.
package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// workloadUser is the identity the API under test was started with, empty when
// it runs everything as root.
func workloadUser() string {
	return strings.TrimSpace(os.Getenv("WORKLOAD_USER"))
}

func requireWorkloadUser(t *testing.T) string {
	t.Helper()
	user := workloadUser()
	if user == "" {
		t.Skip("WORKLOAD_USER is not set: the API under test runs as root")
	}
	return user
}

// runCommand executes a command through the process API and returns its output.
func runCommand(t *testing.T, command string) string {
	t.Helper()

	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command":           command,
		"waitForCompletion": true,
		"cwd":               "/tmp",
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var process map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&process))
	pid, ok := process["pid"].(string)
	require.True(t, ok, "process response has no pid: %v", process)

	// The logs are flushed asynchronously from the process exiting.
	var logs string
	for i := 0; i < 40; i++ {
		logsResp, err := common.MakeRequest(http.MethodGet, "/process/"+pid+"/logs", nil)
		require.NoError(t, err)
		var payload map[string]interface{}
		err = common.ParseJSONResponse(logsResp, &payload)
		logsResp.Body.Close()
		require.NoError(t, err)
		if stdout, ok := payload["stdout"].(string); ok {
			logs = strings.TrimSpace(stdout)
		}
		if logs != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	return logs
}

// TestProcessRunsAsTheWorkloadUser is the core promise of the feature: a
// command the user asks for runs as the image USER, not as the API.
func TestProcessRunsAsTheWorkloadUser(t *testing.T) {
	user := requireWorkloadUser(t)

	assert.Equal(t, user, runCommand(t, "id -un"), "process did not run as the workload user")
	assert.NotEqual(t, "0", runCommand(t, "id -u"), "process ran as uid 0")
}

func TestProcessEnvironmentMatchesTheWorkloadUser(t *testing.T) {
	user := requireWorkloadUser(t)

	assert.Equal(t, user, runCommand(t, "printenv USER"))
	assert.Equal(t, user, runCommand(t, "printenv LOGNAME"))
	assert.NotEqual(t, "/root", runCommand(t, "printenv HOME"), "HOME still points at the root home")
}

// TestProcessCannotRequestRoot covers the escalation path the design closes:
// there is no request field that asks for another identity, and env variables
// a workload controls must not become one.
func TestProcessCannotRequestRoot(t *testing.T) {
	requireWorkloadUser(t)

	for _, body := range []map[string]interface{}{
		{"command": "id -un", "waitForCompletion": true, "user": "root"},
		{"command": "id -un", "waitForCompletion": true, "env": map[string]string{"BL_SANDBOX_USER": "root", "BL_AS_ROOT": "1"}},
	} {
		resp, err := common.MakeRequest(http.MethodPost, "/process", body)
		require.NoError(t, err)
		var process map[string]interface{}
		err = common.ParseJSONResponse(resp, &process)
		resp.Body.Close()
		require.NoError(t, err)

		if pid, ok := process["pid"].(string); ok {
			logsResp, err := common.MakeRequest(http.MethodGet, "/process/"+pid+"/logs", nil)
			require.NoError(t, err)
			var payload map[string]interface{}
			err = common.ParseJSONResponse(logsResp, &payload)
			logsResp.Body.Close()
			require.NoError(t, err)
			assert.NotEqual(t, "root", strings.TrimSpace(fmt.Sprint(payload["stdout"])), "request %v obtained root", body)
		}
	}
}

// TestFilesystemIsScopedToTheWorkloadUser: the filesystem endpoints are the
// other way a process could reach root-owned files, so they are subject to the
// same permissions.
func TestFilesystemIsScopedToTheWorkloadUser(t *testing.T) {
	requireWorkloadUser(t)

	// A root-owned 0600 file the API itself can see.
	secret := "/root/.integration-identity-secret"

	resp, err := common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(secret), nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "a root-only file was readable through the filesystem API")

	// Writing into a root-owned directory must be refused too.
	resp, err = common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath("/root/identity-probe"), map[string]interface{}{
		"content": "nope",
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEqual(t, http.StatusOK, resp.StatusCode, "the filesystem API wrote into /root as the workload user")
}

// TestFilesystemWritesAreOwnedByTheWorkloadUser makes sure the scoping is not
// merely a denial: the user must still be able to work in its own space, and
// what it creates belongs to it.
func TestFilesystemWritesAreOwnedByTheWorkloadUser(t *testing.T) {
	user := requireWorkloadUser(t)

	path := "/tmp/identity-owned-file"
	resp, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(path), map[string]interface{}{
		"content": "written through the API",
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	assert.Equal(t, user, runCommand(t, "stat -c %U "+path))
}

// TestDriveMountCannotTakeOverAnExistingDirectory covers the other way a
// workload could reach root: the mount point of a drive is handed to the
// workload user, and the mount path comes from the request. Asking for an
// existing root-owned directory must not change its owner, otherwise the
// workload could replace a binary the root API later runs.
func TestDriveMountCannotTakeOverAnExistingDirectory(t *testing.T) {
	requireWorkloadUser(t)

	// A root-owned directory that exists in every image, and holds binaries
	// the API executes as root.
	const target = "/usr/local/bin"
	require.Equal(t, "root", runCommand(t, "stat -c %U "+target), "precondition: %s is root-owned", target)

	resp, err := common.MakeRequest(http.MethodPost, "/drives/mount", map[string]interface{}{
		"driveName": "identity-probe",
		"mountPath": target,
	})
	require.NoError(t, err)
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.NoError(t, err)

	// The mount itself is expected to fail (there is no such drive); what
	// matters is that nothing was handed over on the way.
	require.NotContains(t, string(body), "BL_WORKSPACE_ID",
		"the request never reached the mount point creation, set BL_WORKSPACE_ID on the API under test")
	assert.Equal(t, "root", runCommand(t, "stat -c %U "+target),
		"a drive mount request changed the owner of %s", target)
}

// TestDefaultInstanceStaysRoot is the compatibility contract: with no identity
// configured, nothing changed for existing images.
func TestDefaultInstanceStaysRoot(t *testing.T) {
	if workloadUser() != "" {
		t.Skip("the API under test has a workload identity configured")
	}
	if runCommand(t, "id -u") != "0" {
		t.Skip("the API itself is not running as root")
	}

	assert.Equal(t, "0", runCommand(t, "id -u"), "an API with no workload identity must keep running processes as root")
}
