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
			Control: refuseLinkLocal,
		}).DialContext,
		TLSHandshakeTimeout:   handshakeTimeout,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

// refuseLinkLocal refuses to connect to a link-local address.
//
// The archive URL comes from the caller - it has to, it is a presigned URL this
// API cannot mint - and the caller may be the workload itself, so it decides
// where an archive is read from and written to. That is its point, but it must not
// double as a way to reach the addresses that answer only because of where this
// process runs: the instance metadata service and the rest of the link-local
// range are reachable from inside the VM and from nowhere else, and a request
// this API makes to them carries the VM's identity rather than the caller's.
//
// The check is on the resolved address, after the name is looked up, so a
// hostname resolving to a link-local address is refused too.
func refuseLinkLocal(_, address string, _ syscall.RawConn) error {
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
	return nil
}
