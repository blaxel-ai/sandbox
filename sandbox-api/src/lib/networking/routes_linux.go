//go:build linux

package networking

import (
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

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

			// Check if it's on our primary interface
			if w.defaultIface == "" {
				continue
			}

			primaryLink, err := netlink.LinkByName(w.defaultIface)
			if err != nil {
				continue
			}

			if route.LinkIndex != primaryLink.Attrs().Index {
				continue
			}

			// This is a conflicting default route - remove it immediately!
			logrus.WithFields(logrus.Fields{
				"gw":        route.Gw,
				"interface": w.defaultIface,
			}).Warn("Detected new conflicting default route being added, removing immediately")

			if err := netlink.RouteDel(&route); err != nil {
				logrus.WithError(err).Warn("Failed to remove conflicting default route")
			} else {
				logrus.Info("Successfully removed conflicting default route")
			}
		}
	}
}
