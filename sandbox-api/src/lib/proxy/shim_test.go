package proxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseUpstream(t *testing.T) {
	tests := []struct {
		name     string
		template string
		want     upstreamProxy
	}{
		{
			name:     "password directive",
			template: "http://none:{{file(/tok)}}@proxy.internal:8080",
			want:     upstreamProxy{Addr: "proxy.internal:8080", Username: "none", TokenFile: "/tok"},
		},
		{
			name:     "username directive",
			template: "http://{{file(/tok)}}@proxy.internal:8080",
			want:     upstreamProxy{Addr: "proxy.internal:8080", TokenInUser: true, TokenFile: "/tok"},
		},
		{
			name:     "implicit http port",
			template: "http://none:{{file(/tok)}}@proxy.internal",
			want:     upstreamProxy{Addr: "proxy.internal:80", Username: "none", TokenFile: "/tok"},
		},
		{
			name:     "tls upstream",
			template: "https://none:{{file(/tok)}}@proxy.internal",
			want:     upstreamProxy{Addr: "proxy.internal:443", TLS: true, Username: "none", TokenFile: "/tok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUpstream(envTemplate{Template: tt.template, FilePath: "/tok"})
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseUpstream_Invalid(t *testing.T) {
	if _, err := parseUpstream(envTemplate{Template: "not-a-url", FilePath: "/tok"}); err == nil {
		t.Error("expected an error for a proxy url without a host")
	}
}

func TestReadToken_KeepsPreviousTokenWhenFileIsEmpty(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	const good = "header.payload.signature"
	if err := os.WriteFile(tokenFile, []byte(good), 0644); err != nil {
		t.Fatal(err)
	}

	s := &shim{up: upstreamProxy{TokenFile: tokenFile}}
	if got, err := s.readToken(false); err != nil || got != good {
		t.Fatalf("first read: got %q, err %v", got, err)
	}

	// The workload identity provider rewrites the token in place (O_TRUNC), so
	// a read can land on the file while it is empty.
	if err := os.WriteFile(tokenFile, nil, 0644); err != nil {
		t.Fatal(err)
	}

	got, err := s.readToken(true)
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if got != good {
		t.Errorf("got %q, want the previous token", got)
	}
}

func TestReadToken_CachesThenReloadsOnDemand(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &shim{up: upstreamProxy{TokenFile: tokenFile}}
	if _, err := s.readToken(false); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(tokenFile, []byte("second"), 0644); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.readToken(false); got != "first" {
		t.Errorf("cached read: got %q, want the cached token", got)
	}
	if got, _ := s.readToken(true); got != "second" {
		t.Errorf("forced reload: got %q, want the rotated token", got)
	}
}

func TestReadToken_PicksUpRotation(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("aaa.bbb.ccc"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &shim{up: upstreamProxy{TokenFile: tokenFile}}
	if _, err := s.readToken(false); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(tokenFile, []byte("ddd.eee.fff"), 0644); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.cached, s.cachedAt = "", time.Time{}
	s.mu.Unlock()

	got, err := s.readToken(false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "ddd.eee.fff" {
		t.Errorf("got %q, want the rotated token", got)
	}
}

func TestReadToken_MissingFile(t *testing.T) {
	s := &shim{up: upstreamProxy{TokenFile: "/no/such/token"}}
	if _, err := s.readToken(false); err == nil {
		t.Error("expected an error when no token was ever read")
	}
}

func TestAuthorization(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("tok\n"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &shim{up: upstreamProxy{Username: "none", TokenFile: tokenFile}}
	got, err := s.authorization(false)
	if err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("none:tok"))
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	s = &shim{up: upstreamProxy{TokenInUser: true, TokenFile: tokenFile}}
	got, err = s.authorization(false)
	if err != nil {
		t.Fatal(err)
	}
	want = "Basic " + base64.StdEncoding.EncodeToString([]byte("tok:"))
	if got != want {
		t.Errorf("token-as-username: got %q, want %q", got, want)
	}
}

// fakeUpstream is a minimal forward proxy that records the credentials it was
// presented with and serves plain HTTP requests itself.
type fakeUpstream struct {
	ln net.Listener

	mu sync.Mutex
	// accept, when set, is the only credential the upstream honours; anything
	// else is answered with 407 like a real proxy would after a rotation.
	accept string
	auths  []string
}

func newFakeUpstream(t *testing.T) *fakeUpstream {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	f := &fakeUpstream{ln: ln}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln) //nolint:errcheck
	t.Cleanup(func() { srv.Close() })
	return f
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Proxy-Authorization")

	f.mu.Lock()
	f.auths = append(f.auths, auth)
	accept := f.accept
	f.mu.Unlock()

	if accept != "" && auth != accept {
		http.Error(w, "bad credentials", http.StatusProxyAuthRequired)
		return
	}

	if r.Method == http.MethodConnect {
		// Echo back over the tunnel so the client can assert it works.
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		_, _ = io.Copy(conn, conn)
		return
	}

	fmt.Fprintf(w, "proxied %s", r.URL.String())
}

func (f *fakeUpstream) onlyAccept(auth string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.accept = auth
}

func (f *fakeUpstream) lastAuth(t *testing.T) string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.auths) == 0 {
		t.Fatal("upstream received no request")
	}
	return f.auths[len(f.auths)-1]
}

func startTestShim(t *testing.T, upstreamAddr, tokenFile string) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := &shim{up: upstreamProxy{Addr: upstreamAddr, Username: "none", TokenFile: tokenFile}}
	go s.serve(ctx, ln)

	return "http://" + ln.Addr().String()
}

// The whole point of the shim: a client that captured the proxy URL before a
// rotation still authenticates with the token that is on disk right now.
func TestShim_UsesCurrentTokenForEveryRequest(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("aaa.bbb.ccc"), 0644); err != nil {
		t.Fatal(err)
	}

	upstream := newFakeUpstream(t)
	shimURL := startTestShim(t, upstream.ln.Addr().String(), tokenFile)

	proxyURL, err := url.Parse(shimURL)
	if err != nil {
		t.Fatal(err)
	}
	// A single client, created once — exactly like a long-running process that
	// read HTTP_PROXY at startup and never looks at it again.
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
		Timeout:   10 * time.Second,
	}

	tokens := []string{"aaa.bbb.ccc", "ddd.eee.fff", "ggg.hhh.iii"}
	for _, token := range tokens {
		if err := os.WriteFile(tokenFile, []byte(token), 0644); err != nil {
			t.Fatal(err)
		}
		// Defeat the 1s credential cache without sleeping.
		time.Sleep(tokenCacheTTL + 50*time.Millisecond)

		resp, err := client.Get("http://example.invalid/hello")
		if err != nil {
			t.Fatalf("token %s: %v", token, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("token %s: status %d (%s)", token, resp.StatusCode, body)
		}
		if got, want := string(body), "proxied http://example.invalid/hello"; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}

		want := "Basic " + base64.StdEncoding.EncodeToString([]byte("none:"+token))
		if got := upstream.lastAuth(t); got != want {
			t.Errorf("token %s: upstream saw %q, want %q", token, got, want)
		}
	}
}

func TestShim_ConnectTunnel(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("aaa.bbb.ccc"), 0644); err != nil {
		t.Fatal(err)
	}

	upstream := newFakeUpstream(t)
	shimURL := startTestShim(t, upstream.ln.Addr().String(), tokenFile)
	shimAddr := strings.TrimPrefix(shimURL, "http://")

	conn, err := net.Dial("tcp", shimAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	fmt.Fprint(conn, "CONNECT example.invalid:443 HTTP/1.1\r\nHost: example.invalid:443\r\n\r\n")

	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT failed: %q", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}

	// The fake upstream echoes whatever is written through the tunnel.
	if _, err := io.WriteString(conn, "ping\n"); err != nil {
		t.Fatal(err)
	}
	echo, err := br.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if echo != "ping\n" {
		t.Errorf("tunnel echo = %q, want %q", echo, "ping\n")
	}

	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("none:aaa.bbb.ccc"))
	if got := upstream.lastAuth(t); got != want {
		t.Errorf("upstream saw %q, want %q", got, want)
	}
}

func TestShim_TokenUnavailable(t *testing.T) {
	upstream := newFakeUpstream(t)
	shimURL := startTestShim(t, upstream.ln.Addr().String(), filepath.Join(t.TempDir(), "missing"))

	proxyURL, err := url.Parse(shimURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
		Timeout:   10 * time.Second,
	}

	resp, err := client.Get("http://example.invalid/hello")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadGateway)
	}
}

func TestShimPort_Override(t *testing.T) {
	t.Setenv("SANDBOX_LOCAL_PROXY_PORT", "12345")
	if got := shimPort(); got != 12345 {
		t.Errorf("got %d, want 12345", got)
	}

	t.Setenv("SANDBOX_LOCAL_PROXY_PORT", "not-a-port")
	if got := shimPort(); got != defaultShimPort {
		t.Errorf("got %d, want the default %d", got, defaultShimPort)
	}
}

func basicAuth(token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("none:"+token))
}

// A token can rotate between the shim's last read and the request reaching the
// upstream. The 407 that follows is recovered from by re-reading the file.
func TestShim_RetriesOn407WithReloadedToken(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	upstream := newFakeUpstream(t)
	upstream.onlyAccept(basicAuth("old"))
	shimURL := startTestShim(t, upstream.ln.Addr().String(), tokenFile)

	proxyURL, err := url.Parse(shimURL)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true},
		Timeout:   10 * time.Second,
	}

	// Prime the shim's credential cache with the current token.
	resp, err := client.Get("http://example.invalid/first")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("priming request: status %d", resp.StatusCode)
	}

	// Rotate within the cache window, so the shim's first attempt is stale.
	if err := os.WriteFile(tokenFile, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}
	upstream.onlyAccept(basicAuth("new"))

	resp, err = client.Get("http://example.invalid/second")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d (%s), want 200 after the retry", resp.StatusCode, body)
	}
	if got, want := upstream.lastAuth(t), basicAuth("new"); got != want {
		t.Errorf("upstream saw %q on the retry, want %q", got, want)
	}
}

func TestShim_ConnectRetriesOn407(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}

	upstream := newFakeUpstream(t)
	upstream.onlyAccept(basicAuth("new"))
	shimURL := startTestShim(t, upstream.ln.Addr().String(), tokenFile)

	conn, err := net.Dial("tcp", strings.TrimPrefix(shimURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// The rotation lands after the shim read "old" but before the CONNECT is
	// answered, which is what the retry exists for.
	if err := os.WriteFile(tokenFile, []byte("new"), 0644); err != nil {
		t.Fatal(err)
	}

	fmt.Fprint(conn, "CONNECT example.invalid:443 HTTP/1.1\r\nHost: example.invalid:443\r\n\r\n")

	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT was not retried successfully: %q", status)
	}
	if got, want := upstream.lastAuth(t), basicAuth("new"); got != want {
		t.Errorf("upstream saw %q on the retry, want %q", got, want)
	}
}

// Clients resolve "localhost" to either family, so both must answer.
func TestListenShim_DualStack(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	probe.Close()

	listeners, err := listenShim(port)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, ln := range listeners {
			ln.Close()
		}
	}()

	bound := make(map[string]bool)
	for _, ln := range listeners {
		host, _, err := net.SplitHostPort(ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		bound[host] = true
	}

	if !bound["127.0.0.1"] {
		t.Errorf("no IPv4 loopback listener, bound: %v", bound)
	}
	if !bound["::1"] && supportsIPv6Loopback() {
		t.Errorf("no IPv6 loopback listener, bound: %v", bound)
	}
}

func supportsIPv6Loopback() bool {
	ln, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		return false
	}
	ln.Close()
	return true
}
