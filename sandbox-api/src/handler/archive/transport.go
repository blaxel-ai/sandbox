package archive

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"
)

const (
	// transferTimeout bounds an archive transfer end to end. Storage stalling on
	// a presigned transfer must not hold the sandbox forever: an export that
	// never returns leaves it frozen with no way to resume, and an import that
	// never returns holds up the boot before anything is served. The bound is
	// deliberately generous, since the archive is as large as the sandbox's
	// filesystem changes and is transferred at the storage's pace.
	transferTimeout = 1 * time.Hour
	// dialTimeout and handshakeTimeout bound reaching the storage, which either
	// answers in seconds or is not going to.
	dialTimeout      = 30 * time.Second
	handshakeTimeout = 30 * time.Second
)

// transferClient is used for archive uploads and downloads instead of
// http.DefaultClient, which has no timeout of any kind. No ResponseHeaderTimeout
// here: an upload's response headers only come once the whole archive is in, so
// it would cut off exactly the transfers it is supposed to protect, and the
// request context carries transferTimeout for that.
var transferClient = &http.Client{
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: dialTimeout,
			Control: refuseInstanceOnlyAddress,
		}).DialContext,
		TLSHandshakeTimeout:   handshakeTimeout,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// refuseInstanceOnlyAddress refuses to connect to an address that answers only
// because of where this process runs.
//
// The archive URL comes from the caller - it has to, it is a presigned URL this
// API cannot mint - and the caller may be the workload itself, so it decides
// where an archive is read from and written to. That is its point, but it must not
// double as a way to reach the instance's own services: the metadata service is
// reachable from inside the VM and from nowhere else, and a request this API
// makes to it carries the VM's identity rather than the caller's. It answers on
// a link-local address over IPv4 (169.254.169.254) and on a unique-local one
// over IPv6 (fd00:ec2::254), so both ranges are refused.
//
// The check is on the resolved address, after the name is looked up, so a
// hostname resolving into either range is refused too.
func refuseInstanceOnlyAddress(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("refusing to transfer an archive to or from the link-local address %s", ip)
	}
	if isUniqueLocal(ip) {
		return fmt.Errorf("refusing to transfer an archive to or from the unique-local address %s", ip)
	}
	return nil
}

// isUniqueLocal reports whether ip is in the IPv6 unique-local range, fc00::/7.
// net has no predicate for it, and it is where the metadata service answers over
// IPv6.
func isUniqueLocal(ip net.IP) bool {
	ip6 := ip.To16()
	if ip6 == nil || ip.To4() != nil {
		return false
	}
	return ip6[0]&0xfe == 0xfc
}
