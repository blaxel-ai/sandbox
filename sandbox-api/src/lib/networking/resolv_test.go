package networking

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderResolvConfPutsTunnelledNameserversFirst(t *testing.T) {
	existing := "# DNS Configuration\nnameserver fd00:100::a\noptions edns0 trust-ad\n"

	got := string(renderResolvConf([]byte(existing), []string{"1.1.1.1"}))

	want := "nameserver 1.1.1.1\n# DNS Configuration\nnameserver fd00:100::a\noptions edns0 trust-ad\n"
	if got != want {
		t.Fatalf("rendered resolv.conf =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderResolvConfDropsNameserversPastGlibcLimit(t *testing.T) {
	existing := "nameserver fd00:100::a\nnameserver fd00:100::b\n"

	got := string(renderResolvConf([]byte(existing), []string{"1.1.1.1", "8.8.8.8"}))

	want := "nameserver 1.1.1.1\nnameserver 8.8.8.8\nnameserver fd00:100::a\n"
	if got != want {
		t.Fatalf("rendered resolv.conf =\n%q\nwant\n%q", got, want)
	}
}

func TestRenderResolvConfIsIdempotent(t *testing.T) {
	first := renderResolvConf([]byte("nameserver fd00:100::a\n"), []string{"1.1.1.1"})
	second := renderResolvConf(first, []string{"1.1.1.1"})

	if string(first) != string(second) {
		t.Fatalf("second render changed the file:\n%q\nvs\n%q", second, first)
	}
}

func TestApplyTunnelDNSRestoresTheOriginalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	original := "nameserver fd00:100::a\n"
	if err := os.WriteFile(path, []byte(original), 0o444); err != nil {
		t.Fatal(err)
	}

	guard, err := applyTunnelDNS(path, []string{"1.1.1.1"})
	if err != nil {
		t.Fatalf("applyTunnelDNS: %v", err)
	}

	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "nameserver 1.1.1.1\nnameserver fd00:100::a\n"; string(updated) != want {
		t.Fatalf("resolv.conf = %q, want %q", updated, want)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o444 {
		t.Fatalf("resolv.conf mode = %v, want the original 0444", info.Mode().Perm())
	}

	guard.restore()

	restored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(restored) != original {
		t.Fatalf("restored resolv.conf = %q, want %q", restored, original)
	}
}

func TestApplyTunnelDNSWithoutNameserversIsANoOp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-resolv.conf")

	guard, err := applyTunnelDNS(path, nil)
	if err != nil {
		t.Fatalf("applyTunnelDNS: %v", err)
	}
	if guard != nil {
		t.Fatalf("guard = %v, want nil", guard)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("resolv.conf was created: %v", err)
	}
}

func TestTunnelledDNSKeepsOnlyNameserversTheTunnelCarries(t *testing.T) {
	_, v4Default, err := net.ParseCIDR("0.0.0.0/0")
	if err != nil {
		t.Fatal(err)
	}

	servers := []string{"1.1.1.1", "fd00:100::a", "not-an-ip"}

	got := tunnelledDNS(servers, []*net.IPNet{v4Default})
	if len(got) != 1 || got[0] != "1.1.1.1" {
		t.Fatalf("tunnelledDNS = %v, want [1.1.1.1]", got)
	}

	// Nothing tunnelled: an IPv4 resolver would be unreachable, so the
	// sandbox keeps the resolver it booted with.
	if got := tunnelledDNS(servers, nil); len(got) != 0 {
		t.Fatalf("tunnelledDNS with no tunnelled prefixes = %v, want none", got)
	}
}
