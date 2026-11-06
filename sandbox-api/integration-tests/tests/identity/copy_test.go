package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func TestCopyRespectsWorkloadIdentity(t *testing.T) {
	user := requireWorkloadUser(t)
	root := fmt.Sprintf("/tmp/identity-copy-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		runCommand(t, "chmod -R u+rwx "+root)
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(root)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	resp, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(root+"/source/file"),
		map[string]interface{}{"content": "original"})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	runCommand(t, "chmod 0500 "+root+"/source")
	resp, err = common.MakeRequest(http.MethodPost, "/filesystem-copy",
		map[string]interface{}{"source": root + "/source", "destination": root + "/copied", "noOverwrite": true})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, user, runCommand(t, "stat -c %U "+root+"/copied/file"))
	require.Equal(t, "500", runCommand(t, "stat -c %a "+root+"/copied"))

	// The copy route must not become a privileged read or write primitive.
	for _, pair := range [][2]string{
		{"/root/.integration-identity-secret", root + "/secret-copy"},
		{root + "/source/file", "/root/copy-probe"},
	} {
		resp, err = common.MakeRequest(http.MethodPost, "/filesystem-copy",
			map[string]interface{}{"source": pair[0], "destination": pair[1], "noOverwrite": true})
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)
	}
}
