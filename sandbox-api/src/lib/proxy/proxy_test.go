package proxy

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCollectTemplates_NoDirective(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://plain-proxy:3128")
	t.Setenv("HTTPS_PROXY", "")

	templates := collectTemplates("HTTP_PROXY", "HTTPS_PROXY")
	if len(templates) != 0 {
		t.Fatalf("expected 0 templates, got %d", len(templates))
	}
}

func TestCollectTemplates_WithDirective(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://user:{{file(/tmp/tok)}}@proxy:3128")
	t.Setenv("HTTPS_PROXY", "http://user:{{file(/tmp/tok2)}}@proxy:3128")

	templates := collectTemplates("HTTP_PROXY", "HTTPS_PROXY")
	if len(templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(templates))
	}
	if templates[0].FilePath != "/tmp/tok" {
		t.Errorf("expected filePath /tmp/tok, got %s", templates[0].FilePath)
	}
	if templates[1].FilePath != "/tmp/tok2" {
		t.Errorf("expected filePath /tmp/tok2, got %s", templates[1].FilePath)
	}
}

func TestCollectTemplates_LowercaseVariants(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "http://u:{{file(/tmp/lc)}}@p:3128")
	t.Setenv("https_proxy", "http://u:{{file(/tmp/lc2)}}@p:3128")

	templates := collectTemplates(proxyEnvNames...)
	if len(templates) != 2 {
		t.Fatalf("expected 2, got %d", len(templates))
	}
	if templates[0].Name != "http_proxy" {
		t.Errorf("expected http_proxy, got %s", templates[0].Name)
	}
	if templates[1].Name != "https_proxy" {
		t.Errorf("expected https_proxy, got %s", templates[1].Name)
	}
}

func TestResolveAndSet(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("secret-abc\n"), 0644); err != nil {
		t.Fatal(err)
	}

	envName := "TEST_PROXY_RESOLVE"
	t.Setenv(envName, "")

	tmpl := envTemplate{
		Name:     envName,
		Template: "http://user:{{file(" + tokenFile + ")}}@proxy:3128",
		FilePath: tokenFile,
	}

	if err := resolveAndSet(tmpl); err != nil {
		t.Fatal(err)
	}

	got := os.Getenv(envName)
	want := "http://user:secret-abc@proxy:3128"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveAndSet_FileNotFound(t *testing.T) {
	tmpl := envTemplate{
		Name:     "DOES_NOT_MATTER",
		Template: "http://{{file(/no/such/file)}}@proxy",
		FilePath: "/no/such/file",
	}
	if err := resolveAndSet(tmpl); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestRefreshLoop_UpdatesOnTokenChange(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("v1"), 0644); err != nil {
		t.Fatal(err)
	}

	envName := "TEST_REFRESH_PROXY"
	t.Setenv(envName, "")

	templates := []envTemplate{{
		Name:     envName,
		Template: "http://{{file(" + tokenFile + ")}}@proxy",
		FilePath: tokenFile,
	}}

	if err := resolveAndSet(templates[0]); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv(envName); got != "http://v1@proxy" {
		t.Fatalf("initial: got %q", got)
	}

	if err := os.WriteFile(tokenFile, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, tmpl := range templates {
		if err := resolveAndSet(tmpl); err != nil {
			t.Fatal(err)
		}
	}

	got := os.Getenv(envName)
	if got != "http://v2@proxy" {
		t.Errorf("after refresh: got %q, want %q", got, "http://v2@proxy")
	}
}

func TestStart_NoOp(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://plain:3128")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("SANDBOX_PROXY_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	Start(ctx)
	<-ctx.Done()

	if got := os.Getenv("HTTP_PROXY"); got != "http://plain:3128" {
		t.Errorf("a proxy url without a directive must be left alone, got %q", got)
	}
}

func TestStart_PointsEnvAtLocalProxy(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("my-token"), 0644); err != nil {
		t.Fatal(err)
	}

	port := freePort(t)
	t.Setenv("SANDBOX_LOCAL_PROXY_PORT", strconv.Itoa(port))
	t.Setenv("HTTP_PROXY", "http://u:{{file("+tokenFile+")}}@host:3128")
	t.Setenv("HTTPS_PROXY", "http://u:{{file("+tokenFile+")}}@host:3128")
	t.Setenv("SANDBOX_PROXY_STATE_FILE", filepath.Join(dir, "state.json"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx)

	want := "http://localhost:" + strconv.Itoa(port)
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		if got := os.Getenv(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if strings.Contains(os.Getenv("HTTP_PROXY"), "my-token") {
		t.Error("the token must never be published to the environment")
	}

	state := loadState()
	if state.Port != port {
		t.Errorf("state port = %d, want %d", state.Port, port)
	}
	if len(state.Templates) != 2 {
		t.Errorf("expected 2 persisted templates, got %d", len(state.Templates))
	}
}

func TestStatePersistence_SaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "proxy-state.json")
	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)

	templates := []envTemplate{
		{Name: "HTTP_PROXY", Template: "http://u:{{file(/tok)}}@host", FilePath: "/tok"},
		{Name: "HTTPS_PROXY", Template: "https://u:{{file(/tok2)}}@host", FilePath: "/tok2"},
	}

	if err := saveState(templates, 4242); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded := loadState()
	if len(loaded.Templates) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(loaded.Templates))
	}
	if loaded.Port != 4242 {
		t.Errorf("port = %d, want 4242", loaded.Port)
	}
	if loaded.Templates[0].Name != "HTTP_PROXY" || loaded.Templates[0].FilePath != "/tok" {
		t.Errorf("template 0 mismatch: %+v", loaded.Templates[0])
	}
	if loaded.Templates[1].Name != "HTTPS_PROXY" || loaded.Templates[1].FilePath != "/tok2" {
		t.Errorf("template 1 mismatch: %+v", loaded.Templates[1])
	}
}

// A restarted sandbox-api sees resolved env vars with no directive left, so it
// must recover the upstream configuration AND the port from state: processes
// started by the previous instance still point at that exact port.
func TestStatePersistence_RestoreSamePortOnRestart(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	stateFile := filepath.Join(dir, "proxy-state.json")
	if err := os.WriteFile(tokenFile, []byte("restored-tok"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)

	port := freePort(t)
	templates := []envTemplate{
		{Name: "HTTP_PROXY", Template: "http://u:{{file(" + tokenFile + ")}}@host:3128", FilePath: tokenFile},
	}
	if err := saveState(templates, port); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: env vars no longer contain the {{file(...)}} directive
	// and SANDBOX_LOCAL_PROXY_PORT would resolve to a different default.
	t.Setenv("SANDBOX_LOCAL_PROXY_PORT", strconv.Itoa(freePort(t)))
	t.Setenv("HTTP_PROXY", "http://u:old-stale-token@host:3128")
	t.Setenv("HTTPS_PROXY", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	Start(ctx)

	got := os.Getenv("HTTP_PROXY")
	want := "http://localhost:" + strconv.Itoa(port)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestLoadState_MissingFile(t *testing.T) {
	t.Setenv("SANDBOX_PROXY_STATE_FILE", "/tmp/does-not-exist-proxy-state.json")
	loaded := loadState()
	if len(loaded.Templates) != 0 {
		t.Errorf("expected empty state, got %+v", loaded)
	}
}

func TestLoadState_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bad.json")
	os.WriteFile(stateFile, []byte("not json!!!"), 0644)
	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)

	loaded := loadState()
	if len(loaded.Templates) != 0 {
		t.Errorf("expected empty state for corrupt file, got %+v", loaded)
	}
}

func TestEnsureLoopbackBypass(t *testing.T) {
	tests := []struct {
		name    string
		initial string
		want    string
	}{
		{name: "empty", initial: "", want: "localhost,127.0.0.1,::1"},
		{name: "appends missing", initial: "10.0.0.0/8,localhost", want: "10.0.0.0/8,localhost,127.0.0.1,::1"},
		{name: "already complete", initial: "localhost,127.0.0.1,::1", want: "localhost,127.0.0.1,::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_PROXY", tt.initial)
			t.Setenv("no_proxy", "")

			ensureLoopbackBypass()

			for _, name := range []string{"NO_PROXY", "no_proxy"} {
				got := os.Getenv(name)
				if tt.initial == tt.want && name == "no_proxy" {
					continue // untouched when nothing had to be added
				}
				if got != tt.want {
					t.Errorf("%s = %q, want %q", name, got, tt.want)
				}
			}
		})
	}
}

// freePort returns a port that is currently unused, so parallel test runs and
// developer machines never collide on the default shim port.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
