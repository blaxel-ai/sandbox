// Package isolation keeps user workloads from calling the sandbox API from
// inside the sandbox.
//
// Calls that reach the API through the gateway are authenticated before they
// arrive; calls made from inside the VM are not. With BL_SANDBOX_API_ISOLATION
// on, the API installs nftables rules at boot that reject, on its port:
//
//   - connections opened over loopback by any non-root process, which covers
//     everything spawned as the workload identity;
//   - connections arriving on any interface other than loopback and the uplink
//     the gateway uses (docker bridges, tunnels).
//
// Root is not filtered: the initrd's metadata reload, the API itself and image
// entrypoints that bootstrap the workload through the API all run as root. The
// counterpart is that a workload running as root can remove the rules, so the
// isolation is only as strong as the workload identity (see the identity
// package, which this option turns on).
package isolation

import (
	"os"
	"strconv"
	"strings"
)

// EnvEnabled opts into the isolation. It defaults to false.
const EnvEnabled = "BL_SANDBOX_API_ISOLATION"

// TableName is the nftables table holding the rules, in the inet family so a
// single set of rules covers IPv4 and IPv6.
const TableName = "blaxel_sandbox_api"

// Enabled reports whether the environment opts into the isolation. Anything
// unparseable counts as off, the same as unset.
func Enabled() bool {
	on, err := strconv.ParseBool(strings.TrimSpace(os.Getenv(EnvEnabled)))
	return err == nil && on
}
