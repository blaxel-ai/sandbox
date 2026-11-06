package process

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// Same vector as async-sidecar's generateSignature and the SDK's
// verify_webhook_signature (hmac.new(secret, body, sha256).hexdigest()).
func TestSignCallbackMatchesAsyncSidecar(t *testing.T) {
	body := []byte(`{"type":"process.exited","pid":"42"}`)
	const want = "1d52c33e98165a2a5722a19d23cf55df1beb338f3cdc3a191d9893fe9e9b591e"
	if got := signCallback(body, "test-secret"); got != want {
		t.Fatalf("signature = %s, want %s", got, want)
	}
}

func fastCallbackBudgets(t *testing.T) {
	attempt, backoff := callbackAttemptTimeout, callbackBackoff
	callbackAttemptTimeout = 200 * time.Millisecond
	callbackBackoff = []time.Duration{10 * time.Millisecond, 30 * time.Millisecond}
	t.Cleanup(func() { callbackAttemptTimeout, callbackBackoff = attempt, backoff })
}

func TestSendExitCallbackRetries(t *testing.T) {
	fastCallbackBudgets(t)
	cases := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		want    int32
	}{
		{"500 retried", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }, 3},
		{"429 retried", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(429) }, 3},
		{"hanging retried", func(http.ResponseWriter, *http.Request) { time.Sleep(time.Second) }, 3},
		{"400 final", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(400) }, 1},
		{"200 once", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				tc.handler(w, r)
			}))
			defer srv.Close()
			start := time.Now()
			sendExitCallback(srv.URL, "s", []byte(`{}`), 1)
			// 3 x 200ms + 40ms backoff, with slack.
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("delivery took %v, budget exceeded", d)
			}
			if got := hits.Load(); got != tc.want {
				t.Fatalf("attempts = %d, want %d", got, tc.want)
			}
		})
	}
}

// Production budgets must stay under the 15s cap even when every attempt hangs.
func TestCallbackBudgetUnder15s(t *testing.T) {
	total := 3 * callbackAttemptTimeout
	for _, b := range callbackBackoff {
		total += b
	}
	if len(callbackBackoff) != 2 || total >= 15*time.Second {
		t.Fatalf("worst case %v with %d retries", total, len(callbackBackoff))
	}
}

const testCallbackSecret = "test-callback-secret-0123456789abcdef"

func TestExitCallbackValidate(t *testing.T) {
	for _, ok := range []ExitCallback{{}, {"http://x.io/h", testCallbackSecret}, {"https://x.io:8443/a?b=c", testCallbackSecret}} {
		if err := ok.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"x.io/h", "/relative", "ftp://x.io", "http://", "://bad"} {
		if (ExitCallback{bad, testCallbackSecret}).Validate() == nil {
			t.Errorf("url %q accepted", bad)
		}
	}
	for _, bad := range []ExitCallback{
		{"https://x.io/h", ""},          // unsigned webhook
		{"https://x.io/h", "too-short"}, // guessable key
		{"", testCallbackSecret},        // secret with nowhere to go
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// One webhook per process lifetime: the failing first run restarts silently,
// the final exit sends exactly one signed POST.
func TestExitCallbackOnlyOnFinalExit(t *testing.T) {
	originalLogDir := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	t.Setenv("BL_NAME", "my-sandbox")
	// The real hold would make the blaxel package cache "no scale file" for
	// every later test in this binary.
	fakeHolds(t, nil)

	type hit struct {
		body []byte
		sig  string
		ts   string
	}
	hits := make(chan hit, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hits <- hit{b, r.Header.Get("X-Blaxel-Signature"), r.Header.Get("X-Blaxel-Timestamp")}
	}))
	defer srv.Close()

	pm := NewProcessManager()
	cmd := `if [ ! -f "$MARKER" ]; then touch "$MARKER"; exit 1; fi; exit 3`
	proc, err := pm.ExecuteProcessWithCallback(ExitCallback{srv.URL, testCallbackSecret},
		"sh -c '"+cmd+"'", "", "notifier",
		map[string]string{"MARKER": ProcessLogDir + "/once"},
		false, 0, nil, true, 1, false, false,
	)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	select {
	case <-proc.Finished:
	case <-time.After(20 * time.Second):
		t.Fatal("process never finished")
	}

	var h hit
	select {
	case h = <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("no webhook received")
	}
	if h.sig != "sha256="+signCallback(h.body, testCallbackSecret) {
		t.Fatalf("bad signature %q", h.sig)
	}
	var ev map[string]any
	if err := json.Unmarshal(h.body, &ev); err != nil {
		t.Fatal(err)
	}
	if ev["type"] != "process.exited" || ev["name"] != "notifier" || ev["exitCode"] != float64(3) ||
		ev["status"] != "failed" || ev["sandbox"] != "my-sandbox" || ev["pid"] != proc.PID {
		t.Fatalf("unexpected payload %s", h.body)
	}
	if h.ts == "" || ev["timestamp"] == nil {
		t.Fatalf("missing timestamp: header %q, body %s", h.ts, h.body)
	}
	select {
	case extra := <-hits:
		t.Fatalf("second webhook sent: %s", extra.body)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestExitCallbackSkippedWithoutSecret(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()
	if exitCallbackLocked(&ProcessInfo{Callback: ExitCallback{URL: srv.URL}}) != nil {
		t.Fatal("webhook prepared without a secret")
	}
}

// fakeHolds counts scale-to-zero holds; the real scale file is a command
// channel that only shows the last command written.
func fakeHolds(t *testing.T, onHold func()) *atomic.Int32 {
	var holds atomic.Int32
	orig := holdAwake
	holdAwake = func(string) func() {
		if onHold != nil {
			onHold()
		}
		holds.Add(1)
		var released atomic.Bool
		return func() {
			if released.CompareAndSwap(false, true) {
				holds.Add(-1)
			}
		}
	}
	t.Cleanup(func() { holdAwake = orig })
	return &holds
}

// The sandbox must stay awake through every attempt and backoff, and the
// hold must be released both when delivery succeeds and when it gives up.
func TestWebhookHoldsSandboxAwakeUntilDone(t *testing.T) {
	fastCallbackBudgets(t)
	for _, tc := range []struct {
		name  string
		codes []int
	}{
		{"delivered after a retry", []int{500, 200}},
		{"given up", []int{500, 500, 500}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holds := fakeHolds(t, nil)
			arrived, proceed := make(chan int32), make(chan struct{})
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				n := hits.Add(1)
				arrived <- n
				<-proceed
				w.WriteHeader(tc.codes[n-1])
			}))
			defer srv.Close()

			(&exitCallback{target: srv.URL, secret: "s", pid: "1", body: []byte(`{}`), ts: 1}).hold()()
			for i := range tc.codes {
				select {
				case <-arrived:
				case <-time.After(2 * time.Second):
					t.Fatalf("attempt %d never arrived", i+1)
				}
				if got := holds.Load(); got != 1 {
					t.Fatalf("attempt %d: holds = %d, want 1 while delivering", i+1, got)
				}
				proceed <- struct{}{}
			}
			deadline := time.Now().Add(2 * time.Second)
			for holds.Load() != 0 && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if got := holds.Load(); got != 0 {
				t.Fatalf("holds = %d after delivery ended, want 0", got)
			}
		})
	}
}

// The webhook hold must be taken while the process's own keep-alive is still
// held, so the scale-to-zero counter never touches 0 between the two.
func TestWebhookHoldTakenBeforeKeepAliveRelease(t *testing.T) {
	originalLogDir := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	t.Setenv("BLAXEL_SCALE_FILE", ProcessLogDir+"/scale")
	t.Setenv("BL_DISABLE_KEEPALIVE", "")
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()

	pm := NewProcessManager()
	keepAliveAtHold := make(chan bool, 1)
	holds := fakeHolds(t, func() {
		p, _ := pm.GetProcessByIdentifier("kept")
		pm.mu.RLock()
		keepAliveAtHold <- p.KeepAlive
		pm.mu.RUnlock()
	})
	proc, err := pm.ExecuteProcessWithCallback(ExitCallback{srv.URL, testCallbackSecret}, "true", "", "kept", nil, true, 30, nil, false, 0, true, false)
	if err != nil {
		t.Fatalf("starting process: %v", err)
	}
	<-proc.Finished
	select {
	case held := <-keepAliveAtHold:
		if !held {
			t.Fatal("webhook hold taken after the process keep-alive was released")
		}
	default:
		t.Fatal("no webhook hold taken")
	}
	deadline := time.Now().Add(2 * time.Second)
	for holds.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := holds.Load(); got != 0 {
		t.Fatalf("holds = %d after delivery, want 0", got)
	}
}

// The upgrade validation instance must never send: the live one does.
func TestDisableExitWebhooks(t *testing.T) {
	t.Cleanup(func() { exitWebhooksDisabled.Store(false) })
	p := &ProcessInfo{Callback: ExitCallback{"http://127.0.0.1:1/hook", testCallbackSecret}}
	if exitCallbackLocked(p) == nil {
		t.Fatal("expected a webhook before disabling")
	}
	DisableExitWebhooks()
	if exitCallbackLocked(p) != nil {
		t.Fatal("webhook prepared while exit webhooks are disabled")
	}
}

// A process found dead on restore is notified once, not on every API restart.
func TestLoadStateNotifiesADeadProcessOnce(t *testing.T) {
	dir := t.TempDir()
	originalLogDir := ProcessLogDir
	ProcessLogDir = dir
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	stateFile := dir + "/state.json"
	t.Setenv("SANDBOX_STATE_FILE", stateFile)
	holds := fakeHolds(t, nil)

	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	dead := exec.Command("true")
	if err := dead.Run(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(ManagerState{Version: 1, SavedAt: time.Now(), Processes: map[string]ProcessState{
		"1234": {PID: "1234", Name: "gone", Command: "true", ProcessPid: dead.Process.Pid,
			StartedAt: time.Now().Add(-time.Minute), Status: StatusRunning, CallbackURL: srv.URL, CallbackSecret: testCallbackSecret},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	for boot := 0; boot < 2; boot++ {
		if err := NewProcessManager().LoadState(); err != nil {
			t.Fatalf("boot %d: %v", boot, err)
		}
		deadline := time.Now().Add(2 * time.Second)
		for holds.Load() != 0 && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("webhooks over two boots = %d, want 1", got)
	}
}

// An archive stops processes to relaunch them elsewhere: not an exit.
func TestSkipExitWebhook(t *testing.T) {
	originalLogDir := ProcessLogDir
	ProcessLogDir = t.TempDir()
	t.Cleanup(func() { ProcessLogDir = originalLogDir })
	holdsTaken := make(chan struct{}, 1)
	fakeHolds(t, func() { holdsTaken <- struct{}{} })
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits.Add(1) }))
	defer srv.Close()

	pm := NewProcessManager()
	proc, err := pm.ExecuteProcessWithCallback(ExitCallback{srv.URL, testCallbackSecret}, "sleep 30", "", "archived", nil, false, 0, nil, false, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	pm.SkipExitWebhook(proc.PID)
	if err := pm.StopProcess(proc.PID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-proc.Finished:
	case <-time.After(10 * time.Second):
		t.Fatal("process never stopped")
	}
	time.Sleep(200 * time.Millisecond)
	if len(holdsTaken) != 0 || hits.Load() != 0 {
		t.Fatalf("archive stop sent a webhook (holds %d, hits %d)", len(holdsTaken), hits.Load())
	}
}
