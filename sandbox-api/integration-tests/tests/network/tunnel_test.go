package tests

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validTestKey() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func validTestKey2() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 128)
	}
	return base64.StdEncoding.EncodeToString(key)
}

func buildTunnelConfigBase64(cfg map[string]interface{}) string {
	jsonData, _ := json.Marshal(cfg)
	return base64.StdEncoding.EncodeToString(jsonData)
}

func validTunnelConfig() map[string]interface{} {
	return map[string]interface{}{
		"local_ip":        "10.99.0.1/32",
		"peer_endpoint":   "1.2.3.4:51820",
		"peer_public_key": validTestKey(),
		"private_key":     validTestKey2(),
	}
}

func TestTunnelUpdateConfig_MissingBody(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTunnelUpdateConfig_EmptyConfig(t *testing.T) {
	body := map[string]interface{}{
		"config": "",
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestTunnelUpdateConfig_InvalidBase64(t *testing.T) {
	body := map[string]interface{}{
		"config": "not-valid-base64!!!",
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["error"], "invalid tunnel config")
}

func TestTunnelUpdateConfig_InvalidJSON(t *testing.T) {
	body := map[string]interface{}{
		"config": base64.StdEncoding.EncodeToString([]byte("not json")),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

	var errResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)
	assert.Contains(t, errResp["error"], "invalid tunnel config")
}

func TestTunnelUpdateConfig_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "missing local_ip",
			config: map[string]interface{}{
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
			},
		},
		{
			name: "missing peer_endpoint",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
			},
		},
		{
			name: "missing peer_public_key",
			config: map[string]interface{}{
				"local_ip":      "10.0.0.1/32",
				"peer_endpoint": "1.2.3.4:51820",
				"private_key":   validTestKey2(),
			},
		},
		{
			name: "missing private_key",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"config": buildTunnelConfigBase64(tc.config),
			}

			resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

			var errResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&errResp)
			require.NoError(t, err)
			assert.Contains(t, errResp["error"], "invalid tunnel config")
		})
	}
}

func TestTunnelUpdateConfig_InvalidFieldValues(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]interface{}
	}{
		{
			name: "local_ip not CIDR",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
			},
		},
		{
			name: "peer_endpoint missing port",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
			},
		},
		{
			name: "invalid peer_public_key",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": "not-a-valid-key",
				"private_key":     validTestKey2(),
			},
		},
		{
			name: "invalid private_key",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     "not-a-valid-key",
			},
		},
		{
			name: "MTU too low",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
				"mtu":             10,
			},
		},
		{
			name: "MTU too high",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
				"mtu":             99999,
			},
		},
		{
			name: "invalid allowed_ips",
			config: map[string]interface{}{
				"local_ip":        "10.0.0.1/32",
				"peer_endpoint":   "1.2.3.4:51820",
				"peer_public_key": validTestKey(),
				"private_key":     validTestKey2(),
				"allowed_ips":     []string{"not-a-cidr"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]interface{}{
				"config": buildTunnelConfigBase64(tc.config),
			}

			resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusUnprocessableEntity, resp.StatusCode)

			var errResp map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&errResp)
			require.NoError(t, err)
			assert.Contains(t, errResp["error"], "invalid tunnel config")
		})
	}
}

func TestTunnelUpdateConfig_ValidConfig(t *testing.T) {
	cfg := validTunnelConfig()
	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	// On a real Linux sandbox with proper permissions, this succeeds (200).
	// On environments without TUN device support, it may return 500.
	if resp.StatusCode == http.StatusOK {
		assert.Contains(t, result["message"], "updated")

		// Clean up: disconnect the tunnel we just created
		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
		assert.Equal(t, http.StatusOK, disconnectResp.StatusCode)
	} else if resp.StatusCode == http.StatusInternalServerError {
		// Expected on non-Linux or environments without TUN/WireGuard support
		assert.Contains(t, result["error"], "failed to apply tunnel config")
		t.Logf("Tunnel creation failed (expected in non-Linux/unprivileged environments): %v", result["error"])
	} else {
		t.Fatalf("Unexpected status code %d: %v", resp.StatusCode, result)
	}
}

func TestTunnelUpdateConfig_ValidConfigWithAllOptions(t *testing.T) {
	keepalive := 30
	cfg := map[string]interface{}{
		"local_ip":             "10.99.0.2/32",
		"peer_endpoint":        "5.6.7.8:51820",
		"peer_public_key":      validTestKey(),
		"private_key":          validTestKey2(),
		"mtu":                  1400,
		"listen_port":          51821,
		"interface_name":       "wg1",
		"allowed_ips":          []string{"0.0.0.0/0", "10.0.0.0/8"},
		"persistent_keepalive": keepalive,
		"route_all":            false,
	}

	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)

	if resp.StatusCode == http.StatusOK {
		assert.Contains(t, result["message"], "updated")

		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
		assert.Equal(t, http.StatusOK, disconnectResp.StatusCode)
	} else if resp.StatusCode == http.StatusInternalServerError {
		t.Logf("Tunnel creation failed (expected in non-Linux/unprivileged environments): %v", result["error"])
	} else {
		t.Fatalf("Unexpected status code %d: %v", resp.StatusCode, result)
	}
}

func TestTunnelDisconnect_NoTunnelRunning(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	var errResp map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&errResp)
	require.NoError(t, err)

	errMsg, ok := errResp["error"].(string)
	require.True(t, ok, "expected error field in response")
	// On Linux: "no WireGuard client is running"; on non-Linux: "WireGuard networking is only supported on Linux"
	assert.True(t,
		strings.Contains(errMsg, "no WireGuard client is running") ||
			strings.Contains(errMsg, "only supported on Linux"),
		"unexpected error message: %s", errMsg,
	)
}

func TestTunnelDisconnect_AfterSuccessfulConnect(t *testing.T) {
	cfg := validTunnelConfig()
	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	// First, try to connect
	connectResp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer connectResp.Body.Close()

	if connectResp.StatusCode != http.StatusOK {
		t.Skip("Skipping disconnect test: tunnel creation not supported in this environment")
	}

	// Now disconnect
	resp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	err = json.NewDecoder(resp.Body).Decode(&result)
	require.NoError(t, err)
	assert.Contains(t, result["message"], "disconnected")

	// Disconnecting again should fail
	resp2, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer resp2.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp2.StatusCode)
}

func TestTunnelUpdateConfig_NoGetEndpoint(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodGet, "/network/tunnel/config", nil)
	require.NoError(t, err)
	defer resp.Body.Close()

	// There is intentionally no GET endpoint for tunnel config (security: prevents key leakage)
	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}

func TestTunnelUpdateConfig_ExtraFieldsIgnored(t *testing.T) {
	cfg := validTunnelConfig()
	cfg["unknown_field"] = "should be ignored"
	cfg["another_unknown"] = 12345

	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should either succeed (200) or fail at the system level (500), but not 422
	assert.NotEqual(t, http.StatusUnprocessableEntity, resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
	}
}

func TestTunnelUpdateConfig_WrongHTTPMethod(t *testing.T) {
	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(validTunnelConfig()),
	}

	// POST should not work for tunnel config (only PUT is registered)
	resp, err := common.MakeRequest(http.MethodPost, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.NotEqual(t, http.StatusOK, resp.StatusCode)
}

func TestTunnelUpdateConfig_ReplaceExisting(t *testing.T) {
	cfg1 := validTunnelConfig()
	body1 := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg1),
	}

	resp1, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body1)
	require.NoError(t, err)
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Skip("Skipping config replacement test: tunnel creation not supported in this environment")
	}

	// Replace with a different config (different local IP)
	cfg2 := validTunnelConfig()
	cfg2["local_ip"] = "10.99.0.2/32"
	body2 := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg2),
	}

	resp2, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body2)
	require.NoError(t, err)
	defer resp2.Body.Close()

	var result map[string]interface{}
	err = json.NewDecoder(resp2.Body).Decode(&result)
	require.NoError(t, err)

	// Updating an already-running tunnel should tear down and recreate
	assert.Equal(t, http.StatusOK, resp2.StatusCode)
	assert.Contains(t, result["message"], "updated")

	// Clean up
	disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer disconnectResp.Body.Close()
	assert.Equal(t, http.StatusOK, disconnectResp.StatusCode)
}

func TestTunnelUpdateConfig_ConnectDisconnectReconnect(t *testing.T) {
	cfg := validTunnelConfig()
	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	// Connect
	resp1, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp1.Body.Close()

	if resp1.StatusCode != http.StatusOK {
		t.Skip("Skipping reconnect test: tunnel creation not supported in this environment")
	}

	// Disconnect
	resp2, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer resp2.Body.Close()
	require.Equal(t, http.StatusOK, resp2.StatusCode)

	// Reconnect with the same config
	resp3, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp3.Body.Close()

	assert.Equal(t, http.StatusOK, resp3.StatusCode)

	var result map[string]interface{}
	err = json.NewDecoder(resp3.Body).Decode(&result)
	require.NoError(t, err)
	assert.Contains(t, result["message"], "updated")

	// Final cleanup
	resp4, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
	require.NoError(t, err)
	defer resp4.Body.Close()
	assert.Equal(t, http.StatusOK, resp4.StatusCode)
}

func TestTunnelUpdateConfig_IPv6PeerEndpoint(t *testing.T) {
	cfg := map[string]interface{}{
		"local_ip":        "10.99.0.1/32",
		"peer_endpoint":   "[2001:db8::1]:51820",
		"peer_public_key": validTestKey(),
		"private_key":     validTestKey2(),
	}

	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should pass validation (422 would mean the IPv6 endpoint format was rejected)
	assert.NotEqual(t, http.StatusUnprocessableEntity, resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
	}
}

func TestTunnelUpdateConfig_KeepaliveDisabled(t *testing.T) {
	cfg := map[string]interface{}{
		"local_ip":             "10.99.0.1/32",
		"peer_endpoint":        "1.2.3.4:51820",
		"peer_public_key":      validTestKey(),
		"private_key":          validTestKey2(),
		"persistent_keepalive": 0,
	}

	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// keepalive=0 should be accepted by validation
	assert.NotEqual(t, http.StatusUnprocessableEntity, resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
	}
}

func TestTunnelUpdateConfig_MultipleAllowedIPs(t *testing.T) {
	cfg := map[string]interface{}{
		"local_ip":        "10.99.0.1/32",
		"peer_endpoint":   "1.2.3.4:51820",
		"peer_public_key": validTestKey(),
		"private_key":     validTestKey2(),
		"allowed_ips":     []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"},
	}

	body := map[string]interface{}{
		"config": buildTunnelConfigBase64(cfg),
	}

	resp, err := common.MakeRequest(http.MethodPut, "/network/tunnel/config", body)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Multiple allowed IPs should pass validation
	assert.NotEqual(t, http.StatusUnprocessableEntity, resp.StatusCode)

	if resp.StatusCode == http.StatusOK {
		disconnectResp, err := common.MakeRequest(http.MethodDelete, "/network/tunnel", nil)
		require.NoError(t, err)
		defer disconnectResp.Body.Close()
	}
}
