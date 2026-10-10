//go:build !linux

package networking

import "context"

// StartVirtioWatchdog is a no-op on non-Linux platforms.
func StartVirtioWatchdog(ctx context.Context) {}
