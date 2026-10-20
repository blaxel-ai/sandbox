package tests

import (
	"net/http"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

func TestDetachDriveNotMounted(t *testing.T) {
	for name, path := range map[string]string{
		"missing path":    "/drives/mount/tmp/never-mounted-drive",
		"plain directory": "/drives/mount/tmp",
	} {
		t.Run(name, func(t *testing.T) {
			resp, err := common.MakeRequest(http.MethodDelete, path, nil)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusNotFound, resp.StatusCode)
		})
	}
}
