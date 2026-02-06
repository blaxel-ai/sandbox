package networking

import (
	"encoding/base64"
	"net"
	"testing"
)

func TestHexEncode_Valid(t *testing.T) {
	// 32 bytes of known data
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	b64Key := base64.StdEncoding.EncodeToString(key)

	hex, err := hexEncode(b64Key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	if hex != expected {
		t.Errorf("expected %s, got %s", expected, hex)
	}
}

func TestHexEncode_InvalidBase64(t *testing.T) {
	_, err := hexEncode("not-valid-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDerivePublicKey_Valid(t *testing.T) {
	// Use a known private key
	privateKey := testKey()
	pubKey, err := derivePublicKey(privateKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Public key should be valid base64 and 32 bytes
	decoded, err := base64.StdEncoding.DecodeString(pubKey)
	if err != nil {
		t.Fatalf("public key is not valid base64: %v", err)
	}
	if len(decoded) != 32 {
		t.Errorf("expected 32-byte public key, got %d bytes", len(decoded))
	}

	// Same private key should always produce the same public key
	pubKey2, err := derivePublicKey(privateKey)
	if err != nil {
		t.Fatalf("unexpected error on second call: %v", err)
	}
	if pubKey != pubKey2 {
		t.Error("derivePublicKey is not deterministic")
	}
}

func TestDerivePublicKey_InvalidBase64(t *testing.T) {
	_, err := derivePublicKey("not-base64!!!")
	if err == nil {
		t.Fatal("expected error for invalid base64")
	}
}

func TestDerivePublicKey_WrongLength(t *testing.T) {
	shortKey := base64.StdEncoding.EncodeToString([]byte("tooshort"))
	_, err := derivePublicKey(shortKey)
	if err == nil {
		t.Fatal("expected error for wrong-length key")
	}
}

func TestParsePeerEndpoint_IPv4(t *testing.T) {
	ip, err := parsePeerEndpoint("1.2.3.4:51820")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip.String() != "1.2.3.4" {
		t.Errorf("expected 1.2.3.4, got %s", ip.String())
	}
}

func TestParsePeerEndpoint_IPv6(t *testing.T) {
	ip, err := parsePeerEndpoint("[2001:db8::1]:51820")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ip.String() != "2001:db8::1" {
		t.Errorf("expected 2001:db8::1, got %s", ip.String())
	}
}

func TestParsePeerEndpoint_Invalid(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
	}{
		{"no port", "1.2.3.4"},
		{"empty", ""},
		{"just colon", ":"},
		{"no host", ":51820"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parsePeerEndpoint(tt.endpoint)
			if err == nil {
				t.Errorf("expected error for endpoint %q", tt.endpoint)
			}
		})
	}
}

func TestPeerHostMask_IPv4(t *testing.T) {
	ip := testIPv4()
	mask := peerHostMask(ip)
	ones, bits := mask.Size()
	if ones != 32 || bits != 32 {
		t.Errorf("expected /32, got /%d (bits=%d)", ones, bits)
	}
}

func TestPeerHostMask_IPv6(t *testing.T) {
	ip := testIPv6()
	mask := peerHostMask(ip)
	ones, bits := mask.Size()
	if ones != 128 || bits != 128 {
		t.Errorf("expected /128, got /%d (bits=%d)", ones, bits)
	}
}

func TestParseIPCStats(t *testing.T) {
	ipcOutput := `private_key=abcdef0123456789
listen_port=51820
public_key=fedcba9876543210
endpoint=1.2.3.4:51820
allowed_ip=0.0.0.0/0
last_handshake_time_sec=1234567890
last_handshake_time_nsec=123456789
rx_bytes=1024
tx_bytes=2048
persistent_keepalive_interval=25`

	stats := parseIPCStats(ipcOutput)

	if stats["rx_bytes"] != "1024" {
		t.Errorf("expected rx_bytes=1024, got %s", stats["rx_bytes"])
	}
	if stats["tx_bytes"] != "2048" {
		t.Errorf("expected tx_bytes=2048, got %s", stats["tx_bytes"])
	}
	if stats["last_handshake_time_sec"] != "1234567890" {
		t.Errorf("expected last_handshake_time_sec=1234567890, got %s", stats["last_handshake_time_sec"])
	}
	if stats["last_handshake_time_nsec"] != "123456789" {
		t.Errorf("expected last_handshake_time_nsec=123456789, got %s", stats["last_handshake_time_nsec"])
	}

	// Should NOT include private keys or other sensitive fields
	if _, ok := stats["private_key"]; ok {
		t.Error("stats should not include private_key")
	}
	if _, ok := stats["public_key"]; ok {
		t.Error("stats should not include public_key")
	}
}

func TestParseIPCStats_Empty(t *testing.T) {
	stats := parseIPCStats("")
	if len(stats) != 0 {
		t.Errorf("expected empty stats, got %v", stats)
	}
}

func TestBuildIPCConfig(t *testing.T) {
	keepalive := 25
	cfg := &WireGuardConfig{
		PeerPublicKey:       testKey(),
		PeerEndpoint:        "1.2.3.4:51820",
		ListenPort:          51820,
		AllowedIPs:          []string{"0.0.0.0/0"},
		PersistentKeepalive: &keepalive,
	}
	client := &WireGuardClient{config: cfg}

	ipc, err := client.buildIPCConfig(testKey())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain key fields
	if !contains(ipc, "private_key=") {
		t.Error("IPC config missing private_key")
	}
	if !contains(ipc, "public_key=") {
		t.Error("IPC config missing public_key")
	}
	if !contains(ipc, "endpoint=1.2.3.4:51820") {
		t.Error("IPC config missing endpoint")
	}
	if !contains(ipc, "allowed_ip=0.0.0.0/0") {
		t.Error("IPC config missing allowed_ip")
	}
	if !contains(ipc, "persistent_keepalive_interval=25") {
		t.Error("IPC config missing persistent_keepalive_interval")
	}
	if !contains(ipc, "listen_port=51820") {
		t.Error("IPC config missing listen_port")
	}
}

func TestBuildIPCConfig_KeepaliveDisabled(t *testing.T) {
	keepalive := 0
	cfg := &WireGuardConfig{
		PeerPublicKey:       testKey(),
		PeerEndpoint:        "1.2.3.4:51820",
		ListenPort:          51820,
		AllowedIPs:          []string{"0.0.0.0/0"},
		PersistentKeepalive: &keepalive,
	}
	client := &WireGuardClient{config: cfg}

	ipc, err := client.buildIPCConfig(testKey())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if contains(ipc, "persistent_keepalive_interval") {
		t.Error("IPC config should not contain persistent_keepalive_interval when disabled (0)")
	}
}

func TestBuildIPCConfig_InvalidKey(t *testing.T) {
	cfg := &WireGuardConfig{
		PeerPublicKey: "not-valid-base64!!!",
		PeerEndpoint:  "1.2.3.4:51820",
		ListenPort:    51820,
		AllowedIPs:    []string{"0.0.0.0/0"},
	}
	client := &WireGuardClient{config: cfg}

	_, err := client.buildIPCConfig(testKey())
	if err == nil {
		t.Fatal("expected error for invalid peer public key")
	}
}

// ==================== StopWireGuard Tests ====================

// setGlobalClient is a test helper that sets the global wgClient and returns
// a cleanup function that restores the previous value.
func setGlobalClient(t *testing.T, client *WireGuardClient) func() {
	t.Helper()
	wgMutex.Lock()
	saved := wgClient
	wgClient = client
	wgMutex.Unlock()
	return func() {
		wgMutex.Lock()
		wgClient = saved
		wgMutex.Unlock()
	}
}

func TestStopWireGuard_NoClientRunning(t *testing.T) {
	restore := setGlobalClient(t, nil)
	defer restore()

	err := StopWireGuard()
	if err == nil {
		t.Fatal("expected error when no client is running")
	}
	if !contains(err.Error(), "no WireGuard client is running") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestStopWireGuard_ClientNotRunning(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	// client.running is false (never started)

	restore := setGlobalClient(t, client)
	defer restore()

	err := StopWireGuard()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Global should be cleared
	if GetWireGuardClient() != nil {
		t.Error("expected wgClient to be nil after StopWireGuard")
	}
}

func TestStopWireGuard_ClientRunning(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	// Simulate a running state (without actually starting TUN)
	client.running = true

	restore := setGlobalClient(t, client)
	defer restore()

	err := StopWireGuard()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.IsRunning() {
		t.Error("client should not be running after StopWireGuard")
	}
	if GetWireGuardClient() != nil {
		t.Error("expected wgClient to be nil after StopWireGuard")
	}
}

func TestStopWireGuard_WithRouteAll(t *testing.T) {
	cfg := testConfig()
	cfg.RouteAll = true
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	client.running = true
	// defaultGW is nil so removeRoutes will no-op safely

	restore := setGlobalClient(t, client)
	defer restore()

	err := StopWireGuard()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client.IsRunning() {
		t.Error("client should not be running after StopWireGuard")
	}
	if GetWireGuardClient() != nil {
		t.Error("expected wgClient to be nil after StopWireGuard")
	}
}

func TestStopWireGuard_CalledTwice(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	client.running = true

	restore := setGlobalClient(t, client)
	defer restore()

	// First stop should succeed
	err := StopWireGuard()
	if err != nil {
		t.Fatalf("unexpected error on first stop: %v", err)
	}

	// Second stop should return error (nil client)
	err = StopWireGuard()
	if err == nil {
		t.Fatal("expected error on second StopWireGuard call")
	}
	if !contains(err.Error(), "no WireGuard client is running") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// ==================== Client Stop Tests ====================

func TestClientStop_SetsRunningFalse(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	client.running = true

	if !client.IsRunning() {
		t.Fatal("expected IsRunning=true before stop")
	}

	err := client.Stop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if client.IsRunning() {
		t.Error("expected IsRunning=false after stop")
	}
}

func TestClientStop_IdempotentWhenNotRunning(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	// Never started, running=false

	err := client.Stop()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Call again - still a no-op
	err = client.Stop()
	if err != nil {
		t.Fatalf("unexpected error on second stop: %v", err)
	}
}

func TestClientStop_WithNilDevice(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	client.running = true
	client.device = nil

	err := client.Stop()
	if err != nil {
		t.Fatalf("unexpected error stopping client with nil device: %v", err)
	}
	if client.IsRunning() {
		t.Error("expected not running after stop")
	}
}

// ==================== UpdateWireGuardConfig Tests ====================

func TestUpdateWireGuardConfig_NoExistingClient(t *testing.T) {
	restore := setGlobalClient(t, nil)
	defer restore()

	cfg := testConfig()
	cfg.ApplyDefaults()

	// UpdateWireGuardConfig will call Start() which tries to create a TUN device.
	// On macOS/non-root this will fail, but we verify the flow doesn't panic
	// and the error is about starting (not stopping).
	err := UpdateWireGuardConfig(&cfg)

	if err == nil {
		// If it somehow succeeded (running as root), clean up
		_ = StopWireGuard()
	} else {
		// The error should be about starting the client, not stopping
		if contains(err.Error(), "failed to stop") {
			t.Errorf("should not fail on stopping when no existing client, got: %v", err)
		}
		// Should be about TUN creation or similar
		if !contains(err.Error(), "failed to start") && !contains(err.Error(), "failed to create") {
			t.Logf("Got error (expected on non-root): %v", err)
		}
	}
}

func TestUpdateWireGuardConfig_StopsExistingClient(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	existingClient, _ := NewWireGuardClient(&cfg)
	existingClient.running = true

	restore := setGlobalClient(t, existingClient)
	defer restore()

	newCfg := testConfig()
	newCfg.LocalIP = "10.0.0.2/32"
	newCfg.ApplyDefaults()

	// UpdateWireGuardConfig will stop existing, then try Start() on new.
	// Start() will fail (no TUN on macOS), but existing should be stopped.
	_ = UpdateWireGuardConfig(&newCfg)

	// Existing client must have been stopped
	if existingClient.IsRunning() {
		t.Error("existing client should have been stopped during update")
	}
}

func TestUpdateWireGuardConfig_ClearsGlobalOnStopPhase(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	existingClient, _ := NewWireGuardClient(&cfg)
	existingClient.running = true

	restore := setGlobalClient(t, existingClient)
	defer restore()

	newCfg := testConfig()
	newCfg.ApplyDefaults()

	// Update will stop existing, clear global, then try Start (which fails).
	// After the failure, wgClient should still be nil (old one cleared, new one failed).
	err := UpdateWireGuardConfig(&newCfg)

	if err == nil {
		// If somehow Start succeeded, the global should point to new client
		if GetWireGuardClient() == existingClient {
			t.Error("global should not point to old client after successful update")
		}
		_ = StopWireGuard()
	} else {
		// After failed Start, the global should be nil (old stopped, new failed)
		if GetWireGuardClient() == existingClient {
			t.Error("global should not point to old client after update attempt")
		}
	}
}

// ==================== GetStatus with running state ====================

func TestGetStatus_BeforeAndAfterStop(t *testing.T) {
	cfg := testConfig()
	cfg.ApplyDefaults()
	client, _ := NewWireGuardClient(&cfg)
	client.running = true

	// Before stop
	status := client.GetStatus()
	if status["running"] != true {
		t.Errorf("expected running=true before stop, got %v", status["running"])
	}

	_ = client.Stop()

	// After stop
	status = client.GetStatus()
	if status["running"] != false {
		t.Errorf("expected running=false after stop, got %v", status["running"])
	}
}

// ==================== buildIPCConfig with different configs (update scenario) ====================

func TestBuildIPCConfig_DifferentConfigs(t *testing.T) {
	cfg1 := testConfig()
	cfg1.ListenPort = 11111
	cfg1.ApplyDefaults()
	client1, _ := NewWireGuardClient(&cfg1)

	cfg2 := testConfig()
	cfg2.ListenPort = 22222
	cfg2.ApplyDefaults()
	client2, _ := NewWireGuardClient(&cfg2)

	ipc1, err := client1.buildIPCConfig(cfg1.PrivateKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ipc2, err := client2.buildIPCConfig(cfg2.PrivateKey)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !contains(ipc1, "listen_port=11111") {
		t.Errorf("client1 IPC should contain listen_port=11111")
	}
	if !contains(ipc2, "listen_port=22222") {
		t.Errorf("client2 IPC should contain listen_port=22222")
	}
	if contains(ipc1, "listen_port=22222") {
		t.Error("client1 IPC should NOT contain client2's listen port")
	}
	if contains(ipc2, "listen_port=11111") {
		t.Error("client2 IPC should NOT contain client1's listen port")
	}
}

// helpers

func testIPv4() net.IP {
	ip, _ := parsePeerEndpoint("1.2.3.4:51820")
	return ip
}

func testIPv6() net.IP {
	ip, _ := parsePeerEndpoint("[2001:db8::1]:51820")
	return ip
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
