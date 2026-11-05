package process

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
)

// Budgets are vars so tests can shrink them. Worst case is 3 attempts x 3s
// plus 2s of backoff: a slow receiver costs one goroutine and one scale-to-zero
// hold for ~11s, never the exit path.
var (
	callbackAttemptTimeout = 3 * time.Second
	callbackBackoff        = []time.Duration{500 * time.Millisecond, 1500 * time.Millisecond}
	// Shared so retries and successive exits reuse connections. Redirects are
	// not followed: a redirected POST would be replayed as GET or sent to a
	// host the caller never named.
	callbackClient = &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	callbackSecretWarning sync.Once
	exitWebhooksDisabled  atomic.Bool
)

// DisableExitWebhooks turns exit webhooks off for this instance. The upgrade
// validation instance adopts the same processes as the live one, and clearing
// its env is not enough: startup reloads the secret from the host.
func DisableExitWebhooks() { exitWebhooksDisabled.Store(true) }

// SkipExitWebhook marks a process whose coming stop is not the end of it (an
// archive stops it to relaunch it elsewhere), so the stop sends no webhook.
func (pm *ProcessManager) SkipExitWebhook(identifier string) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	if p := pm.lookupProcessLocked(identifier); p != nil {
		p.skipExitWebhook = true
	}
}

type exitEvent struct {
	Type        string                  `json:"type"`
	Sandbox     string                  `json:"sandbox,omitempty"`
	PID         string                  `json:"pid"`
	Name        string                  `json:"name"`
	Command     string                  `json:"command"`
	ExitCode    int                     `json:"exitCode"`
	Status      constants.ProcessStatus `json:"status"`
	StartedAt   time.Time               `json:"startedAt"`
	CompletedAt *time.Time              `json:"completedAt"`
	Timestamp   int64                   `json:"timestamp"`
}

// ValidateCallbackURL rejects anything but an absolute http(s) URL, so a bad
// value fails the request instead of failing silently at exit time.
func ValidateCallbackURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("callbackUrl must be an absolute http or https URL")
	}
	return nil
}

// signCallback must stay byte-compatible with async-sidecar's
// generateSignature: SDK verifiers check both with the same code.
func signCallback(body []byte, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

// exitCallback is a process.exited webhook ready to send.
type exitCallback struct {
	target, secret, pid string
	body                []byte
	ts                  int64
}

// exitCallbackLocked snapshots p (caller holds pm.mu) into the webhook for its
// final exit, or nil when none is configured. Call only on a final exit.
func exitCallbackLocked(p *ProcessInfo) *exitCallback {
	if p.skipExitWebhook || exitWebhooksDisabled.Load() {
		return nil
	}
	target := p.CallbackURL
	if target == "" {
		target = os.Getenv("BL_PROCESS_CALLBACK_URL")
	}
	if target == "" {
		return nil
	}
	secret := os.Getenv("BL_PROCESS_CALLBACK_SECRET")
	if secret == "" {
		callbackSecretWarning.Do(func() {
			logrus.Warn("[Callback] Process callback URL set but BL_PROCESS_CALLBACK_SECRET is empty; not sending process exit webhooks")
		})
		return nil
	}
	ts := time.Now().Unix()
	body, err := json.Marshal(exitEvent{
		Type:        "process.exited",
		Sandbox:     os.Getenv("BL_NAME"),
		PID:         p.PID,
		Name:        p.Name,
		Command:     p.Command,
		ExitCode:    p.ExitCode,
		Status:      p.Status,
		StartedAt:   p.StartedAt,
		CompletedAt: p.CompletedAt,
		Timestamp:   ts,
	})
	if err != nil {
		logrus.WithError(err).Warn("[Callback] Failed to encode process exit webhook")
		return nil
	}
	return &exitCallback{target: target, secret: secret, pid: p.PID, body: body, ts: ts}
}

// holdAwake is a var so tests can count holds; the real scale file only
// records the last command written.
var holdAwake = blaxel.HoldAwake

// hold keeps the sandbox awake from now until delivery succeeds or gives up,
// and returns the function that starts delivery. Take it before releasing
// the process's own keep-alive: if the scale counter touches 0 in between,
// the sandbox can hibernate before the POST leaves. Safe on nil.
func (c *exitCallback) hold() func() {
	if c == nil {
		return func() {}
	}
	release := holdAwake("process exit webhook " + c.pid)
	return func() {
		go func() {
			defer release()
			sendExitCallback(c.target, c.secret, c.body, c.ts)
		}()
	}
}

func sendExitCallback(target, secret string, body []byte, ts int64) {
	signature := "sha256=" + signCallback(body, secret)
	for attempt := 0; ; attempt++ {
		retry, err := postCallback(target, signature, ts, body)
		if err == nil {
			return
		}
		if !retry || attempt >= len(callbackBackoff) {
			logrus.WithError(err).WithField("attempts", attempt+1).Warn("[Callback] Dropping process exit webhook")
			return
		}
		time.Sleep(callbackBackoff[attempt])
	}
}

// postCallback reports whether a failure is worth retrying: network errors,
// 5xx and 429 are; any other status is the receiver's final answer.
func postCallback(target, signature string, ts int64, body []byte) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callbackAttemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Blaxel-Signature", signature)
	req.Header.Set("X-Blaxel-Timestamp", strconv.FormatInt(ts, 10))
	resp, err := callbackClient.Do(req)
	if err != nil {
		return true, err
	}
	// Drain a little so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	_ = resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, nil
	}
	return resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests,
		errors.New("callback returned " + resp.Status)
}
