//go:build linux

package isolation

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/google/nftables"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func TestIsDefaultRoute(t *testing.T) {
	_, v6Default, _ := net.ParseCIDR("::/0")
	_, v4Default, _ := net.ParseCIDR("0.0.0.0/0")
	_, subnet, _ := net.ParseCIDR("fd00::/64")
	for _, tc := range []struct {
		dst  *net.IPNet
		want bool
	}{{nil, true}, {v6Default, true}, {v4Default, true}, {subnet, false}} {
		if got := isDefaultRoute(netlink.Route{Dst: tc.dst}); got != tc.want {
			t.Fatalf("isDefaultRoute(%v) = %v, want %v", tc.dst, got, tc.want)
		}
	}
}

func TestIfname(t *testing.T) {
	b := ifname("eth0")
	if len(b) != 16 || string(b[:4]) != "eth0" || b[4] != 0 {
		t.Fatalf("ifname(eth0) = %q, want eth0 NUL-padded to 16 bytes", b)
	}
}

// TestInstall drives Install and Remove in a throwaway network namespace, with
// a tunnel-free uplink carrying the default route.
func TestInstall(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root")
	}

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	origin, err := netns.Get()
	if err != nil {
		t.Fatalf("netns.Get: %v", err)
	}
	defer origin.Close()
	ns, err := netns.New()
	if err != nil {
		t.Fatalf("netns.New: %v", err)
	}
	defer ns.Close()
	defer func() { _ = netns.Set(origin) }()

	uplink := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "uplink0"}}
	if err := netlink.LinkAdd(uplink); err != nil {
		t.Fatalf("LinkAdd: %v", err)
	}
	if err := netlink.LinkSetUp(uplink); err != nil {
		t.Fatalf("LinkSetUp: %v", err)
	}
	_, anywhere, _ := net.ParseCIDR("::/0")
	if err := netlink.RouteAdd(&netlink.Route{LinkIndex: uplink.Attrs().Index, Dst: anywhere}); err != nil {
		t.Fatalf("RouteAdd: %v", err)
	}

	if got, err := uplinkInterface(); err != nil || got != "uplink0" {
		t.Fatalf("uplinkInterface() = %q, %v, want uplink0", got, err)
	}

	for i := 0; i < 2; i++ {
		if err := Install(8080); err != nil {
			t.Fatalf("Install #%d: %v", i+1, err)
		}
	}

	conn, err := nftables.New()
	if err != nil {
		t.Fatalf("nftables.New: %v", err)
	}
	requireOneRulePerChain(t, conn)

	// A restart that finds no default route (crashed route-all tunnel) keeps
	// the rules already installed.
	if err := netlink.RouteDel(&netlink.Route{LinkIndex: uplink.Attrs().Index, Dst: anywhere}); err != nil {
		t.Fatalf("RouteDel: %v", err)
	}
	if err := Install(8080); err != nil {
		t.Fatalf("Install without a default route: %v", err)
	}
	requireOneRulePerChain(t, conn)

	for i := 0; i < 2; i++ {
		if err := Remove(); err != nil {
			t.Fatalf("Remove #%d: %v", i+1, err)
		}
	}
	if installed, err := tableExists(conn); err != nil || installed {
		t.Fatalf("tableExists after Remove = %v, %v, want false", installed, err)
	}

	// Without the rules of a previous run, a missing uplink is an error.
	if err := Install(8080); err == nil {
		t.Fatal("Install without a default route nor previous rules succeeded")
	}
}

func requireOneRulePerChain(t *testing.T, conn *nftables.Conn) {
	t.Helper()
	table := &nftables.Table{Family: nftables.TableFamilyINet, Name: TableName}
	for _, chain := range []string{"input", "output"} {
		rules, err := conn.GetRules(table, &nftables.Chain{Name: chain, Table: table})
		if err != nil {
			t.Fatalf("GetRules(%s): %v", chain, err)
		}
		if len(rules) != 1 {
			t.Fatalf("chain %s has %d rules, want 1", chain, len(rules))
		}
	}
}
