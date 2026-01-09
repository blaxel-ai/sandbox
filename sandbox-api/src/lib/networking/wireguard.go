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

// Global WireGuard client instance
var (
	wgClient     *WireGuardClient
	wgClientOnce sync.Once
)

// GetWireGuardClient returns the global WireGuard client instance
func GetWireGuardClient() *WireGuardClient {
	return wgClient
}

// StartWireGuardFromEnv initializes and starts the WireGuard client if config is present in env
// This should be called once at application startup
func StartWireGuardFromEnv() error {
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

	wgClientOnce.Do(func() {
		wgClient = client
	})

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
		w.tunDevice.Close()
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
	ipcConfig := w.buildIPCConfig(privateKey)
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
func (w *WireGuardClient) buildIPCConfig(privateKey string) string {
	var config strings.Builder

	// Interface configuration
	config.WriteString(fmt.Sprintf("private_key=%s\n", hexEncode(privateKey)))
	config.WriteString(fmt.Sprintf("listen_port=%d\n", w.config.ListenPort))

	// Peer configuration
	config.WriteString(fmt.Sprintf("public_key=%s\n", hexEncode(w.config.PeerPublicKey)))
	config.WriteString(fmt.Sprintf("endpoint=%s\n", w.config.PeerEndpoint))

	// Allowed IPs
	for _, allowedIP := range w.config.AllowedIPs {
		config.WriteString(fmt.Sprintf("allowed_ip=%s\n", allowedIP))
	}

	// Persistent keepalive
	if w.config.PersistentKeepalive > 0 {
		config.WriteString(fmt.Sprintf("persistent_keepalive_interval=%d\n", w.config.PersistentKeepalive))
	}

	return config.String()
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

// setupRoutes configures routing to send all traffic through the WireGuard tunnel using netlink
func (w *WireGuardClient) setupRoutes(wgLink netlink.Link) error {
	// Extract peer IP from endpoint (remove port)
	peerIPStr := strings.Split(w.config.PeerEndpoint, ":")[0]
	peerIP := net.ParseIP(peerIPStr)
	if peerIP == nil {
		return fmt.Errorf("invalid peer IP: %s", peerIPStr)
	}

	// Get current default gateway BEFORE we delete any routes
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
		"peer_ip":       peerIPStr,
	}).Info("Setting up routes")

	// Get the primary interface link
	primaryLink, err := netlink.LinkByName(defaultIface)
	if err != nil {
		return fmt.Errorf("failed to find primary interface %s: %w", defaultIface, err)
	}

	// STEP 1: Add route to peer endpoint via original gateway (to maintain WireGuard connection)
	peerRoute := &netlink.Route{
		Dst: &net.IPNet{
			IP:   peerIP,
			Mask: net.CIDRMask(32, 32),
		},
		Gw:        defaultGW,
		LinkIndex: primaryLink.Attrs().Index,
	}
	if err := netlink.RouteAdd(peerRoute); err != nil {
		logrus.WithError(err).Warn("Failed to add route to peer endpoint (may already exist)")
	} else {
		logrus.WithField("peer_ip", peerIPStr).Info("Added route to peer endpoint")
	}

	// STEP 2: Delete ALL existing default routes FIRST
	defaultDst := &net.IPNet{
		IP:   net.IPv4zero,
		Mask: net.CIDRMask(0, 32),
	}

	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	for _, route := range routes {
		isDefault := route.Dst == nil ||
			(route.Dst != nil && route.Dst.IP.Equal(net.IPv4zero) && route.Dst.Mask.String() == "00000000")

		if isDefault {
			linkName := "unknown"
			if link, err := netlink.LinkByIndex(route.LinkIndex); err == nil {
				linkName = link.Attrs().Name
			}
			logrus.WithField("interface", linkName).Info("Removing default route")

			if err := netlink.RouteDel(&route); err != nil {
				logrus.WithError(err).WithField("interface", linkName).Warn("Failed to remove default route")
			} else {
				logrus.WithField("interface", linkName).Info("Removed default route")
			}
		}
	}

	// STEP 3: Add new default route via WireGuard interface
	newDefaultRoute := &netlink.Route{
		Dst:       defaultDst,
		LinkIndex: wgLink.Attrs().Index,
	}
	if err := netlink.RouteAdd(newDefaultRoute); err != nil {
		return fmt.Errorf("failed to add WireGuard default route: %w", err)
	}
	logrus.Info("Added WireGuard default route")

	// STEP 4: Delete eth0 default route again (platform may have re-added it)
	// Do this AFTER adding wg0 route to ensure we always have a default route
	if err := w.deleteDefaultRouteOnInterface(defaultIface); err != nil {
		logrus.WithError(err).Warn("Failed to delete eth0 default route")
	}

	logrus.Info("Routes configured successfully")
	return nil
}

// deleteDefaultRouteOnInterface removes the default route on a specific interface
func (w *WireGuardClient) deleteDefaultRouteOnInterface(ifaceName string) error {
	link, err := netlink.LinkByName(ifaceName)
	if err != nil {
		return fmt.Errorf("failed to find interface %s: %w", ifaceName, err)
	}

	routes, err := netlink.RouteList(link, syscall.AF_INET)
	if err != nil {
		return fmt.Errorf("failed to list routes for %s: %w", ifaceName, err)
	}

	for _, route := range routes {
		isDefault := route.Dst == nil ||
			(route.Dst != nil && route.Dst.IP.Equal(net.IPv4zero) && route.Dst.Mask.String() == "00000000")

		if isDefault {
			logrus.WithField("interface", ifaceName).Info("Removing default route from interface")
			if err := netlink.RouteDel(&route); err != nil {
				return fmt.Errorf("failed to delete default route: %w", err)
			}
			logrus.WithField("interface", ifaceName).Info("Removed default route from interface")
		}
	}

	return nil
}

// removeRoutes removes the routes set up by setupRoutes using netlink
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

	// Default route destination (0.0.0.0/0)
	defaultDst := &net.IPNet{
		IP:   net.IPv4zero,
		Mask: net.CIDRMask(0, 32),
	}

	// Remove WireGuard default route
	wgDefaultRoute := &netlink.Route{
		Dst:       defaultDst,
		LinkIndex: wgLink.Attrs().Index,
	}
	if err := netlink.RouteDel(wgDefaultRoute); err != nil {
		logrus.WithError(err).Warn("Failed to remove WireGuard default route")
	}

	// Get primary interface
	primaryLink, err := netlink.LinkByName(w.defaultIface)
	if err != nil {
		logrus.WithError(err).Warn("Failed to find primary interface for route cleanup")
		return
	}

	// Remove peer endpoint route
	peerIPStr := strings.Split(w.config.PeerEndpoint, ":")[0]
	peerIP := net.ParseIP(peerIPStr)
	if peerIP != nil {
		peerRoute := &netlink.Route{
			Dst: &net.IPNet{
				IP:   peerIP,
				Mask: net.CIDRMask(32, 32),
			},
			Gw:        w.defaultGW,
			LinkIndex: primaryLink.Attrs().Index,
		}
		if err := netlink.RouteDel(peerRoute); err != nil {
			logrus.WithError(err).Warn("Failed to remove peer endpoint route")
		}
	}

	// Restore original default route
	originalDefaultRoute := &netlink.Route{
		Dst:       defaultDst,
		Gw:        w.defaultGW,
		LinkIndex: primaryLink.Attrs().Index,
	}
	if err := netlink.RouteAdd(originalDefaultRoute); err != nil {
		logrus.WithError(err).Warn("Failed to restore original default route")
	}

	logrus.Info("Routes cleaned up")
}

// derivePublicKey derives a public key from a private key
func derivePublicKey(privateKeyBase64 string) (string, error) {
	privateBytes, err := base64.StdEncoding.DecodeString(privateKeyBase64)
	if err != nil {
		return "", fmt.Errorf("failed to decode private key: %w", err)
	}

	if len(privateBytes) != 32 {
		return "", fmt.Errorf("invalid private key length: expected 32, got %d", len(privateBytes))
	}

	var private, public [32]byte
	copy(private[:], privateBytes)
	curve25519.ScalarBaseMult(&public, &private)

	return base64.StdEncoding.EncodeToString(public[:]), nil
}

// hexEncode converts a base64-encoded key to hex encoding (required by WireGuard IPC)
func hexEncode(base64Key string) string {
	keyBytes, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%x", keyBytes)
}

// getDefaultGateway returns the default gateway IP and interface name using netlink
func getDefaultGateway() (net.IP, string, error) {
	routes, err := netlink.RouteList(nil, syscall.AF_INET)
	if err != nil {
		return nil, "", fmt.Errorf("failed to list routes: %w", err)
	}

	for _, route := range routes {
		// Default route can be represented as:
		// 1. Dst == nil (common representation)
		// 2. Dst == 0.0.0.0/0 (some container environments)
		isDefault := route.Dst == nil ||
			(route.Dst != nil && route.Dst.IP.Equal(net.IPv4zero) && route.Dst.Mask.String() == "00000000")

		if isDefault && route.Gw != nil {
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
	}

	return nil, "", fmt.Errorf("default gateway not found")
}

// GetStatus returns the current status of the WireGuard client
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

	return status
}
