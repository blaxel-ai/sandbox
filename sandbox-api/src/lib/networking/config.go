package networking

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
)

const (
	// EnvNetworkingConfig is the environment variable name for the base64-encoded WireGuard config
	EnvNetworkingConfig = "BL_NETWORKING_CONFIG"

	// Default values
	DefaultMTU        = 1420
	DefaultListenPort = 51820
	DefaultWgName     = "wg0"
)

// WireGuardConfig represents the WireGuard configuration
type WireGuardConfig struct {
	// LocalIP is the IP address to assign to the WireGuard interface (e.g., "240.1.0.120/32")
	LocalIP string `json:"local_ip"`

	// PeerEndpoint is the peer's endpoint address (e.g., "1.2.3.4:51380")
	PeerEndpoint string `json:"peer_endpoint"`

	// PeerPublicKey is the peer's public key (base64 encoded)
	PeerPublicKey string `json:"peer_public_key"`

	// PrivateKey is the local private key (base64 encoded)
	PrivateKey string `json:"private_key"`

	// MTU is the Maximum Transmission Unit for the interface
	MTU int `json:"mtu,omitempty"`

	// ListenPort is the UDP port to listen on
	ListenPort int `json:"listen_port,omitempty"`

	// InterfaceName is the name for the WireGuard interface (e.g., "wg0")
	InterfaceName string `json:"interface_name,omitempty"`

	// AllowedIPs is the list of IP ranges to route through the tunnel (defaults to "0.0.0.0/0")
	AllowedIPs []string `json:"allowed_ips,omitempty"`

	// PersistentKeepalive is the interval in seconds for keepalive packets.
	// nil means use default (25s), pointer to 0 explicitly disables keepalive.
	PersistentKeepalive *int `json:"persistent_keepalive,omitempty"`

	// RouteAll routes all traffic through the tunnel (sets up default route)
	RouteAll bool `json:"route_all,omitempty"`
}

// LoadConfigFromEnv loads and parses the WireGuard configuration from the environment variable
func LoadConfigFromEnv() (*WireGuardConfig, error) {
	base64Config := os.Getenv(EnvNetworkingConfig)
	if base64Config == "" {
		return nil, nil // No config provided, not an error
	}

	return ParseBase64Config(base64Config)
}

// ParseBase64Config decodes and parses a base64-encoded WireGuard configuration
func ParseBase64Config(base64Config string) (*WireGuardConfig, error) {
	configData, err := base64.StdEncoding.DecodeString(base64Config)
	if err != nil {
		return nil, fmt.Errorf("failed to decode base64 configuration: %w", err)
	}

	var config WireGuardConfig
	if err := json.Unmarshal(configData, &config); err != nil {
		return nil, fmt.Errorf("failed to parse JSON configuration: %w", err)
	}

	// Apply defaults
	config.ApplyDefaults()

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

// ApplyDefaults sets default values for optional fields
func (c *WireGuardConfig) ApplyDefaults() {
	if c.MTU == 0 {
		c.MTU = DefaultMTU
	}
	if c.ListenPort == 0 {
		c.ListenPort = DefaultListenPort
	}
	if c.InterfaceName == "" {
		c.InterfaceName = DefaultWgName
	}
	if len(c.AllowedIPs) == 0 {
		c.AllowedIPs = []string{"0.0.0.0/0"}
	}
	if c.PersistentKeepalive == nil {
		defaultKeepalive := 25
		c.PersistentKeepalive = &defaultKeepalive
	}
}

// Validate checks if the configuration is valid
func (c *WireGuardConfig) Validate() error {
	if c.LocalIP == "" {
		return fmt.Errorf("local_ip is required")
	}
	if _, _, err := net.ParseCIDR(c.LocalIP); err != nil {
		return fmt.Errorf("local_ip must be in CIDR notation (e.g., 10.0.0.1/32): %w", err)
	}

	if c.PeerEndpoint == "" {
		return fmt.Errorf("peer_endpoint is required")
	}
	if _, _, err := net.SplitHostPort(c.PeerEndpoint); err != nil {
		return fmt.Errorf("peer_endpoint must be in host:port format (e.g., 1.2.3.4:51820): %w", err)
	}

	if c.PeerPublicKey == "" {
		return fmt.Errorf("peer_public_key is required")
	}
	if err := validateWireGuardKey(c.PeerPublicKey); err != nil {
		return fmt.Errorf("invalid peer_public_key: %w", err)
	}

	if c.PrivateKey == "" {
		return fmt.Errorf("private_key is required")
	}
	if err := validateWireGuardKey(c.PrivateKey); err != nil {
		return fmt.Errorf("invalid private_key: %w", err)
	}

	for _, ip := range c.AllowedIPs {
		if _, _, err := net.ParseCIDR(ip); err != nil {
			return fmt.Errorf("invalid allowed_ip %q: %w", ip, err)
		}
	}

	if c.MTU < 68 || c.MTU > 65535 {
		return fmt.Errorf("mtu must be between 68 and 65535, got %d", c.MTU)
	}

	return nil
}

// validateWireGuardKey checks that a key is valid base64-encoded 32-byte value
func validateWireGuardKey(key string) error {
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return fmt.Errorf("not valid base64: %w", err)
	}
	if len(decoded) != 32 {
		return fmt.Errorf("expected 32 bytes, got %d", len(decoded))
	}
	return nil
}

// ToJSON returns the configuration as a JSON string for debugging.
// The private key is redacted for security.
func (c *WireGuardConfig) ToJSON() string {
	redacted := *c
	if redacted.PrivateKey != "" {
		redacted.PrivateKey = "[REDACTED]"
	}
	data, _ := json.MarshalIndent(redacted, "", "  ")
	return string(data)
}
