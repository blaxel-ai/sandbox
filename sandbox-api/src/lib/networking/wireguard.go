package networking

import (
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"syscall"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/crypto/curve25519"
	"golang.zx2c4.com/wireguard/conn"
	"golang.zx2c4.com/wireguard/device"
	"golang.zx2c4.com/wireguard/tun"
)

// WireGuardClient manages a WireGuard connection
type WireGuardClient struct {
	config       *WireGuardConfig
	device       *device.Device
	tunDevice    tun.Device
	publicKey    string
	running      bool
	mutex        sync.Mutex
	defaultGW    net.IP
	defaultIface string
}

// Global WireGuard client instance protected by a mutex.
// Using a mutex instead of sync.Once so that the entire initialization is guarded
// and subsequent calls are safe no-ops without leaking resources.
var (
	wgClient *WireGuardClient
	wgMutex  sync.Mutex
)

// GetWireGuardClient returns the global WireGuard client instance
func GetWireGuardClient() *WireGuardClient {
	wgMutex.Lock()
	defer wgMutex.Unlock()
	return wgClient
}

// StartWireGuardFromEnv initializes and starts the WireGuard client if config is present in env.
// This should be called once at application startup. It is safe to call multiple times;
// subsequent calls are no-ops if a client is already running.
func StartWireGuardFromEnv() error {
	wgMutex.Lock()
	defer wgMutex.Unlock()

	if wgClient != nil {
		logrus.Debug("WireGuard client already initialized, skipping")
		return nil
	}

	config, err := LoadConfigFromEnv()
	if err != nil {
		return fmt.Errorf("failed to load WireGuard config: %w", err)
	}

	if config == nil {
		logrus.Debug("No WireGuard configuration found in environment, skipping initialization")
		return nil
	}

	logrus.Info("WireGuard configuration found, initializing client...")

	client, err := NewWireGuardClient(config)
	if err != nil {
		return fmt.Errorf("failed to create WireGuard client: %w", err)
	}

	if err := client.Start(); err != nil {
		return fmt.Errorf("failed to start WireGuard client: %w", err)
	}

	wgClient = client
	return nil
}

// StopWireGuard stops the global WireGuard client and cleans up resources.
// Returns an error if no client is running.
func StopWireGuard() error {
	wgMutex.Lock()
	defer wgMutex.Unlock()

	if wgClient == nil {
		return fmt.Errorf("no WireGuard client is running")
	}

	if err := wgClient.Stop(); err != nil {
		return fmt.Errorf("failed to stop WireGuard client: %w", err)
	}
	wgClient = nil
	return nil
}

// UpdateWireGuardConfig stops the current WireGuard client (if any) and starts a new one
// with the provided configuration.
func UpdateWireGuardConfig(config *WireGuardConfig) error {
	wgMutex.Lock()
	defer wgMutex.Unlock()

	// Stop existing client if running
	if wgClient != nil {
		logrus.Info("Stopping existing WireGuard client for config update")
		if err := wgClient.Stop(); err != nil {
			return fmt.Errorf("failed to stop existing WireGuard client: %w", err)
		}
		wgClient = nil
	}

	client, err := NewWireGuardClient(config)
	if err != nil {
		return fmt.Errorf("failed to create WireGuard client: %w", err)
	}

	if err := client.Start(); err != nil {
		return fmt.Errorf("failed to start WireGuard client: %w", err)
	}

	wgClient = client
	return nil
}

// NewWireGuardClient creates a new WireGuard client with the given configuration
func NewWireGuardClient(config *WireGuardConfig) (*WireGuardClient, error) {
	return &WireGuardClient{
		config: config,
	}, nil
}

// Start initializes and starts the WireGuard interface
func (w *WireGuardClient) Start() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.running {
		return fmt.Errorf("WireGuard client is already running")
	}

	logrus.WithFields(logrus.Fields{
		"interface":     w.config.InterfaceName,
		"local_ip":      w.config.LocalIP,
		"peer_endpoint": w.config.PeerEndpoint,
		"mtu":           w.config.MTU,
	}).Info("Starting WireGuard client")

	// Derive public key from private key
	privateKey := w.config.PrivateKey
	var err error
	w.publicKey, err = derivePublicKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to derive public key: %w", err)
	}
	logrus.WithField("public_key", w.publicKey).Debug("Derived public key from private key")

	// Create TUN device
	tunDev, err := tun.CreateTUN(w.config.InterfaceName, w.config.MTU)
	if err != nil {
		return fmt.Errorf("failed to create TUN device: %w", err)
	}
	w.tunDevice = tunDev

	// Get the real interface name (might differ from requested on some platforms)
	realName, err := tunDev.Name()
	if err != nil {
		_ = w.tunDevice.Close()
		return fmt.Errorf("failed to get TUN device name: %w", err)
	}
	logrus.WithField("tun_name", realName).Debug("Created TUN device")

	// Create WireGuard device (only log errors, not verbose/debug messages)
	logger := &device.Logger{
		Verbosef: func(format string, args ...interface{}) {
			// Suppress verbose WireGuard logs (keepalive, handshake, etc.)
		},
		Errorf: func(format string, args ...interface{}) {
			logrus.Errorf("[WireGuard] "+format, args...)
		},
	}

	w.device = device.NewDevice(tunDev, conn.NewDefaultBind(), logger)

	// Build and apply IPC configuration
	ipcConfig, err := w.buildIPCConfig(privateKey)
	if err != nil {
		w.device.Close()
		return fmt.Errorf("failed to build IPC config: %w", err)
	}
	if err := w.device.IpcSet(ipcConfig); err != nil {
		w.device.Close()
		return fmt.Errorf("failed to configure WireGuard device: %w", err)
	}

	// Bring up the device
	if err := w.device.Up(); err != nil {
		w.device.Close()
		return fmt.Errorf("failed to bring up WireGuard device: %w", err)
	}

	// Configure network interface (IP address, routes) using netlink
	if err := w.configureNetwork(realName); err != nil {
		w.device.Close()
		return fmt.Errorf("failed to configure network: %w", err)
	}

	w.running = true

	logrus.WithFields(logrus.Fields{
		"interface":  realName,
		"public_key": w.publicKey,
	}).Info("WireGuard client started successfully")

	return nil
}

// Stop shuts down the WireGuard client
func (w *WireGuardClient) Stop() error {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if !w.running {
		return nil
	}

	logrus.Info("Stopping WireGuard client")

	// Remove routes before shutting down
	if w.config.RouteAll {
		w.removeRoutes()
	}

	if w.device != nil {
		w.device.Close()
	}

	w.running = false
	logrus.Info("WireGuard client stopped")

	return nil
}

// GetPublicKey returns the local public key
func (w *WireGuardClient) GetPublicKey() string {
	return w.publicKey
}

// IsRunning returns whether the WireGuard client is running
func (w *WireGuardClient) IsRunning() bool {
	w.mutex.Lock()
	defer w.mutex.Unlock()
	return w.running
}

// buildIPCConfig creates the IPC configuration string for WireGuard
func (w *WireGuardClient) buildIPCConfig(privateKey string) (string, error) {
	var config strings.Builder

	// Interface configuration
	privHex, err := hexEncode(privateKey)
	if err != nil {
		return "", fmt.Errorf("failed to encode private key: %w", err)
	}
	config.WriteString(fmt.Sprintf("private_key=%s\n", privHex))
	config.WriteString(fmt.Sprintf("listen_port=%d\n", w.config.ListenPort))

	// Peer configuration
	pubHex, err := hexEncode(w.config.PeerPublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to encode peer public key: %w", err)
	}
	config.WriteString(fmt.Sprintf("public_key=%s\n", pubHex))
	config.WriteString(fmt.Sprintf("endpoint=%s\n", w.config.PeerEndpoint))

	// Allowed IPs
	for _, allowedIP := range w.config.AllowedIPs {
		config.WriteString(fmt.Sprintf("allowed_ip=%s\n", allowedIP))
	}

	// Persistent keepalive
	if w.config.PersistentKeepalive != nil && *w.config.PersistentKeepalive > 0 {
		config.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", *w.config.PersistentKeepalive))
	}

	return config.String(), nil
}

// configureNetwork sets up the IP address and routing for the WireGuard interface using netlink
func (w *WireGuardClient) configureNetwork(interfaceName string) error {
	// Get the link by name
	link, err := netlink.LinkByName(interfaceName)
	if err != nil {
		return fmt.Errorf("failed to find interface %s: %w", interfaceName, err)
	}

	// Set MTU
	if err := netlink.LinkSetMTU(link, w.config.MTU); err != nil {
		logrus.WithError(err).Warn("Failed to set MTU")
	}

	// Parse the local IP address
	addr, err := netlink.ParseAddr(w.config.LocalIP)
	if err != nil {
		return fmt.Errorf("failed to parse local IP %s: %w", w.config.LocalIP, err)
	}

	// Add IP address to interface
	if err := netlink.AddrAdd(link, addr); err != nil {
		return fmt.Errorf("failed to add IP address to interface: %w", err)
	}

	// Bring up the interface
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("failed to bring up interface: %w", err)
	}

	logrus.WithFields(logrus.Fields{
		"interface": interfaceName,
		"address":   w.config.LocalIP,
		"mtu":       w.config.MTU,
	}).Info("Interface configured")

	// Set up routing if RouteAll is enabled
	if w.config.RouteAll {
		if err := w.setupRoutes(link); err != nil {
			return fmt.Errorf("failed to set up routes: %w", err)
		}
	}

	return nil
}

// setupRoutes configures routing to send all traffic through the WireGuard tunnel.
//
// Uses the wg-quick approach: two half-default routes (0.0.0.0/1 and 128.0.0.0/1) that
// are more specific than the existing 0.0.0.0/0 default route, so they take precedence
// without needing to delete or modify the original default route. This avoids race
// conditions with the container runtime or DHCP client re-adding routes.
func (w *WireGuardClient) setupRoutes(wgLink netlink.Link) error {
	// Parse peer endpoint IP (handles both IPv4 and IPv6 endpoints)
	peerIP, err := parsePeerEndpoint(w.config.PeerEndpoint)
	if err != nil {
		return err
	}

	// Get current default gateway
	defaultGW, defaultIface, err := getDefaultGateway()
	if err != nil {
		logrus.WithError(err).Warn("Could not detect default gateway, skipping route setup")
		return nil
	}

	// Store for later cleanup
	w.defaultGW = defaultGW
	w.defaultIface = defaultIface

	logrus.WithFields(logrus.Fields{
		"default_gw":    defaultGW.String(),
		"default_iface": defaultIface,
		"peer_ip":       peerIP.String(),
	}).Info("Setting up routes")

	// Get the primary interface link
	primaryLink, err := netlink.LinkByName(defaultIface)
	if err != nil {
		return fmt.Errorf("failed to find primary interface %s: %w", defaultIface, err)
	}

	// STEP 1: Add explicit route to peer endpoint via original gateway.
	// This ensures WireGuard UDP traffic always uses the physical interface.
	peerRoute := &netlink.Route{
		Dst: &net.IPNet{
			IP:   peerIP,
			Mask: peerHostMask(peerIP),
		},
		Gw:        defaultGW,
		LinkIndex: primaryLink.Attrs().Index,
	}
	if err := netlink.RouteAdd(peerRoute); err != nil {
		logrus.WithError(err).Warn("Failed to add route to peer endpoint (may already exist)")
	} else {
		logrus.WithField("peer_ip", peerIP.String()).Info("Added route to peer endpoint")
	}

	// STEP 2: Add two half-default routes via WireGuard interface.
	// 0.0.0.0/1 and 128.0.0.0/1 together cover all IPv4 addresses and are more specific
	// than the existing 0.0.0.0/0 default route, so they take priority automatically.
	// This is the same approach used by wg-quick.
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, dst, _ := net.ParseCIDR(cidr)
		route := &netlink.Route{
			Dst:       dst,
			LinkIndex: wgLink.Attrs().Index,
		}
		if err := netlink.RouteAdd(route); err != nil {
			return fmt.Errorf("failed to add route %s via WireGuard: %w", cidr, err)
		}
		logrus.WithField("route", cidr).Info("Added WireGuard route")
	}

	logrus.Info("Routes configured successfully")
	return nil
}

// removeRoutes removes the routes set up by setupRoutes
func (w *WireGuardClient) removeRoutes() {
	if w.defaultGW == nil {
		return
	}

	realName, err := w.tunDevice.Name()
	if err != nil {
		logrus.WithError(err).Warn("Failed to get TUN device name for route cleanup")
		return
	}

	wgLink, err := netlink.LinkByName(realName)
	if err != nil {
		logrus.WithError(err).Warn("Failed to find WireGuard interface for route cleanup")
		return
	}

	// Remove the two half-default routes
	for _, cidr := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		_, dst, _ := net.ParseCIDR(cidr)
		route := &netlink.Route{
			Dst:       dst,
			LinkIndex: wgLink.Attrs().Index,
		}
		if err := netlink.RouteDel(route); err != nil {
			logrus.WithError(err).Warnf("Failed to remove WireGuard route %s", cidr)
		}
	}

	// Remove peer endpoint route
	peerIP, err := parsePeerEndpoint(w.config.PeerEndpoint)
	if err != nil {
		logrus.WithError(err).Warn("Failed to parse peer endpoint for route cleanup")
		return
	}

	primaryLink, err := netlink.LinkByName(w.defaultIface)
	if err != nil {
		logrus.WithError(err).Warn("Failed to find primary interface for route cleanup")
		return
	}

	peerRoute := &netlink.Route{
		Dst: &net.IPNet{
			IP:   peerIP,
			Mask: peerHostMask(peerIP),
		},
		Gw:        w.defaultGW,
		LinkIndex: primaryLink.Attrs().Index,
	}
	if err := netlink.RouteDel(peerRoute); err != nil {
		logrus.WithError(err).Warn("Failed to remove peer endpoint route")
	}

	logrus.Info("Routes cleaned up")
}

// parsePeerEndpoint extracts the host IP from a peer endpoint string.
// Handles both IPv4 (1.2.3.4:51820) and IPv6 ([2001:db8::1]:51820) formats.
func parsePeerEndpoint(endpoint string) (net.IP, error) {
	host, _, err := net.SplitHostPort(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid peer endpoint %q: %w", endpoint, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, fmt.Errorf("invalid IP in peer endpoint: %s", host)
	}
	return ip, nil
}

// peerHostMask returns the appropriate host mask for an IP address
// (/32 for IPv4, /128 for IPv6)
func peerHostMask(ip net.IP) net.IPMask {
	if ip.To4() != nil {
		return net.CIDRMask(32, 32)
	}
	return net.CIDRMask(128, 128)
}

// derivePublicKey derives a Curve25519 public key from a base64-encoded private key
func derivePublicKey(privateKeyBase64 string) (string, error) {
	privateBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}

	if len(privateBytes) != 32 {
		return "", fmt.Errorf("invalid private key length: expected 32, got %d", len(privateBytes))
	}

	publicBytes, err := curve25519.X25519(privateBytes, curve25519.Basepoint)
	if err != nil {
		return "", fmt.Errorf("failed to compute public key: %w", err)
	}

	return base64.StdEncoding.EncodeToString(publicBytes), nil
}

// hexEncode converts a base64-encoded key to hex encoding (required by WireGuard IPC)
func hexEncode(base64Key string) (string, error) {
	keyBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return "", fmt.Errorf("failed to decode base64 key: %w", err)
	}
	return fmt.Sprintf("%x", keyBytes), nil
}

// isDefaultRoute checks if a netlink route is a default route (0.0.0.0/0)
func isDefaultRoute(route netlink.Route) bool {
	return route.Dst == nil ||
		(route.Dst != nil && route.Dst.IP.Equal(net.IPv4zero) && route.Dst.Mask.String() == "00000000")
}

// getDefaultGateway returns the default gateway IP and interface name using netlink
func getDefaultGateway() (net.IP, string, error) {
	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list routes: %w", err)
	}

	for _, route := range routes {
		if !isDefaultRoute(route) || route.Gw == nil {
			continue
		}
		// Get interface name
		link, err := netlink.LinkByIndex(route.LinkIndex)
		if err != nil {
			logrus.WithError(err).WithField("link_index", route.LinkIndex).Debug("Failed to get link by index")
			continue
		}
		logrus.WithFields(logrus.Fields{
			"gateway":   route.Gw.String(),
			"interface": link.Attrs().Name,
		}).Debug("Found default gateway")
		return route.Gw, link.Attrs().Name, nil
	}

	return nil, "", fmt.Errorf("default gateway not found")
}

// GetStatus returns the current status of the WireGuard client including operational metrics
func (w *WireGuardClient) GetStatus() map[string]interface{} {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	status := map[string]interface{}{
		"running":       w.running,
		"public_key":    w.publicKey,
		"interface":     w.config.InterfaceName,
		"local_ip":      w.config.LocalIP,
		"peer_endpoint": w.config.PeerEndpoint,
		"mtu":           w.config.MTU,
		"listen_port":   w.config.ListenPort,
	}

	// Query operational stats from the WireGuard device via IPC
	if w.device != nil && w.running {
		ipcOutput, err := w.device.IpcGet()
		if err == nil {
			for k, v := range parseIPCStats(ipcOutput) {
				status[k] = v
			}
		}
	}

	return status
}

// parseIPCStats extracts operational metrics from WireGuard IPC output
func parseIPCStats(ipcOutput string) map[string]string {
	stats := make(map[string]string)
	for _, line := range strings.Split(ipcOutput, "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "rx_bytes", "tx_bytes", "last_handshake_time_sec", "last_handshake_time_nsec":
			stats[parts[0]] = parts[1]
		}
	}
	return stats
}
