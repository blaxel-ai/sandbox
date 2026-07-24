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
	tornReadRetries = 3
	tornReadBackoff = 2 * time.Millisecond
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
	lastGood string
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

// listenShim binds the loopback listener, retrying briefly so that a hot
// upgrade — where the previous sandbox-api still holds the port for a moment —
// does not permanently lose the proxy.
func listenShim(port int) (net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))

	var err error
	for attempt := 0; attempt < 10; attempt++ {
		var ln net.Listener
		ln, err = net.Listen("tcp", addr)
		if err == nil {
			return ln, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("listen on %s: %w", addr, err)
}

// serve runs the shim until ctx is cancelled.
func (s *shim) serve(ctx context.Context, ln net.Listener) {
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

	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		logrus.WithError(err).Error("proxy: local proxy stopped")
	}
}

func (s *shim) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		s.handleConnect(w, r)
		return
	}
	s.handleHTTP(w, r)
}

// handleConnect tunnels a CONNECT request through the upstream proxy, adding a
// freshly read Proxy-Authorization header.
func (s *shim) handleConnect(w http.ResponseWriter, r *http.Request) {
	auth, err := s.authorization()
	if err != nil {
		logrus.WithError(err).Warn("proxy: cannot build upstream credentials")
		http.Error(w, "sandbox proxy: identity token unavailable", http.StatusBadGateway)
		return
	}

	upConn, err := s.dialUpstream()
	if err != nil {
		logrus.WithError(err).Warnf("proxy: cannot reach upstream proxy %s", s.up.Addr)
		http.Error(w, "sandbox proxy: upstream unreachable", http.StatusBadGateway)
		return
	}

	req := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\nProxy-Connection: Keep-Alive\r\n\r\n",
		r.Host, r.Host, auth)
	if _, err := io.WriteString(upConn, req); err != nil {
		upConn.Close()
		http.Error(w, "sandbox proxy: upstream write failed", http.StatusBadGateway)
		return
	}

	upReader := bufio.NewReader(upConn)
	resp, err := http.ReadResponse(upReader, r)
	if err != nil {
		upConn.Close()
		http.Error(w, "sandbox proxy: upstream read failed", http.StatusBadGateway)
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

	auth, err := s.authorization()
	if err != nil {
		logrus.WithError(err).Warn("proxy: cannot build upstream credentials")
		http.Error(w, "sandbox proxy: identity token unavailable", http.StatusBadGateway)
		return
	}

	upConn, err := s.dialUpstream()
	if err != nil {
		logrus.WithError(err).Warnf("proxy: cannot reach upstream proxy %s", s.up.Addr)
		http.Error(w, "sandbox proxy: upstream unreachable", http.StatusBadGateway)
		return
	}
	defer upConn.Close()

	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""
	for _, h := range hopByHopHeaders {
		outReq.Header.Del(h)
	}
	outReq.Header.Set("Proxy-Authorization", auth)
	outReq.Close = true

	if err := outReq.WriteProxy(upConn); err != nil {
		http.Error(w, "sandbox proxy: upstream write failed", http.StatusBadGateway)
		return
	}

	resp, err := http.ReadResponse(bufio.NewReader(upConn), outReq)
	if err != nil {
		http.Error(w, "sandbox proxy: upstream read failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	for k, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
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
func (s *shim) authorization() (string, error) {
	token, err := s.readToken()
	if err != nil {
		return "", err
	}

	credentials := s.up.Username + ":" + token
	if s.up.TokenInUser {
		credentials = token + ":"
	}
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(credentials)), nil
}

// readToken reads the identity token, tolerating the non-atomic in-place
// rewrite performed by the workload identity provider: a truncated or empty
// read is retried, and the last known good token is used as a last resort.
func (s *shim) readToken() (string, error) {
	s.mu.Lock()
	if s.cached != "" && time.Since(s.cachedAt) < tokenCacheTTL {
		token := s.cached
		s.mu.Unlock()
		return token, nil
	}
	s.mu.Unlock()

	for attempt := 0; attempt < tornReadRetries; attempt++ {
		data, err := os.ReadFile(s.up.TokenFile)
		if err == nil {
			token := strings.TrimSpace(string(data))
			if s.plausible(token) {
				s.mu.Lock()
				s.lastGood = token
				s.cached = token
				s.cachedAt = time.Now()
				s.mu.Unlock()
				return token, nil
			}
		}
		time.Sleep(tornReadBackoff)
	}

	s.mu.Lock()
	lastGood := s.lastGood
	s.mu.Unlock()
	if lastGood != "" {
		logrus.Warnf("proxy: token file %s unreadable or truncated, reusing last known good token", s.up.TokenFile)
		return lastGood, nil
	}
	return "", fmt.Errorf("token file %s is unreadable or empty", s.up.TokenFile)
}

// plausible rejects reads that look like they caught the token file mid-write:
// empty content, a JWT that lost its three-part structure, or a value that
// suddenly shrank by more than half.
func (s *shim) plausible(token string) bool {
	if token == "" {
		return false
	}

	s.mu.Lock()
	lastGood := s.lastGood
	s.mu.Unlock()
	if lastGood == "" {
		return true
	}
	if strings.Count(lastGood, ".") == 2 && strings.Count(token, ".") != 2 {
		return false
	}
	return len(token) >= len(lastGood)/2
}
