package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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
}

// proxyEnvNames lists every env-var spelling we check. Go's net/http honours
// both upper- and lowercase variants, so we handle all of them.
var proxyEnvNames = []string{
	"HTTP_PROXY", "http_proxy",
	"HTTPS_PROXY", "https_proxy",
}

// StartProxyTokenRefresh checks HTTP(S)_PROXY (upper and lowercase) for
// {{file(...)}} directives. When found, it reads the referenced file, replaces
// the directive with the file's contents, and starts a background goroutine
// that re-reads the file on a timer so rotated tokens are picked up before
// they expire.
//
// Templates are persisted to disk so that restarts (hot-reload, upgrade)
// continue refreshing even though the env vars no longer contain the raw
// {{file(...)}} directive. If no directives are found and no state file
// exists, the function is a no-op.
func StartProxyTokenRefresh(ctx context.Context) {
	templates := collectTemplates(proxyEnvNames...)

	if len(templates) == 0 {
		restored := loadState()
		if len(restored) == 0 {
			return
		}
		// Verify the token files still exist before restoring; if they were
		// removed the proxy configuration was likely cleaned up intentionally.
		var valid []envTemplate
		for _, t := range restored {
			if _, err := os.Stat(t.FilePath); err == nil {
				valid = append(valid, t)
			} else {
				logrus.Infof("proxy: token file %s no longer exists, dropping %s from state", t.FilePath, t.Name)
			}
		}
		if len(valid) == 0 {
			clearState()
			return
		}
		logrus.Infof("proxy: restored %d template(s) from state file", len(valid))
		templates = valid

		for _, t := range templates {
			os.Setenv(t.Name, t.Template)
		}
	}

	for _, t := range templates {
		if err := resolveAndSet(t); err != nil {
			logrus.WithError(err).Errorf("proxy: initial token resolve failed for %s", t.Name)
		}
	}

	if err := saveState(templates); err != nil {
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
}

func resolveAndSet(t envTemplate) error {
	data, err := os.ReadFile(t.FilePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", t.FilePath, err)
	}
	token := strings.TrimSpace(string(data))
	resolved := fileDirectiveRe.ReplaceAllString(t.Template, token)
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

func saveState(templates []envTemplate) error {
	state := proxyState{
		Version:   1,
		SavedAt:   time.Now(),
		Templates: templates,
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

func loadState() []envTemplate {
	path := getStateFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var state proxyState
	if err := json.Unmarshal(data, &state); err != nil {
		logrus.WithError(err).Warn("proxy: corrupt state file, ignoring")
		return nil
	}
	return state.Templates
}

func clearState() {
	path := getStateFilePath()
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		logrus.WithError(err).Warn("proxy: failed to remove stale state file")
	}
}
