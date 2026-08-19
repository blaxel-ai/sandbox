//go:build linux

package networking

import (
	"net"
	"syscall"
	"testing"

	"github.com/vishvananda/netlink"
)

func mustCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	dsts, err := parseTunnelRoutes([]string{cidr})
	if err != nil {
		t.Fatalf("parseTunnelRoutes(%q): %v", cidr, err)
	}
	return dsts[0]
}

func TestIPFamily(t *testing.T) {
	if got := ipFamily(net.ParseIP("1.2.3.4")); got != syscall.AF_INET {
		t.Errorf("expected AF_INET for IPv4, got %d", got)
	}
	if got := ipFamily(net.ParseIP("2001:db8::1")); got != syscall.AF_INET6 {
		t.Errorf("expected AF_INET6 for IPv6, got %d", got)
	}
	// An IPv4 address in 16-byte form is still IPv4.
	if got := ipFamily(net.ParseIP("1.2.3.4").To16()); got != syscall.AF_INET {
		t.Errorf("expected AF_INET for 4-in-6 IPv4, got %d", got)
	}
}

func TestIsDefaultPrefix(t *testing.T) {
	tests := []struct {
		cidr string
		want bool
	}{
		{"0.0.0.0/0", true},
		{"::/0", true},
		{"10.0.0.0/8", false},
		{"2001:db8::/32", false},
		{"240.1.0.1/32", false},
	}

	for _, tt := range tests {
		if got := isDefaultPrefix(mustCIDR(t, tt.cidr)); got != tt.want {
			t.Errorf("isDefaultPrefix(%s) = %v, want %v", tt.cidr, got, tt.want)
		}
	}

	if isDefaultPrefix(nil) {
		t.Error("isDefaultPrefix(nil) must be false")
	}
}

func TestParseTunnelRoutesKeepsFamilies(t *testing.T) {
	dsts, err := parseTunnelRoutes([]string{"0.0.0.0/0", "::/0", "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(dsts) != 3 {
		t.Fatalf("expected 3 routes, got %d", len(dsts))
	}
	if ipFamily(dsts[0].IP) != syscall.AF_INET {
		t.Error("0.0.0.0/0 must stay IPv4")
	}
	if ipFamily(dsts[1].IP) != syscall.AF_INET6 {
		t.Error("::/0 must stay IPv6")
	}
	if dsts[1].String() != "::/0" {
		t.Errorf("expected ::/0, got %s", dsts[1].String())
	}
	if dsts[2].String() != "10.0.0.0/8" {
		t.Errorf("expected 10.0.0.0/8, got %s", dsts[2].String())
	}
}

func TestParseTunnelRoutesInvalid(t *testing.T) {
	if _, err := parseTunnelRoutes([]string{"not-a-cidr"}); err == nil {
		t.Fatal("expected an error for an invalid allowed IP")
	}
}

func TestIsDefaultRoute(t *testing.T) {
	tests := []struct {
		name  string
		route netlink.Route
		want  bool
	}{
		{"no destination", netlink.Route{}, true},
		{"ipv4 default", netlink.Route{Dst: mustCIDR(t, "0.0.0.0/0")}, true},
		{"ipv6 default", netlink.Route{Dst: mustCIDR(t, "::/0")}, true},
		{"ipv4 subnet", netlink.Route{Dst: mustCIDR(t, "10.0.0.0/8")}, false},
		{"ipv6 host", netlink.Route{Dst: mustCIDR(t, "2001:db8::1/128")}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDefaultRoute(tt.route); got != tt.want {
				t.Errorf("isDefaultRoute() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRouteFamily(t *testing.T) {
	if got := routeFamily(netlink.Route{Dst: mustCIDR(t, "::/0")}); got != syscall.AF_INET6 {
		t.Errorf("expected AF_INET6 from destination, got %d", got)
	}
	if got := routeFamily(netlink.Route{Gw: net.ParseIP("fe80::1")}); got != syscall.AF_INET6 {
		t.Errorf("expected AF_INET6 from gateway, got %d", got)
	}
	if got := routeFamily(netlink.Route{Gw: net.ParseIP("10.0.0.1")}); got != syscall.AF_INET {
		t.Errorf("expected AF_INET from gateway, got %d", got)
	}
	if got := routeFamily(netlink.Route{}); got != syscall.AF_UNSPEC {
		t.Errorf("expected AF_UNSPEC for an address-less route, got %d", got)
	}
	// An on-link default carries neither destination nor gateway; the kernel's
	// family is the only thing telling it apart.
	if got := routeFamily(netlink.Route{Family: syscall.AF_INET6}); got != syscall.AF_INET6 {
		t.Errorf("expected AF_INET6 from the route family, got %d", got)
	}
	if got := routeFamily(netlink.Route{Family: syscall.AF_INET}); got != syscall.AF_INET {
		t.Errorf("expected AF_INET from the route family, got %d", got)
	}
}

// An unattributable default must never be left in place while the tunnel owns a
// default, or it would silently divert traffic off the tunnel.
func TestConflictsWithTunnel(t *testing.T) {
	client := &WireGuardClient{tunnelDsts: []*net.IPNet{mustCIDR(t, "0.0.0.0/0")}}

	if !client.conflictsWithTunnel(netlink.Route{Family: syscall.AF_INET}) {
		t.Error("an on-link IPv4 default conflicts with the tunnelled IPv4 default")
	}
	if client.conflictsWithTunnel(netlink.Route{Family: syscall.AF_INET6}) {
		t.Error("an IPv6 default does not conflict when only IPv4 is tunnelled")
	}
	if !client.conflictsWithTunnel(netlink.Route{}) {
		t.Error("a default of unknown family must be treated as conflicting")
	}

	noDefault := &WireGuardClient{tunnelDsts: []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}}
	if noDefault.conflictsWithTunnel(netlink.Route{}) {
		t.Error("nothing conflicts when the tunnel carries no default route")
	}
}

// A v6-only sandbox tunnelling IPv4 must keep its IPv6 default route: it is what
// carries the tunnel's own UDP to an IPv6 peer endpoint.
func TestRoutesFamilyByDefault(t *testing.T) {
	client := &WireGuardClient{tunnelDsts: []*net.IPNet{mustCIDR(t, "0.0.0.0/0")}}

	if !client.routesFamilyByDefault(syscall.AF_INET) {
		t.Error("IPv4 default is on the tunnel, expected true")
	}
	if client.routesFamilyByDefault(syscall.AF_INET6) {
		t.Error("IPv6 default is not on the tunnel, expected false")
	}
	if client.routesFamilyByDefault(syscall.AF_UNSPEC) {
		t.Error("an unknown family must never be treated as tunnelled")
	}

	client.tunnelDsts = append(client.tunnelDsts, mustCIDR(t, "::/0"))
	if !client.routesFamilyByDefault(syscall.AF_INET6) {
		t.Error("IPv6 default is on the tunnel now, expected true")
	}

	narrow := &WireGuardClient{tunnelDsts: []*net.IPNet{mustCIDR(t, "10.0.0.0/8")}}
	if narrow.routesFamilyByDefault(syscall.AF_INET) {
		t.Error("a narrow prefix must not count as the family default")
	}
}

func TestDefaultAllowedIP(t *testing.T) {
	tests := []struct {
		localIP string
		want    string
	}{
		{"240.1.0.1/32", "0.0.0.0/0"},
		{"fd00::2/128", "::/0"},
		{"", "0.0.0.0/0"},
	}

	for _, tt := range tests {
		if got := defaultAllowedIP(tt.localIP); got != tt.want {
			t.Errorf("defaultAllowedIP(%q) = %s, want %s", tt.localIP, got, tt.want)
		}
	}
}
