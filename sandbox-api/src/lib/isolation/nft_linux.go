//go:build linux

package isolation

import (
	"fmt"
	"syscall"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

const loopback = "lo"

// Install (re)creates the isolation rules for the API listening on port. It is
// idempotent, so a re-exec after an upgrade replaces the rules of the previous
// process rather than stacking a second copy.
func Install(port int) error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}

	uplink, err := uplinkInterface()
	if err != nil {
		// A crashed route-all tunnel takes the IPv4 default route with it, so
		// a restart may find none. The rules of the run that did find the
		// uplink are still in the kernel; keep them rather than fail.
		if installed, listErr := tableExists(conn); listErr == nil && installed {
			logrus.WithError(err).Warn("Uplink interface not found, keeping the sandbox API isolation rules already installed")
			return nil
		}
		return fmt.Errorf("detect the uplink interface: %w", err)
	}

	table := conn.AddTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: TableName})
	conn.FlushTable(table)

	accept := nftables.ChainPolicyAccept
	output := conn.AddChain(&nftables.Chain{
		Name:     "output",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})
	input := conn.AddChain(&nftables.Chain{
		Name:     "input",
		Table:    table,
		Type:     nftables.ChainTypeFilter,
		Hooknum:  nftables.ChainHookInput,
		Priority: nftables.ChainPriorityFilter,
		Policy:   &accept,
	})

	conn.AddRule(&nftables.Rule{Table: table, Chain: output, Exprs: localRule(port)})
	conn.AddRule(&nftables.Rule{Table: table, Chain: input, Exprs: foreignInterfaceRule(port, uplink)})

	if err := conn.Flush(); err != nil {
		return fmt.Errorf("install nftables rules: %w", err)
	}
	logrus.WithFields(logrus.Fields{
		"port":   port,
		"uplink": uplink,
		"table":  "inet " + TableName,
	}).Info("Sandbox API isolated: local non-root callers and foreign interfaces are rejected")
	return nil
}

// Remove deletes the isolation rules, if a previous run installed them, so
// turning the option off takes effect on the next start.
func Remove() error {
	conn, err := nftables.New()
	if err != nil {
		return fmt.Errorf("open nftables: %w", err)
	}
	installed, err := tableExists(conn)
	if err != nil || !installed {
		return err
	}
	conn.DelTable(&nftables.Table{Family: nftables.TableFamilyINet, Name: TableName})
	if err := conn.Flush(); err != nil {
		return fmt.Errorf("delete nftables table: %w", err)
	}
	logrus.WithField("table", "inet "+TableName).Info("Sandbox API isolation disabled: removed the rules of a previous run")
	return nil
}

func tableExists(conn *nftables.Conn) (bool, error) {
	tables, err := conn.ListTablesOfFamily(nftables.TableFamilyINet)
	if err != nil {
		return false, fmt.Errorf("list nftables tables: %w", err)
	}
	for _, t := range tables {
		if t.Name == TableName {
			return true, nil
		}
	}
	return false, nil
}

// localRule: oifname "lo" tcp dport <port> meta skuid != 0 reject with tcp reset
//
// Every connection to an address of the VM itself, including its own uplink
// address, leaves through loopback, so this is where local callers are told
// apart: by the uid owning the socket.
func localRule(port int) []expr.Any {
	rule := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: ifname(loopback)},
	}
	rule = append(rule, tcpDport(port)...)
	return append(rule,
		&expr.Meta{Key: expr.MetaKeySKUID, Register: 1},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: binaryutil.NativeEndian.PutUint32(0)},
		&expr.Reject{Type: unix.NFT_REJECT_TCP_RST},
	)
}

// foreignInterfaceRule: iifname != "lo" iifname != <uplink> tcp dport <port> reject with tcp reset
//
// Containers and tunnels inside the sandbox reach the API through their own
// interfaces, where there is no socket owner to check.
func foreignInterfaceRule(port int, uplink string) []expr.Any {
	rule := []expr.Any{
		&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifname(loopback)},
		&expr.Cmp{Op: expr.CmpOpNeq, Register: 1, Data: ifname(uplink)},
	}
	rule = append(rule, tcpDport(port)...)
	return append(rule, &expr.Reject{Type: unix.NFT_REJECT_TCP_RST})
}

func tcpDport(port int) []expr.Any {
	return []expr.Any{
		&expr.Meta{Key: expr.MetaKeyL4PROTO, Register: 1},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: []byte{unix.IPPROTO_TCP}},
		&expr.Payload{DestRegister: 1, Base: expr.PayloadBaseTransportHeader, Offset: 2, Len: 2},
		&expr.Cmp{Op: expr.CmpOpEq, Register: 1, Data: binaryutil.BigEndian.PutUint16(uint16(port))},
	}
}

// ifname encodes an interface name the way the kernel compares it: a
// NUL-padded IFNAMSIZ buffer.
func ifname(name string) []byte {
	b := make([]byte, unix.IFNAMSIZ)
	copy(b, name)
	return b
}

// uplinkInterface returns the interface the gateway reaches the sandbox
// through: the one carrying the default route, IPv6 first since that is the
// family the gateway dials. Tunnel devices are skipped, because the egress
// tunnel replaces the IPv4 default route once it is up, and an upgrade re-runs
// Install after that.
func uplinkInterface() (string, error) {
	for _, family := range []int{syscall.AF_INET6, syscall.AF_INET} {
		routes, err := netlink.RouteList(nil, family)
		if err != nil {
			return "", fmt.Errorf("list routes: %w", err)
		}
		for _, route := range routes {
			if !isDefaultRoute(route) || route.LinkIndex == 0 {
				continue
			}
			link, err := netlink.LinkByIndex(route.LinkIndex)
			if err != nil {
				continue
			}
			if isTunnel(link) {
				continue
			}
			return link.Attrs().Name, nil
		}
	}
	return "", fmt.Errorf("no default route")
}

func isDefaultRoute(route netlink.Route) bool {
	if route.Dst == nil {
		return true
	}
	ones, _ := route.Dst.Mask.Size()
	return ones == 0
}

func isTunnel(link netlink.Link) bool {
	switch link.Type() {
	case "tuntap", "wireguard":
		return true
	}
	return false
}
