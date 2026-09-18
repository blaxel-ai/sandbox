//go:build linux

package networking

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// StartVirtioWatchdog watches the kernel log for a virtio_net rx ring that the
// device and the driver no longer agree on, and re-initialises the device by
// unbinding and rebinding the virtio_net driver.
//
// The desync shows up after a snapshot/restore cycle: the guest is alive and
// its workload keeps running, but every packet the host queues on the broken
// ring is dropped, so the sandbox is unreachable until the VM is restarted.
// Rebinding the driver renegotiates the virtqueues with the device, which is
// the part of a restart the network actually needs, without losing the guest's
// memory or processes. The addresses and routes the interface carried are
// captured before the unbind and put back after it.
//
// It returns immediately; the watchdog runs until ctx is cancelled.
func StartVirtioWatchdog(ctx context.Context) {
	if VirtioWatchdogDisabled(os.Getenv) {
		logrus.Info("[VirtioWatchdog] Disabled by environment")
		return
	}
	if _, err := os.Stat(virtioNetDriverDir); err != nil {
		logrus.Debugf("[VirtioWatchdog] No virtio_net driver on this kernel, not watching (%v)", err)
		return
	}
	f, err := os.Open(kmsgPath)
	if err != nil {
		logrus.WithError(err).Warn("[VirtioWatchdog] Cannot read the kernel log, a broken virtio_net ring will not be recovered")
		return
	}
	// Only records logged from now on: the boot log is not something to react to.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		logrus.WithError(err).Warn("[VirtioWatchdog] Cannot seek the kernel log")
	}
	go watchKmsg(ctx, f, newRecoveryGate(virtioRecoverCooldown, time.Now), recoverVirtioNet)
	logrus.Info("[VirtioWatchdog] Watching the kernel log for a broken virtio_net ring")
}

func watchKmsg(ctx context.Context, f *os.File, gate *recoveryGate, recover func(device string) error) {
	defer func() { _ = f.Close() }()
	go func() {
		<-ctx.Done()
		_ = f.Close()
	}()

	// /dev/kmsg hands out one record per read; a Reader with a large buffer
	// keeps that boundary. A record the kernel dropped from the ring buffer
	// surfaces as EPIPE on read, which is not a reason to stop.
	r := bufio.NewReaderSize(f, 8192)
	for {
		line, err := r.ReadString('\n')
		if device := parseKmsgLine(line); device != "" {
			if gate.allow(device) {
				logrus.Warnf("[VirtioWatchdog] Kernel reports a broken virtio_net ring on %s: %s", device, strings.TrimSpace(line))
				if err := recover(device); err != nil {
					logrus.WithError(err).Errorf("[VirtioWatchdog] Failed to recover %s, the sandbox network may stay unreachable", device)
				}
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if isKmsgOverrun(err) {
				continue
			}
			logrus.WithError(err).Warn("[VirtioWatchdog] Kernel log read failed, stopping")
			return
		}
	}
}

// netState is what an interface carries that a driver rebind wipes.
type netState struct {
	name   string
	addrs  []netlink.Addr
	routes []netlink.Route
	mtu    int
}

// recoverVirtioNet rebinds the virtio_net driver to device (e.g. "virtio0")
// and restores the network configuration of the interface it backs.
func recoverVirtioNet(device string) error {
	ifname, err := virtioNetIface(device)
	if err != nil {
		return err
	}
	link, err := netlink.LinkByName(ifname)
	if err != nil {
		return fmt.Errorf("looking up %s backed by %s: %w", ifname, device, err)
	}
	state, err := captureNetState(link)
	if err != nil {
		return err
	}
	logrus.WithFields(logrus.Fields{
		"device": device, "iface": ifname, "addrs": len(state.addrs), "routes": len(state.routes),
	}).Info("[VirtioWatchdog] Rebinding virtio_net")

	if err := os.WriteFile(filepath.Join(virtioNetDriverDir, "unbind"), []byte(device), 0); err != nil {
		return fmt.Errorf("unbinding %s: %w", device, err)
	}
	if err := os.WriteFile(filepath.Join(virtioNetDriverDir, "bind"), []byte(device), 0); err != nil {
		return fmt.Errorf("rebinding %s: %w", device, err)
	}
	time.Sleep(virtioRebindSettle)

	newName, err := virtioNetIface(device)
	if err != nil {
		return fmt.Errorf("after rebind: %w", err)
	}
	newLink, err := netlink.LinkByName(newName)
	if err != nil {
		return fmt.Errorf("looking up %s after rebind: %w", newName, err)
	}
	if newName != state.name {
		if err := netlink.LinkSetName(newLink, state.name); err != nil {
			return fmt.Errorf("renaming %s back to %s: %w", newName, state.name, err)
		}
		if newLink, err = netlink.LinkByName(state.name); err != nil {
			return fmt.Errorf("looking up %s after rename: %w", state.name, err)
		}
	}
	if err := restoreNetState(newLink, state); err != nil {
		return err
	}
	logrus.Infof("[VirtioWatchdog] %s recovered on %s", state.name, device)
	return nil
}

// virtioNetIface returns the name of the network interface backed by a virtio
// device, from /sys/bus/virtio/devices/<device>/net/<ifname>.
func virtioNetIface(device string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(virtioDevicesDir, device, "net"))
	if err != nil {
		return "", fmt.Errorf("listing the interface of %s: %w", device, err)
	}
	if len(entries) != 1 {
		return "", fmt.Errorf("%s backs %d interfaces, expected 1", device, len(entries))
	}
	return entries[0].Name(), nil
}

func captureNetState(link netlink.Link) (*netState, error) {
	addrs, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("listing addresses of %s: %w", link.Attrs().Name, err)
	}
	routes, err := netlink.RouteList(link, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("listing routes of %s: %w", link.Attrs().Name, err)
	}
	return &netState{
		name:   link.Attrs().Name,
		addrs:  addrs,
		routes: routes,
		mtu:    link.Attrs().MTU,
	}, nil
}

func restoreNetState(link netlink.Link, state *netState) error {
	if state.mtu > 0 && link.Attrs().MTU != state.mtu {
		if err := netlink.LinkSetMTU(link, state.mtu); err != nil {
			logrus.WithError(err).Warnf("[VirtioWatchdog] Failed to restore MTU %d on %s", state.mtu, state.name)
		}
	}
	if err := netlink.LinkSetUp(link); err != nil {
		return fmt.Errorf("bringing %s up: %w", state.name, err)
	}
	for i := range state.addrs {
		addr := state.addrs[i]
		if addr.Scope == int(netlink.SCOPE_LINK) && addr.IP.To4() == nil {
			continue // IPv6 link-local is regenerated by the kernel
		}
		if err := netlink.AddrReplace(link, &addr); err != nil {
			logrus.WithError(err).Warnf("[VirtioWatchdog] Failed to restore address %s on %s", addr.IPNet, state.name)
		}
	}
	// Interface routes (the kernel adds them for each address) come first so
	// gateway routes have something to resolve their next hop through.
	index := link.Attrs().Index
	for pass := 0; pass < 2; pass++ {
		for i := range state.routes {
			route := state.routes[i]
			if (route.Gw == nil) != (pass == 0) {
				continue
			}
			route.LinkIndex = index
			if err := netlink.RouteReplace(&route); err != nil {
				logrus.WithError(err).Warnf("[VirtioWatchdog] Failed to restore route %s on %s", route.String(), state.name)
			}
		}
	}
	return nil
}

// isKmsgOverrun reports whether a /dev/kmsg read failed because records were
// overwritten in the kernel ring buffer before they were read (EPIPE); the
// next read resumes at the oldest available record.
func isKmsgOverrun(err error) bool {
	return errors.Is(err, syscall.EPIPE)
}
