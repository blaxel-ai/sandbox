package proxy

import (
	"context"
	"os"
	"path/filepath"
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

func TestStartProxyTokenRefresh_NoOp(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://plain:3128")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("SANDBOX_PROXY_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	StartProxyTokenRefresh(ctx)
	<-ctx.Done()
}

func TestStartProxyTokenRefresh_WithDirective(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("my-token"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HTTP_PROXY", "http://u:{{file("+tokenFile+")}}@host:3128")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("SANDBOX_PROXY_STATE_FILE", filepath.Join(dir, "state.json"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartProxyTokenRefresh(ctx)

	got := os.Getenv("HTTP_PROXY")
	want := "http://u:my-token@host:3128"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
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

	if err := saveState(templates); err != nil {
		t.Fatalf("saveState: %v", err)
	}

	loaded := loadState()
	if len(loaded) != 2 {
		t.Fatalf("expected 2 templates, got %d", len(loaded))
	}
	if loaded[0].Name != "HTTP_PROXY" || loaded[0].FilePath != "/tok" {
		t.Errorf("template 0 mismatch: %+v", loaded[0])
	}
	if loaded[1].Name != "HTTPS_PROXY" || loaded[1].FilePath != "/tok2" {
		t.Errorf("template 1 mismatch: %+v", loaded[1])
	}
}

func TestStatePersistence_RestoreOnRestart(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	stateFile := filepath.Join(dir, "proxy-state.json")
	if err := os.WriteFile(tokenFile, []byte("restored-tok"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)

	original := "http://u:{{file(" + tokenFile + ")}}@host:3128"
	templates := []envTemplate{
		{Name: "HTTP_PROXY", Template: original, FilePath: tokenFile},
	}
	if err := saveState(templates); err != nil {
		t.Fatal(err)
	}

	// Simulate a restart: env vars no longer contain the {{file(...)}} directive
	t.Setenv("HTTP_PROXY", "http://u:old-stale-token@host:3128")
	t.Setenv("HTTPS_PROXY", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartProxyTokenRefresh(ctx)

	got := os.Getenv("HTTP_PROXY")
	want := "http://u:restored-tok@host:3128"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStatePersistence_ClearedWhenTokenFileRemoved(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	stateFile := filepath.Join(dir, "proxy-state.json")

	// Create token file and state, then remove the token file
	if err := os.WriteFile(tokenFile, []byte("tok"), 0644); err != nil {
		t.Fatal(err)
	}
	templates := []envTemplate{
		{Name: "HTTP_PROXY", Template: "http://{{file(" + tokenFile + ")}}@h", FilePath: tokenFile},
	}
	if err := saveState(templates); err != nil {
		t.Fatal(err)
	}
	os.Remove(tokenFile)

	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	StartProxyTokenRefresh(ctx)

	// State file should have been cleaned up
	if _, err := os.Stat(stateFile); !os.IsNotExist(err) {
		t.Error("expected state file to be removed after token file was deleted")
	}
}

func TestLoadState_MissingFile(t *testing.T) {
	t.Setenv("SANDBOX_PROXY_STATE_FILE", "/tmp/does-not-exist-proxy-state.json")
	loaded := loadState()
	if loaded != nil {
		t.Errorf("expected nil, got %+v", loaded)
	}
}

func TestLoadState_CorruptFile(t *testing.T) {
	dir := t.TempDir()
	stateFile := filepath.Join(dir, "bad.json")
	os.WriteFile(stateFile, []byte("not json!!!"), 0644)
	t.Setenv("SANDBOX_PROXY_STATE_FILE", stateFile)

	loaded := loadState()
	if loaded != nil {
		t.Errorf("expected nil for corrupt file, got %+v", loaded)
	}
}
