//go:build linux

package networking

import (
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

// routesFamilyByDefault reports whether the tunnel carries the default route of
// the given address family.
func (w *WireGuardClient) routesFamilyByDefault(family int) bool {
	for _, dst := range w.tunnelDsts {
		if isDefaultPrefix(dst) && ipFamily(dst.IP) == family {
			return true
		}
	}
	return false
}

// routesAnyFamilyByDefault reports whether the tunnel carries a default route
// at all, whichever family.
func (w *WireGuardClient) routesAnyFamilyByDefault() bool {
	for _, dst := range w.tunnelDsts {
		if isDefaultPrefix(dst) {
			return true
		}
	}
	return false
}

// conflictsWithTunnel reports whether a newly added default route competes with
// a default the tunnel took over. A route whose family cannot be determined is
// treated as conflicting whenever the tunnel owns any default, so an
// unattributable default can never quietly divert traffic off the tunnel.
func (w *WireGuardClient) conflictsWithTunnel(route netlink.Route) bool {
	family := routeFamily(route)
	if family == unix.AF_UNSPEC {
		return w.routesAnyFamilyByDefault()
	}
	return w.routesFamilyByDefault(family)
}

// monitorRoutes subscribes to route changes and immediately removes conflicting default routes.
// This handles snapshot resume scenarios where the container runtime may re-add routes.
func (w *WireGuardClient) monitorRoutes(wgLink netlink.Link) {
	// Create a channel to receive route updates
	routeUpdateCh := make(chan netlink.RouteUpdate)
	doneCh := make(chan struct{})

	// Subscribe to route changes
	if err := netlink.RouteSubscribe(routeUpdateCh, doneCh); err != nil {
		logrus.WithError(err).Error("Failed to subscribe to route changes")
		return
	}

	logrus.Info("Started route monitor using real-time notifications")

	for {
		select {
		case <-w.stopMonitor:
			close(doneCh)
			logrus.Debug("Stopping route monitor")
			return

		case update := <-routeUpdateCh:
			// Only care about new routes being added
			if update.Type != unix.RTM_NEWROUTE {
				continue
			}

			route := update.Route

			// Check if this is a default route on the physical interface
			if !isDefaultRoute(route) {
				continue
			}

			// Only defaults of a family the tunnel took over are conflicting:
			// on an IPv6-only sandbox tunnelling IPv4, the IPv6 default is what
			// carries the tunnel's own packets and must be left alone.
			if !w.conflictsWithTunnel(route) {
				continue
			}

			// Anything off the tunnel competes with it, whichever interface it
			// showed up on.
			if route.LinkIndex == wgLink.Attrs().Index {
				continue
			}

			// This is a conflicting default route - remove it immediately!
			logrus.WithFields(logrus.Fields{
				"gw":         route.Gw,
				"link_index": route.LinkIndex,
			}).Warn("Detected new conflicting default route being added, removing immediately")

			if err := netlink.RouteDel(&route); err != nil {
				logrus.WithError(err).Warn("Failed to remove conflicting default route")
			} else {
				logrus.Info("Successfully removed conflicting default route")
			}
		}
	}
}
