package archive

import (
	"net/http"
	"testing"
)

// TestArchiveTransfersUseTheProxyOfTheEnvironment: a sandbox whose egress is
// locked down to its proxy only reaches the storage through it, so the
// transfer client must pick up HTTP(S)_PROXY like the rest of the sandbox.
func TestArchiveTransfersUseTheProxyOfTheEnvironment(t *testing.T) {
	transport, ok := transferClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", transferClient.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("transfer transport ignores HTTP(S)_PROXY: Proxy is nil")
	}
}

// TestArchiveTransfersRefuseTheInstancesOwnAddresses checks the archive URL
// cannot be pointed at the services that answer only from inside the VM. The
// metadata service is the one that matters: it answers on a link-local address
// over IPv4 and on a unique-local one over IPv6, and a request this API makes to
// it would carry the VM's identity instead of the caller's.
func TestArchiveTransfersRefuseTheInstancesOwnAddresses(t *testing.T) {
	refused := []string{
		"169.254.169.254:80",
		"[fe80::1]:443",
		"[fd00:ec2::254]:80",
		"[fc00::1]:443",
		// Scoped addresses: an IPv6 address carries the interface it is bound
		// to, and the zone must not make the address unreadable - an address
		// that cannot be read is one that is not refused.
		"[fe80::1%eth0]:443",
		"[fd00:ec2::254%eth0]:80",
	}
	for _, address := range refused {
		if err := refuseInstanceOnlyAddress("tcp", address, nil); err == nil {
			t.Errorf("connecting to %s should be refused", address)
		}
	}

	allowed := []string{
		"52.219.0.1:443",
		"[2600:1f18::1]:443",
		"127.0.0.1:8080",
		"storage.example.com:443",
	}
	for _, address := range allowed {
		if err := refuseInstanceOnlyAddress("tcp", address, nil); err != nil {
			t.Errorf("connecting to %s should be allowed, got %s", address, err)
		}
	}
}
