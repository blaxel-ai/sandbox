//go:build !linux

package isolation

import "errors"

// Install is only supported on Linux, where the rules are enforced by nftables.
func Install(port int) error {
	return errors.New("API isolation requires Linux nftables")
}
