package api

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quic-go/quic-go/http3"
)

// github.com/quic-go/quic-go is linked into the sandbox-api binary because
// gin imports quic-go/http3 unconditionally (for Engine.RunQUIC, which this
// repo never calls). This test is what makes the dependency verifiable rather
// than merely compiled: it stands up the *real* router from SetupRouter behind
// an HTTP/3 server on loopback UDP, completes a genuine QUIC/TLS 1.3 handshake
// and round-trips a request, then round-trips response trailers -- the exact
// surface quic-go 0.59.1 changed (GHSA-vvgj-x9jq-8cj9, HTTP/3 QPACK trailer
// expansion memory exhaustion; the fix added validation to http3.parseTrailers,
// which the *client* runs when decoding response trailers).
//
// What it proves: the bump did not break the HTTP/3 transport or trailer
// handling. What it does NOT prove: that the advisory is gone. That is a
// memory-exhaustion class reachable only from a malicious peer emitting
// oversized/invalid QPACK trailers, which requires a hand-rolled QPACK writer
// to reach -- the server's own writeTrailers filters such fields before they
// hit the wire. Closure of the advisory rests on the version floor plus the
// go.sum check, not on this test.

// newLoopbackCert mints a self-signed cert for 127.0.0.1. notBefore is a year
// in the past on purpose: a cert minted "now" fails on any runner with clock
// skew, and passes locally, which is the worst pairing.
func newLoopbackCert(t *testing.T) (tls.Certificate, *x509.CertPool) {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "sandbox-api-http3-test"},
		NotBefore:             time.Now().Add(-365 * 24 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(leaf)

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, pool
}

func TestHTTP3QUICRoundTripThroughRealRouter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// The production construction path: the same engine main() serves.
	engine := SetupRouter(true, false)

	// Response trailers are not part of the router's own surface, so the probe
	// route is added here. It is the only way to drive http3's trailer decoder,
	// which is the function the 0.59.1 patch rewrote.
	const trailerKey = "X-Sandbox-Checksum"
	engine.GET("/__http3_trailer_probe", func(c *gin.Context) {
		c.Writer.Header().Set("Trailer", trailerKey)
		c.String(http.StatusOK, "trailer-probe-body")
		c.Writer.Flush()
		c.Writer.Header().Set(trailerKey, "deadbeef")
	})

	cert, pool := newLoopbackCert(t)

	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("listen udp: %v", err)
	}
	defer udpConn.Close()

	srv := &http3.Server{
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13},
		Handler:   engine,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(udpConn) }()
	defer srv.Close()

	tr := &http3.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS13},
	}
	defer tr.Close()
	client := &http.Client{Transport: tr}

	base := "https://" + udpConn.LocalAddr().String()

	t.Run("health over QUIC", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/health", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("QUIC handshake / request failed: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
		}
		// Proves the response really came over HTTP/3, not a fallback.
		if resp.ProtoMajor != 3 {
			t.Fatalf("proto = %s, want HTTP/3", resp.Proto)
		}
		if _, err := io.ReadAll(resp.Body); err != nil {
			t.Fatalf("read body: %v", err)
		}
	})

	t.Run("response trailers decode", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__http3_trailer_probe", nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request failed: %v", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != "trailer-probe-body" {
			t.Fatalf("body = %q, want %q", body, "trailer-probe-body")
		}
		// Trailers are only populated after the body is fully read.
		if got := resp.Trailer.Get(trailerKey); got != "deadbeef" {
			t.Fatalf("trailer %s = %q, want %q (trailers: %v)", trailerKey, got, "deadbeef", resp.Trailer)
		}
	})

	srv.Close()
	select {
	case <-serveErr:
	case <-time.After(5 * time.Second):
		t.Error("http3 server did not shut down")
	}
}
