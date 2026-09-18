package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWelcomeDiscoversAPIReference(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodGet, "/", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var welcome map[string]string
	require.NoError(t, common.ParseJSONResponse(resp, &welcome))
	require.Equal(t, "/swagger/doc.json", welcome["apiReference"])
	assert.Equal(t, "Welcome to your Blaxel Sandbox. Discover your capabilities here: /swagger/doc.json", welcome["message"])
	assert.Equal(t, "https://docs.blaxel.ai/Sandboxes/Overview", welcome["documentation"])
	assert.NotEmpty(t, welcome["description"])

	referenceResp, err := common.MakeRequest(http.MethodGet, welcome["apiReference"], nil)
	require.NoError(t, err)
	defer referenceResp.Body.Close()
	require.Equal(t, http.StatusOK, referenceResp.StatusCode)

	var reference struct {
		Swagger string                     `json:"swagger"`
		Paths   map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, common.ParseJSONResponse(referenceResp, &reference))
	assert.Equal(t, "2.0", reference.Swagger)
	require.NotEmpty(t, reference.Paths)
	assert.Contains(t, reference.Paths, "/process")
}
