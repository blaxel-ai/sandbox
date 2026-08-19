//go:build linux

package networking

import (
	"encoding/base64"
	"errors"
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
	stopMonitor  chan struct{}
	// tunnelDsts are the prefixes routed into the tunnel, one per allowed IP.
	tunnelDsts []*net.IPNet
	// replacedDefaults are the pre-existing default routes taken off the
	// physical interface, kept verbatim so they can be restored on teardown.
	replacedDefaults []*netlink.Route
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
		logrus.WithError(err).Error("WireGuard configuration was provided but could not be loaded")
		return fmt.Errorf("failed to load WireGuard config: %w", err)
	}

	if config == nil {
		logrus.Debug("No WireGuard configuration found in environment, skipping initialization")
		return nil
	}

	logrus.WithFields(logrus.Fields{
		"interface":     config.InterfaceName,
		"peer_endpoint": config.PeerEndpoint,
		"local_ip":      config.LocalIP,
		"route_all":     config.RouteAll,
	}).Info("WireGuard configuration found, initializing client...")

	client, err := NewWireGuardClient(config)
	if err != nil {
		logrus.WithError(err).Error("Failed to create WireGuard client")
		return fmt.Errorf("failed to create WireGuard client: %w", err)
	}

	if err := client.Start(); err != nil {
		logrus.WithError(err).Error("Failed to start WireGuard client")
		return fmt.Errorf("failed to start WireGuard client: %w", err)
	}

	wgClient = client
	logrus.Info("WireGuard client initialized successfully - outbound internet connectivity is available")
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
	logrus.Warn("WireGuard tunnel disconnected - outbound internet connectivity is no longer available (no egress)")
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

	// Stop route monitor if running
	if w.stopMonitor != nil {
		close(w.stopMonitor)
		w.stopMonitor = nil
	}

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

		// Start route monitor to handle snapshot resume scenarios
		w.stopMonitor = make(chan struct{})
		go w.monitorRoutes(link)
	}

	return nil
}

// setupRoutes routes the tunnel's allowed IPs through the WireGuard interface,
// one route per allowed prefix, and takes the matching default routes off the
// physical interface. Each family is handled on its own: an IPv6-only sandbox
// tunnelling IPv4 has no IPv4 default route to move, and its IPv6 default must
// stay in place to carry the tunnel's own UDP packets.
func (w *WireGuardClient) setupRoutes(wgLink netlink.Link) error {
	// Parse peer endpoint IP (handles both IPv4 and IPv6 endpoints)
	peerIP, err := parsePeerEndpoint(w.config.PeerEndpoint)
	if err != nil {
		return err
	}

	dsts, err := parseTunnelRoutes(w.config.AllowedIPs)
	if err != nil {
		return err
	}

	// Pin the peer endpoint to the physical interface first, so the tunnel's
	// own UDP keeps flowing once its family's default route moves to wg0.
	peerPinned := w.pinPeerEndpoint(peerIP)

	for _, dst := range dsts {
		if isDefaultPrefix(dst) {
			// Without a pinned peer route, the peer is only reachable through
			// its family's default route: moving that onto the tunnel would
			// black-hole the tunnel itself, so leave the family alone.
			if ipFamily(dst.IP) == ipFamily(peerIP) && !peerPinned {
				logrus.WithField("prefix", dst.String()).
					Warn("Peer endpoint is not pinned to the physical interface, leaving this default route alone")
				continue
			}
			w.detachDefaultRoutes(ipFamily(dst.IP), wgLink.Attrs().Index)
		}
		route := &netlink.Route{Dst: dst, LinkIndex: wgLink.Attrs().Index}
		if err := netlink.RouteAdd(route); err != nil {
			return fmt.Errorf("failed to add route %s via WireGuard: %w", dst.String(), err)
		}
		w.tunnelDsts = append(w.tunnelDsts, dst)
		logrus.WithField("prefix", dst.String()).Info("Added route via WireGuard")
	}

	logrus.Info("Routes configured successfully")
	return nil
}

// pinPeerEndpoint installs a host route to the peer endpoint via the default
// gateway of the endpoint's own address family. It reports whether the peer is
// pinned to the physical interface afterwards.
func (w *WireGuardClient) pinPeerEndpoint(peerIP net.IP) bool {
	gw, iface, err := getDefaultGateway(ipFamily(peerIP))
	if err != nil {
		logrus.WithError(err).WithField("peer_ip", peerIP.String()).
			Warn("No default gateway for the peer endpoint family, not pinning the peer route")
		return false
	}

	primaryLink, err := netlink.LinkByName(iface)
	if err != nil {
		logrus.WithError(err).WithField("interface", iface).Warn("Failed to find primary interface")
		return false
	}

	peerRoute := &netlink.Route{
		Dst:       &net.IPNet{IP: peerIP, Mask: peerHostMask(peerIP)},
		Gw:        gw,
		LinkIndex: primaryLink.Attrs().Index,
	}
	if err := netlink.RouteAdd(peerRoute); err != nil && !errors.Is(err, syscall.EEXIST) {
		logrus.WithError(err).Warn("Failed to add route to peer endpoint")
		return false
	}

	// Stored for cleanup and for the route monitor, only once the peer really
	// is reachable off-tunnel.
	w.defaultGW = gw
	w.defaultIface = iface

	logrus.WithFields(logrus.Fields{
		"peer_ip": peerIP.String(),
		"gateway": gw.String(),
		"device":  iface,
	}).Info("Pinned peer endpoint to the physical interface")
	return true
}

// detachDefaultRoutes removes every default route of the given family that does
// not already point at the tunnel, remembering them for restoration.
func (w *WireGuardClient) detachDefaultRoutes(family, wgLinkIndex int) {
	routes, err := netlink.RouteList(nil, family)
	if err != nil {
		logrus.WithError(err).Warn("Failed to list routes, not detaching default routes")
		return
	}

	for _, route := range routes {
		if !isDefaultRoute(route) || route.LinkIndex == wgLinkIndex {
			continue
		}
		if err := netlink.RouteDel(&route); err != nil {
			logrus.WithError(err).WithField("gw", route.Gw).Warn("Failed to delete original default route")
			continue
		}
		w.replacedDefaults = append(w.replacedDefaults, &route)
		logrus.WithField("gw", route.Gw).Info("Deleted original default route")
	}
}

// removeRoutes removes the routes set up by setupRoutes and restores the
// default routes it took off the physical interface.
func (w *WireGuardClient) removeRoutes() {
	if len(w.tunnelDsts) > 0 {
		realName, err := w.tunDevice.Name()
		if err != nil {
			logrus.WithError(err).Warn("Failed to get TUN device name for route cleanup")
		} else if wgLink, err := netlink.LinkByName(realName); err != nil {
			logrus.WithError(err).Warn("Failed to find WireGuard interface for route cleanup")
		} else {
			for _, dst := range w.tunnelDsts {
				route := &netlink.Route{Dst: dst, LinkIndex: wgLink.Attrs().Index}
				if err := netlink.RouteDel(route); err != nil {
					logrus.WithError(err).WithField("prefix", dst.String()).Warn("Failed to remove WireGuard route")
				}
			}
		}
		w.tunnelDsts = nil
	}

	for _, route := range w.replacedDefaults {
		if err := netlink.RouteAdd(route); err != nil {
			logrus.WithError(err).WithField("gw", route.Gw).Warn("Failed to restore original default route")
		} else {
			logrus.WithField("gw", route.Gw).Info("Restored original default route")
		}
	}
	w.replacedDefaults = nil

	if w.defaultGW == nil {
		return
	}

	primaryLink, err := netlink.LinkByName(w.defaultIface)
	if err != nil {
		logrus.WithError(err).Warn("Failed to find primary interface for peer route cleanup")
		return
	}

	peerIP, err := parsePeerEndpoint(w.config.PeerEndpoint)
	if err != nil {
		logrus.WithError(err).Warn("Failed to parse peer endpoint for route cleanup")
		return
	}

	peerRoute := &netlink.Route{
		Dst:       &net.IPNet{IP: peerIP, Mask: peerHostMask(peerIP)},
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

// isDefaultRoute checks if a netlink route is a default route (0.0.0.0/0 or
// ::/0). netlink reports either an unset or an all-zero destination for them.
func isDefaultRoute(route netlink.Route) bool {
	if route.Dst == nil {
		return true
	}
	return isDefaultPrefix(route.Dst)
}

// isDefaultPrefix reports whether a prefix covers a whole address family.
func isDefaultPrefix(dst *net.IPNet) bool {
	if dst == nil {
		return false
	}
	ones, _ := dst.Mask.Size()
	return ones == 0 && dst.IP.IsUnspecified()
}

// ipFamily returns the netlink address family of an IP.
func ipFamily(ip net.IP) int {
	if ip.To4() != nil {
		return syscall.AF_INET
	}
	return syscall.AF_INET6
}

// routeFamily returns the address family a route belongs to, or AF_UNSPEC when
// it carries no address to tell from (a family-agnostic default route).
func routeFamily(route netlink.Route) int {
	switch {
	case route.Dst != nil && route.Dst.IP != nil:
		return ipFamily(route.Dst.IP)
	case route.Gw != nil:
		return ipFamily(route.Gw)
	default:
		return syscall.AF_UNSPEC
	}
}

// parseTunnelRoutes turns the configured allowed IPs into the prefixes to route
// into the tunnel, mirroring wg-quick: one route per allowed IP rather than an
// unconditional IPv4 default.
func parseTunnelRoutes(allowedIPs []string) ([]*net.IPNet, error) {
	dsts := make([]*net.IPNet, 0, len(allowedIPs))
	for _, allowedIP := range allowedIPs {
		ip, prefix, err := net.ParseCIDR(allowedIP)
		if err != nil {
			return nil, fmt.Errorf("invalid allowed_ip %q: %w", allowedIP, err)
		}
		// ParseCIDR masks the address; keep the family of the original for
		// an unspecified default so ::/0 does not collapse onto 0.0.0.0/0.
		if ip.To4() == nil {
			prefix.IP = prefix.IP.To16()
		}
		dsts = append(dsts, prefix)
	}
	return dsts, nil
}

// getDefaultGateway returns the default gateway IP and interface name of the
// given address family using netlink
func getDefaultGateway(family int) (net.IP, string, error) {
	routes, err := netlink.RouteList(nil, family)
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
