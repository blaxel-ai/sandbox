package archive

import (
	"net"
	"net/http"
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
		DialContext:           (&net.Dialer{Timeout: dialTimeout}).DialContext,
		TLSHandshakeTimeout:   handshakeTimeout,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}
