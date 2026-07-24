package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

var fileDirectiveRe = regexp.MustCompile(`\{\{file\(([^)]+)\)\}\}`)

const (
	refreshInterval      = 30 * time.Second
	defaultStateFilePath = "/tmp/sandbox-api-proxy-state.json"
)

type envTemplate struct {
	Name     string `json:"name"`
	Template string `json:"template"`
	FilePath string `json:"filePath"`
}

type proxyState struct {
	Version   int           `json:"version"`
	SavedAt   time.Time     `json:"savedAt"`
	Templates []envTemplate `json:"templates"`
	// Port is the loopback port the credential-injecting proxy listens on. It
	// is persisted so that a restarted sandbox-api rebinds the same port and
	// the HTTP(S)_PROXY value already inherited by running processes stays
	// valid.
	Port int `json:"port,omitempty"`
}

// proxyEnvNames lists every env-var spelling we check. Go's net/http honours
// both upper- and lowercase variants, so we handle all of them.
var proxyEnvNames = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
}

// Start wires up outbound proxy authentication.
//
// The control plane injects HTTP(S)_PROXY with a {{file(...)}} directive in
// place of the password, pointing at the rotating workload identity token.
// Resolving that directive into the environment produces a snapshot that goes
// stale as soon as the token rotates and can never be updated for an
// already-running process, so instead a small proxy is started on loopback and
// HTTP(S)_PROXY is pointed at it. It reads the credential from disk on every
// request, so every consumer authenticates with the current token no matter
// when it was started.
//
// The configuration is persisted so restarts (hot-reload, upgrade) rebind the
// same port even though the env vars no longer contain the raw directive. If no
// directive is found and no state exists, Start is a no-op. If the loopback
// listener cannot be bound, Start falls back to resolving the token into the
// environment on a timer (the previous behaviour) so the sandbox keeps outbound
// connectivity.
func Start(ctx context.Context) {
	templates := collectTemplates(proxyEnvNames...)
	port := 0

	if len(templates) == 0 {
		state := loadState()
		if len(state.Templates) == 0 {
			return
		}
		logrus.Infof("proxy: restored %d template(s) from state file", len(state.Templates))
		templates = state.Templates
		port = state.Port
	}

	if port == 0 {
		port = shimPort()
	}

	if err := startShim(ctx, templates, port); err != nil {
		logrus.WithError(err).Error("proxy: could not start local proxy, falling back to environment token refresh")
		startEnvRefresh(ctx, templates, port)
	}
}

// startShim launches the loopback proxy and points every proxy env var at it.
func startShim(ctx context.Context, templates []envTemplate, port int) error {
	up, err := parseUpstream(templates[0])
	if err != nil {
		return err
	}
	for _, t := range templates[1:] {
		other, err := parseUpstream(t)
		if err != nil {
			return err
		}
		if other != up {
			logrus.Warnf("proxy: %s points at a different upstream than %s, using %s for both",
				t.Name, templates[0].Name, up.Addr)
		}
	}

	ln, err := listenShim(port)
	if err != nil {
		return err
	}

	go (&shim{up: up}).serve(ctx, ln)

	localURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	for _, t := range templates {
		if err := os.Setenv(t.Name, localURL); err != nil {
			logrus.WithError(err).Warnf("proxy: failed to set %s", t.Name)
		}
	}

	logrus.Infof("proxy: local proxy listening on %s, forwarding to %s with credentials from %s",
		localURL, up.Addr, up.TokenFile)

	if err := saveState(templates, port); err != nil {
		logrus.WithError(err).Warn("proxy: failed to persist proxy state")
	}
	return nil
}

// startEnvRefresh is the fallback path: resolve the token into the environment
// and re-resolve it on a timer. Processes started before a rotation keep a
// stale credential, which is why it is only a fallback.
func startEnvRefresh(ctx context.Context, templates []envTemplate, port int) {
	for _, t := range templates {
		if err := resolveAndSet(t); err != nil {
			logrus.WithError(err).Errorf("proxy: initial token resolve failed for %s", t.Name)
		}
	}

	if err := saveState(templates, port); err != nil {
		logrus.WithError(err).Warn("proxy: failed to persist proxy state")
	}

	go refreshLoop(ctx, templates)
}

func collectTemplates(names ...string) []envTemplate {
	var out []envTemplate
	for _, name := range names {
		raw := os.Getenv(name)
		if raw == "" {
			continue
		}
		matches := fileDirectiveRe.FindAllStringSubmatch(raw, -1)
		if matches == nil {
			continue
		}
		if len(matches) > 1 {
			logrus.Warnf("proxy: %s contains %d {{file(...)}} directives; only one is supported", name, len(matches))
			continue
		}
		out = append(out, envTemplate{
			Name:     name,
			Template: raw,
			FilePath: matches[0][1],
		})
		logrus.Infof("proxy: detected {{file(...)}} directive in %s, will refresh token from %s", name, matches[0][1])
	}
	return out
}

var mu sync.Mutex

func resolveAndSet(t envTemplate) error {
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", t.FilePath, err)
	}
	token := strings.TrimSpace(string(data))
	resolved := fileDirectiveRe.ReplaceAllString(t.Template, token)
	mu.Lock()
	defer mu.Unlock()
	return os.Setenv(t.Name, resolved)
}

func refreshLoop(ctx context.Context, templates []envTemplate) {
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logrus.Info("proxy: token refresh stopped")
			return
		case <-ticker.C:
			for _, t := range templates {
				if err := resolveAndSet(t); err != nil {
					logrus.WithError(err).Warnf("proxy: failed to refresh token for %s", t.Name)
				}
			}
		}
	}
}

// --- state persistence ---

func getStateFilePath() string {
	if p := os.Getenv("SANDBOX_PROXY_STATE_FILE"); p != "" {
		return p
	}
	return defaultStateFilePath
}

func saveState(templates []envTemplate, port int) error {
	state := proxyState{
		Version:   2,
		SavedAt:   time.Now(),
		Templates: templates,
		Port:      port,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal proxy state: %w", err)
	}

	path := getStateFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		return fmt.Errorf("write proxy state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename proxy state: %w", err)
	}
	return nil
}

func loadState() proxyState {
	path := getStateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return proxyState{}
	}

	var state proxyState
	if err := json.Unmarshal(data, &state); err != nil {
		logrus.WithError(err).Warn("proxy: corrupt state file, ignoring")
		return proxyState{}
	}
	return state
}
