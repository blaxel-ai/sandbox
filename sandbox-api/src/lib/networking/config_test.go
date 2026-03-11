package networking

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// testKey returns a valid base64-encoded 32-byte key for testing
func testKey() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// testKey2 returns a different valid base64-encoded 32-byte key for testing
func testKey2() string {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 128)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// testConfig returns a minimal valid WireGuardConfig for testing.
// Uses distinct keys for PeerPublicKey and PrivateKey so that redaction
// tests can distinguish between them.
func testConfig() WireGuardConfig {
	return WireGuardConfig{
		LocalIP:       "10.0.0.1/32",
		PeerEndpoint:  "1.2.3.4:51820",
		PeerPublicKey: testKey(),
		PrivateKey:    testKey2(),
	}
}

func TestParseBase64Config_Valid(t *testing.T) {
	cfg := testConfig()
	jsonData, _ := json.Marshal(cfg)
	b64 := base64.StdEncoding.EncodeToString(jsonData)

	result, err := ParseBase64Config(b64)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.LocalIP != "10.0.0.1/32" {
		t.Errorf("expected LocalIP 10.0.0.1/32, got %s", result.LocalIP)
	}
	if result.MTU != DefaultMTU {
		t.Errorf("expected default MTU %d, got %d", DefaultMTU, result.MTU)
	}
	if result.ListenPort != DefaultListenPort {
		t.Errorf("expected default ListenPort %d, got %d", DefaultListenPort, result.ListenPort)
	}
	if result.InterfaceName != DefaultWgName {
		t.Errorf("expected default InterfaceName %s, got %s", DefaultWgName, result.InterfaceName)
	}
	if result.PersistentKeepalive == nil || *result.PersistentKeepalive != 25 {
		t.Errorf("expected default PersistentKeepalive 25, got %v", result.PersistentKeepalive)
	}
}

func TestParseBase64Config_InvalidBase64(t *testing.T) {
	_, err := ParseBase64Config("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
	if !strings.Contains(err.Error(), "base64") {
		t.Errorf("expected base64 error, got: %v", err)
	}
}

func TestParseBase64Config_InvalidJSON(t *testing.T) {
	b64 := base64.StdEncoding.EncodeToString([]byte("not json"))
	_, err := ParseBase64Config(b64)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "JSON") {
		t.Errorf("expected JSON error, got: %v", err)
	}
}

func TestApplyDefaults(t *testing.T) {
	cfg := WireGuardConfig{}
	cfg.ApplyDefaults()

	if cfg.MTU != DefaultMTU {
		t.Errorf("expected MTU %d, got %d", DefaultMTU, cfg.MTU)
	}
	if cfg.ListenPort != DefaultListenPort {
		t.Errorf("expected ListenPort %d, got %d", DefaultListenPort, cfg.ListenPort)
	}
	if cfg.InterfaceName != DefaultWgName {
		t.Errorf("expected InterfaceName %s, got %s", DefaultWgName, cfg.InterfaceName)
	}
	if len(cfg.AllowedIPs) != 1 || cfg.AllowedIPs[0] != "0.0.0.0/0" {
		t.Errorf("expected default AllowedIPs, got %v", cfg.AllowedIPs)
	}
	if cfg.PersistentKeepalive == nil || *cfg.PersistentKeepalive != 25 {
		t.Errorf("expected PersistentKeepalive 25, got %v", cfg.PersistentKeepalive)
	}
}

func TestApplyDefaults_DoesNotOverrideExplicitValues(t *testing.T) {
	keepalive := 0
	cfg := WireGuardConfig{
		MTU:                 1500,
		ListenPort:          12345,
		InterfaceName:       "custom0",
		AllowedIPs:          []string{"10.0.0.0/8"},
		PersistentKeepalive: &keepalive,
	}
	cfg.ApplyDefaults()

	if cfg.MTU != 1500 {
		t.Errorf("expected MTU 1500, got %d", cfg.MTU)
	}
	if cfg.ListenPort != 12345 {
		t.Errorf("expected ListenPort 12345, got %d", cfg.ListenPort)
	}
	if cfg.InterfaceName != "custom0" {
		t.Errorf("expected InterfaceName custom0, got %s", cfg.InterfaceName)
	}
	if len(cfg.AllowedIPs) != 1 || cfg.AllowedIPs[0] != "10.0.0.0/8" {
		t.Errorf("expected custom AllowedIPs, got %v", cfg.AllowedIPs)
	}
	// Explicitly set to 0 should NOT be overridden
	if cfg.PersistentKeepalive == nil || *cfg.PersistentKeepalive != 0 {
		t.Errorf("expected PersistentKeepalive 0 (explicit), got %v", cfg.PersistentKeepalive)
	}
}

func TestValidate_ValidConfig(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestValidate_MissingLocalIP(t *testing.T) {
	cfg := testConfig()
	cfg.LocalIP = ""
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for missing local_ip")
	}
	if !strings.Contains(err.Error(), "local_ip") {
		t.Errorf("expected local_ip error, got: %v", err)
	}
}

func TestValidate_InvalidCIDR(t *testing.T) {
	cfg := testConfig()
	cfg.LocalIP = "10.0.0.1" // missing /prefix
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	if !strings.Contains(err.Error(), "CIDR") {
		t.Errorf("expected CIDR error, got: %v", err)
	}
}

func TestValidate_InvalidPeerEndpoint(t *testing.T) {
	cfg := testConfig()
	cfg.PeerEndpoint = "not-a-valid-endpoint"
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid peer_endpoint")
	}
	if !strings.Contains(err.Error(), "peer_endpoint") {
		t.Errorf("expected peer_endpoint error, got: %v", err)
	}
}

func TestValidate_InvalidKey(t *testing.T) {
	cfg := testConfig()
	cfg.PeerPublicKey = "not-base64"
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid key")
	}
	if !strings.Contains(err.Error(), "peer_public_key") {
		t.Errorf("expected peer_public_key error, got: %v", err)
	}
}

func TestValidate_WrongLengthKey(t *testing.T) {
	cfg := testConfig()
	shortKey := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	cfg.PrivateKey = shortKey
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for wrong-length key")
	}
	if !strings.Contains(err.Error(), "private_key") {
		t.Errorf("expected private_key error, got: %v", err)
	}
}

func TestValidate_InvalidAllowedIP(t *testing.T) {
	cfg := testConfig()
	cfg.AllowedIPs = []string{"not-a-cidr"}
	cfg.ApplyDefaults()
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for invalid allowed_ip")
	}
	if !strings.Contains(err.Error(), "allowed_ip") {
		t.Errorf("expected allowed_ip error, got: %v", err)
	}
}

func TestValidate_MTUOutOfRange(t *testing.T) {
	cfg := testConfig()
	cfg.MTU = 10 // below 68
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected error for out-of-range MTU")
	}
	if !strings.Contains(err.Error(), "mtu") {
		t.Errorf("expected mtu error, got: %v", err)
	}
}

func TestValidateWireGuardKey(t *testing.T) {
	tests := []struct {
		name    string
		key     string
		wantErr bool
	}{
		{"valid key", testKey(), false},
		{"invalid base64", "not-base64!!!", true},
		{"wrong length", base64.StdEncoding.EncodeToString([]byte("short")), true},
		{"empty", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateWireGuardKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateWireGuardKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestToJSON_RedactsPrivateKey(t *testing.T) {
	cfg := testConfig()
	output := cfg.ToJSON()

	if strings.Contains(output, cfg.PrivateKey) {
		t.Error("ToJSON output contains the actual private key")
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Error("ToJSON output does not contain [REDACTED] placeholder")
	}
	// PeerPublicKey should still be present
	if !strings.Contains(output, cfg.PeerPublicKey) {
		t.Error("ToJSON output should contain the peer public key")
	}
}

func TestToJSON_DoesNotMutateOriginal(t *testing.T) {
	cfg := testConfig()
	originalKey := cfg.PrivateKey
	_ = cfg.ToJSON()

	if cfg.PrivateKey != originalKey {
		t.Error("ToJSON mutated the original config's PrivateKey")
	}
}
