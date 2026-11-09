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
	"sync/atomic"
	"syscall"
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
	exitWebhooksDisabled atomic.Bool
	// exitWebhooksInFlight counts deliveries that have been committed to (held)
	// and not yet sent or dropped, so shutdown and upgrade can wait for them.
	exitWebhooksInFlight atomic.Int64
)

// ErrNotRunning is returned by StopForMigration for a process that has
// already ended on its own.
var ErrNotRunning = errors.New("process is not running")

// exitWebhooksWaitLimit bounds WaitExitWebhooks. A delivery gives up within
// ~11s on its own, so this only bites when new exits keep arriving during
// the wait (an upgrade leaves processes running) and would otherwise hold
// shutdown forever.
const exitWebhooksWaitLimit = 15 * time.Second

// WaitExitWebhooks returns once every exit webhook already committed to has
// been delivered or dropped, or after exitWebhooksWaitLimit. Call it before
// the process exits or execs, which would kill those deliveries.
// ponytail: an atomic counter polled rather than a sync.WaitGroup: exits keep
// happening during the wait, and a WaitGroup panics when Add races a Wait
// that is waking up at zero.
func WaitExitWebhooks() {
	deadline := time.Now().Add(exitWebhooksWaitLimit)
	for exitWebhooksInFlight.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
}

// DisableExitWebhooks turns exit webhooks off for this instance. The upgrade
// validation instance adopts the same processes as the live one, and clearing
// its env is not enough: startup reloads the secret from the host.
func DisableExitWebhooks() { exitWebhooksDisabled.Store(true) }

// StopForMigration sends SIGTERM to a process whose stop is not the end of it
// (an archive stops it to relaunch it elsewhere), so the stop sends no
// webhook. Check and mark happen under one lock: a process that has already
// ended on its own returns ErrNotRunning and keeps its webhook.
func (pm *ProcessManager) StopForMigration(identifier string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	p := pm.lookupProcessLocked(identifier)
	if p == nil {
		return fmt.Errorf("process with Identifier %s not found", identifier)
	}
	if p.Status != StatusRunning {
		return ErrNotRunning
	}
	p.skipExitWebhook = true
	return pm.signalProcessLocked(identifier, syscall.SIGTERM, StatusStopped)
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

// callbackSecretMinLength keeps caller-chosen secrets out of guessing range:
// anyone who can guess one can forge webhooks to the caller's receiver.
const callbackSecretMinLength = 32

// ExitCallback is where a process's exit webhook goes and the key it is
// signed with. Both come from the POST /process caller; the zero value means
// no webhook.
type ExitCallback struct {
	URL    string
	Secret string
}

// Validate rejects a callback that could only fail at exit time, or that would
// send unsigned or weakly signed webhooks.
func (c ExitCallback) Validate() error {
	if c.URL == "" {
		if c.Secret != "" {
			return fmt.Errorf("callbackSecret is set without a callbackUrl")
		}
		return nil
	}
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("callbackUrl must be an absolute http or https URL")
	}
	if len(c.Secret) < callbackSecretMinLength {
		return fmt.Errorf("callbackSecret must be at least %d characters when callbackUrl is set", callbackSecretMinLength)
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
	if p.Callback.URL == "" || p.Callback.Secret == "" {
		return nil
	}
	target, secret := p.Callback.URL, p.Callback.Secret
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
	exitWebhooksInFlight.Add(1) // before markFinished, so a waiter on Finished sees it
	return func() {
		go func() {
			defer exitWebhooksInFlight.Add(-1)
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
