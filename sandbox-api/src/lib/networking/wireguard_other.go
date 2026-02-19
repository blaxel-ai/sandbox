//go:build !linux

package networking

import "fmt"

// WireGuard is only supported on Linux
var errNotSupported = fmt.Errorf("WireGuard networking is only supported on Linux")

// GetWireGuardClient returns nil on non-Linux platforms
func GetWireGuardClient() *WireGuardClient {
	return nil
}

// StartWireGuardFromEnv returns an error on non-Linux platforms
func StartWireGuardFromEnv() error {
	return nil // Silently ignore on non-Linux for dev environments
}

// StopWireGuard returns an error on non-Linux platforms
func StopWireGuard() error {
	return errNotSupported
}

// UpdateWireGuardConfig returns an error on non-Linux platforms
func UpdateWireGuardConfig(config *WireGuardConfig) error {
	return errNotSupported
}

// NewWireGuardClient returns an error on non-Linux platforms
func NewWireGuardClient(config *WireGuardConfig) (*WireGuardClient, error) {
	return nil, errNotSupported
}

// Stub type for non-Linux platforms
type WireGuardClient struct{}
