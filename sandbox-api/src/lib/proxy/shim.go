package proxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// defaultShimPort is the loopback port the credential-injecting proxy
	// listens on. It sits at the bottom of the IANA dynamic/private range
	// (49152-65535) so it cannot collide with a user application's service
	// port. It must stay stable across sandbox-api restarts: processes started
	// earlier keep http://127.0.0.1:<port> in their environment for their whole
	// lifetime.
	defaultShimPort = 49152

	// tokenPlaceholder replaces the {{file(...)}} directive while the proxy URL
	// is parsed, so that url.Parse sees a syntactically valid userinfo section.
	tokenPlaceholder = "blxltoken"

	tokenCacheTTL   = time.Second
	upstreamTimeout = 30 * time.Second
)

// upstreamProxy describes the real forward proxy and where its credential lives.
type upstreamProxy struct {
	Addr        string // host:port
	TLS         bool   // the upstream proxy itself speaks TLS
	Username    string // basic-auth username, empty when the token is the username
	TokenInUser bool   // the {{file(...)}} directive sat in the username position
	TokenFile   string
}

// parseUpstream derives the upstream proxy configuration from a proxy URL that
// still contains a {{file(...)}} directive.
func parseUpstream(t envTemplate) (upstreamProxy, error) {
	raw := fileDirectiveRe.ReplaceAllString(t.Template, tokenPlaceholder)

	u, err := url.Parse(raw)
	if err != nil {
		return upstreamProxy{}, fmt.Errorf("parse proxy url: %w", err)
	}
	if u.Host == "" {
		return upstreamProxy{}, fmt.Errorf("proxy url %q has no host", raw)
	}

	up := upstreamProxy{
		Addr:      u.Host,
		TLS:       u.Scheme == "https",
		TokenFile: t.FilePath,
	}
	if u.Port() == "" {
		if up.TLS {
			up.Addr = net.JoinHostPort(u.Hostname(), "443")
		} else {
			up.Addr = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	if u.User != nil {
		if u.User.Username() == tokenPlaceholder {
			up.TokenInUser = true
		} else {
			up.Username = u.User.Username()
		}
	}
	return up, nil
}

// shim is an HTTP proxy listening on loopback. It forwards every request to the
// upstream forward proxy, reading the identity token from disk at request time
// so that the credential is never captured in any process's environment.
type shim struct {
	up upstreamProxy

	mu       sync.Mutex
	cached   string
	cachedAt time.Time
}

func shimPort() int {
	if raw := os.Getenv("SANDBOX_LOCAL_PROXY_PORT"); raw != "" {
		if p, err := strconv.Atoi(raw); err == nil && p > 0 && p < 65536 {
			return p
		}
		logrus.Warnf("proxy: invalid SANDBOX_LOCAL_PROXY_PORT %q, using %d", raw, defaultShimPort)
	}
	return defaultShimPort
}

// loopbackAddrs are the addresses the shim binds. Both families are served so
// that a client resolving "localhost" to either ::1 or 127.0.0.1 reaches the
// shim, whichever it happens to pick.
var loopbackAddrs = []string{"127.0.0.1", "::1"}

// listenShim binds the loopback listeners, retrying briefly so that a hot
// upgrade — where the previous sandbox-api still holds the port for a moment —
// does not permanently lose the proxy. A family that the sandbox does not
// support is skipped as long as the other one binds.
func listenShim(port int) ([]net.Listener, error) {
	var listeners []net.Listener
	var lastErr error

	for _, host := range loopbackAddrs {
		addr := net.JoinHostPort(host, strconv.Itoa(port))

		var ln net.Listener
		var err error
		for attempt := 0; attempt < 10; attempt++ {
			ln, err = net.Listen("tcp", addr)
			if err == nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if err != nil {
			lastErr = fmt.Errorf("listen on %s: %w", addr, err)
			logrus.WithError(err).Warnf("proxy: could not listen on %s", addr)
			continue
		}
		listeners = append(listeners, ln)
	}

	if len(listeners) == 0 {
		return nil, lastErr
	}
	return listeners, nil
}

// serve runs the shim on every listener until ctx is cancelled.
func (s *shim) serve(ctx context.Context, listeners ...net.Listener) {
	srv := &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 30 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	var wg sync.WaitGroup
	for _, ln := range listeners {
		wg.Add(1)
		go func(ln net.Listener) {
			defer wg.Done()
			if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
				logrus.WithError(err).Errorf("proxy: local proxy stopped listening on %s", ln.Addr())
			}
		}(ln)
	}
	wg.Wait()
}

func (s *shim) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// handleConnect tunnels a CONNECT request through the upstream proxy, adding a
// freshly read Proxy-Authorization header. A 407 means the cached token was
// rotated out from under us, so the tunnel is retried once with a re-read.
func (s *shim) handleConnect(w http.ResponseWriter, r *http.Request) {
	upConn, upReader, resp, err := s.connect(r.Host, false)
	if err == nil && resp.StatusCode == http.StatusProxyAuthRequired {
		resp.Body.Close()
		upConn.Close()
		logrus.Info("proxy: upstream returned 407 for CONNECT, retrying with a re-read token")
		upConn, upReader, resp, err = s.connect(r.Host, true)
	}
	if err != nil {
		logrus.WithError(err).Warnf("proxy: CONNECT %s failed", r.Host)
		http.Error(w, "sandbox proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		resp.Body.Close()
		upConn.Close()
		logrus.Warnf("proxy: upstream refused CONNECT %s: %s", r.Host, resp.Status)
		http.Error(w, "sandbox proxy: upstream refused CONNECT: "+resp.Status+" "+string(body), resp.StatusCode)
		return
	}

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		upConn.Close()
		http.Error(w, "sandbox proxy: hijacking unsupported", http.StatusInternalServerError)
		return
	}
	clientConn, clientRW, err := hijacker.Hijack()
	if err != nil {
		upConn.Close()
		return
	}

	if _, err := io.WriteString(clientConn, "HTTP/1.1 200 Connection established\r\n\r\n"); err != nil {
		clientConn.Close()
		upConn.Close()
		return
	}

	// No deadlines on an established tunnel; it may stay idle for a long time.
	_ = clientConn.SetDeadline(time.Time{})
	_ = upConn.SetDeadline(time.Time{})

	go func() {
		defer upConn.Close()
		_, _ = io.Copy(upConn, clientRW)
	}()
	go func() {
		defer clientConn.Close()
		_, _ = io.Copy(clientConn, upReader)
	}()
}

// connect opens a tunnel to host through the upstream proxy and returns the
// connection together with the upstream's CONNECT response.
func (s *shim) connect(host string, reloadToken bool) (net.Conn, *bufio.Reader, *http.Response, error) {
	auth, err := s.authorization(reloadToken)
	if err != nil {
		return nil, nil, nil, err
	}

	conn, err := s.dialUpstream()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("upstream %s unreachable: %w", s.up.Addr, err)
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nProxy-Connection: Keep-Alive\r\n\r\n",
		host, host, auth)
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("upstream write failed: %w", err)
	}

	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("upstream read failed: %w", err)
	}
	return conn, reader, resp, nil
}

// hopByHopHeaders are stripped before forwarding a plain HTTP request.
var hopByHopHeaders = []string{
	"Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"Proxy-Connection",
	"Te",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// handleHTTP forwards a plain (non-CONNECT) request to the upstream proxy. A
// fresh connection is used per request: plain HTTP through the proxy is rare
// and pooling would pin a credential to a connection.
func (s *shim) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		http.Error(w, "sandbox proxy: absolute URI required", http.StatusBadRequest)
		return
	}

	// A request carrying a body cannot be replayed without buffering what may
	// be an arbitrarily large upload, so only bodyless requests are retried.
	retriable := r.ContentLength == 0

	upConn, resp, err := s.forward(r, false)
	if err == nil && retriable && resp.StatusCode == http.StatusProxyAuthRequired {
		resp.Body.Close()
		upConn.Close()
		logrus.Info("proxy: upstream returned 407, retrying with a re-read token")
		upConn, resp, err = s.forward(r, true)
	}
	if err != nil {
		logrus.WithError(err).Warnf("proxy: %s %s failed", r.Method, r.URL.Host)
		http.Error(w, "sandbox proxy: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer upConn.Close()
	defer resp.Body.Close()

	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// forward sends one request to the upstream proxy over its own connection:
// pooling would pin a credential to a connection, and plain HTTP through the
// proxy is rare.
func (s *shim) forward(r *http.Request, reloadToken bool) (net.Conn, *http.Response, error) {
	auth, err := s.authorization(reloadToken)
	if err != nil {
		return nil, nil, err
	}

	conn, err := s.dialUpstream()
	if err != nil {
		return nil, nil, fmt.Errorf("upstream %s unreachable: %w", s.up.Addr, err)
	}

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	for _, h := range hopByHopHeaders {
		outReq.Header.Del(h)
	}
	outReq.Header.Set("Proxy-Authorization", auth)
	outReq.Close = true

	if err := outReq.WriteProxy(conn); err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("upstream write failed: %w", err)
	}

	resp, err := http.ReadResponse(bufio.NewReader(conn), outReq)
	if err != nil {
		conn.Close()
		return nil, nil, fmt.Errorf("upstream read failed: %w", err)
	}
	return conn, resp, nil
}

func (s *shim) dialUpstream() (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", s.up.Addr, upstreamTimeout)
	if err != nil {
		return nil, err
	}
	if !s.up.TLS {
		return conn, nil
	}

	host, _, splitErr := net.SplitHostPort(s.up.Addr)
	if splitErr != nil {
		host = s.up.Addr
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	if err := tlsConn.Handshake(); err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// authorization builds the Proxy-Authorization header from the token on disk.
func (s *shim) authorization(reloadToken bool) (string, error) {
	token, err := s.readToken(reloadToken)
	if err != nil {
		return "", err
	}

	credentials := s.up.Username + ":" + token
	if s.up.TokenInUser {
		credentials = token + ":"
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials)), nil
}

// readToken returns the identity token, re-reading the file unless a very
// recent read is cached. reload forces a read, which is how a 407 is recovered
// from. The workload identity provider rewrites the file in place, so a read
// can catch it empty; the last value read is kept for that case.
func (s *shim) readToken(reload bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !reload && s.cached != "" && time.Since(s.cachedAt) < tokenCacheTTL {
		return s.cached, nil
	}

	data, err := os.ReadFile(s.up.TokenFile)
	token := strings.TrimSpace(string(data))
	if err != nil || token == "" {
		if s.cached != "" {
			logrus.Warnf("proxy: token file %s unreadable or empty, reusing the previous token", s.up.TokenFile)
			return s.cached, nil
		}
		if err != nil {
			return "", fmt.Errorf("read %s: %w", s.up.TokenFile, err)
		}
		return "", fmt.Errorf("token file %s is empty", s.up.TokenFile)
	}

	s.cached = token
	s.cachedAt = time.Now()
	return token, nil
}
