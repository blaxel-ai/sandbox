// Package tests covers BL_SANDBOX_API_ISOLATION: user workloads must not reach
// the API from inside the sandbox, while root (initrd, image entrypoints) still
// can.
//
// It needs an API started with the isolation on and BL_SANDBOX_USER set to
// WORKLOAD_USER, without BL_SANDBOX_USER_ENABLED: the isolation has to turn the
// identity on by itself. The suite runs as root, since a non-root test runner
// is exactly what the isolation refuses. It is skipped unless ISOLATION=true.
package tests

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func requireIsolation(t *testing.T) string {
	t.Helper()
	if os.Getenv("ISOLATION") != "true" {
		t.Skip("ISOLATION is not set: the API under test is not isolated")
	}
	if os.Geteuid() != 0 {
		t.Fatal("the isolation suite must run as root")
	}
	user := strings.TrimSpace(os.Getenv("WORKLOAD_USER"))
	require.NotEmpty(t, user, "WORKLOAD_USER must name the image USER the API was started with")
	return user
}

func apiPort(t *testing.T) string {
	t.Helper()
	u, err := url.Parse(common.GetEnv("API_BASE_URL", "http://localhost:8080"))
	require.NoError(t, err)
	return u.Port()
}

// noResponse is what probe prints when the connection was refused or reset.
const noResponse = "000"

// probe is a shell command printing the HTTP status of /health on addr.
func probe(addr string) string {
	return fmt.Sprintf("curl -s -o /dev/null -w '%%{http_code}' --max-time 5 'http://%s/health'; true", addr)
}

func runProcess(t *testing.T, command string) string {
	t.Helper()
	var process struct {
		Logs     string `json:"logs"`
		ExitCode int    `json:"exitCode"`
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/process", map[string]interface{}{
		"command":           command,
		"waitForCompletion": true,
		"cwd":               "/tmp",
	}, &process)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	return strings.TrimSpace(process.Logs)
}

func TestRootReachesTheAPI(t *testing.T) {
	requireIsolation(t)
	resp, err := common.MakeRequest(http.MethodGet, "/health", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// The image USER applies without BL_SANDBOX_USER_ENABLED.
func TestIsolationEnablesTheImageUser(t *testing.T) {
	user := requireIsolation(t)
	require.Equal(t, user, runProcess(t, "id -un"))
}

func TestWorkloadCannotCallTheAPI(t *testing.T) {
	requireIsolation(t)
	port := apiPort(t)
	for _, addr := range []string{"127.0.0.1:" + port, "[::1]:" + port, "localhost:" + port} {
		require.Equal(t, noResponse, runProcess(t, probe(addr)), "a workload process reached the API on %s", addr)
	}
}

// Any non-root process is refused, not only the ones the API spawned.
func TestNonRootProcessCannotCallTheAPI(t *testing.T) {
	requireIsolation(t)
	cmd := exec.Command("sh", "-c", probe("127.0.0.1:"+apiPort(t)))
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
	out, err := cmd.Output()
	require.NoError(t, err)
	require.Equal(t, noResponse, strings.TrimSpace(string(out)), "a non-root process reached the API")
}
