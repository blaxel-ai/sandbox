package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestExecWait(t *testing.T) {
	old := maxExecWaitSeconds
	maxExecWaitSeconds = 115
	t.Cleanup(func() { maxExecWaitSeconds = old })

	for _, tc := range []struct {
		timeout     int
		waits       bool
		wantSeconds int
		wantCapped  bool
	}{
		{timeout: 0, waits: false, wantSeconds: 0, wantCapped: false},     // no wait, nothing to cap
		{timeout: 300, waits: false, wantSeconds: 300, wantCapped: false}, // no wait, process timeout untouched
		{timeout: 0, waits: true, wantSeconds: 115, wantCapped: true},     // no timeout: wait capped
		{timeout: -1, waits: true, wantSeconds: 115, wantCapped: true},    // negative means none
		{timeout: 60, waits: true, wantSeconds: 60, wantCapped: false},
		{timeout: 115, waits: true, wantSeconds: 115, wantCapped: false},
		{timeout: 116, waits: true, wantSeconds: 115, wantCapped: true},
		{timeout: 600, waits: true, wantSeconds: 115, wantCapped: true}, // keepAlive default
	} {
		seconds, capped := execWait(tc.timeout, tc.waits)
		if seconds != tc.wantSeconds || capped != tc.wantCapped {
			t.Errorf("execWait(%d, %v) = (%d, %v), want (%d, %v)", tc.timeout, tc.waits, seconds, capped, tc.wantSeconds, tc.wantCapped)
		}
	}
}

func execRequest(t *testing.T, h *ProcessHandler, body string) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/process", h.HandleExecuteCommand)
	req := httptest.NewRequest(http.MethodPost, "/process", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	start := time.Now()
	r.ServeHTTP(w, req)
	return w, time.Since(start)
}

func TestHandleExecuteCommandWaitLimit(t *testing.T) {
	useTempLogDir(t)
	old := maxExecWaitSeconds
	maxExecWaitSeconds = 1
	t.Cleanup(func() { maxExecWaitSeconds = old })
	h := NewProcessHandler()

	decodeError := func(t *testing.T, w *httptest.ResponseRecorder) ErrorResponse {
		t.Helper()
		var e ErrorResponse
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
		var fields map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &fields); err != nil || len(fields) != 2 {
			t.Fatalf("body %s: want only the existing error and code fields", w.Body.String())
		}
		if e.Code != codeProcessWaitLimitReached || w.Header().Get("X-Blaxel-Error-Code") != codeProcessWaitLimitReached {
			t.Fatalf("code %q, header %q: want %s in both", e.Code, w.Header().Get("X-Blaxel-Error-Code"), codeProcessWaitLimitReached)
		}
		return e
	}
	stillRunning := func(t *testing.T, name string) {
		t.Helper()
		p, err := h.GetProcess(name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = h.KillProcess(p.PID) })
		if p.Status != "running" {
			t.Fatalf("process %s is %s after the capped wait, want running", name, p.Status)
		}
	}

	t.Run("waitForCompletion past the cap answers 422 and leaves the process running", func(t *testing.T) {
		w, took := execRequest(t, h, `{"command":"sleep 30","name":"wait-limit-completion","waitForCompletion":true,"timeout":300}`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if took < time.Second || took > 3*time.Second {
			t.Fatalf("answered after %s, want about 1s", took)
		}
		e := decodeError(t, w)
		for _, want := range []string{`process "wait-limit-completion"`, "is still running after 1 seconds", "120 seconds", "300-second timeout", "/process/wait-limit-completion/wait", "process.wait", "streamLogs", "onLog", "Don't resend"} {
			if !strings.Contains(e.Error, want) {
				t.Errorf("message %q does not contain %q", e.Error, want)
			}
		}
		stillRunning(t, "wait-limit-completion")
	})

	t.Run("no timeout is capped too", func(t *testing.T) {
		w, _ := execRequest(t, h, `{"command":"sleep 30","name":"wait-limit-no-timeout","waitForCompletion":true}`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		e := decodeError(t, w)
		if !strings.Contains(e.Error, "It keeps running until it exits.") {
			t.Errorf("message %q should say the process runs until it exits", e.Error)
		}
		stillRunning(t, "wait-limit-no-timeout")
	})

	t.Run("waitForPorts past the cap names the port", func(t *testing.T) {
		w, _ := execRequest(t, h, `{"command":"sleep 30","name":"wait-limit-ports","waitForPorts":[39217],"timeout":300}`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		e := decodeError(t, w)
		for _, want := range []string{"port 39217 is not open yet", "/process/wait-limit-ports/logs/stream", "/network/process/"} {
			if !strings.Contains(e.Error, want) {
				t.Errorf("message %q does not contain %q", e.Error, want)
			}
		}
		stillRunning(t, "wait-limit-ports")
	})

	t.Run("a timeout under the cap keeps the existing error", func(t *testing.T) {
		maxExecWaitSeconds = 5
		t.Cleanup(func() { maxExecWaitSeconds = 1 })
		w, took := execRequest(t, h, `{"command":"sleep 30","waitForPorts":[39218],"timeout":1}`)
		if w.Code != http.StatusUnprocessableEntity || took > 4*time.Second {
			t.Fatalf("status %d after %s: %s", w.Code, took, w.Body.String())
		}
		if body := w.Body.String(); body != `{"error":"process timed out waiting for ports after 1 seconds"}` {
			t.Fatalf("got %s, want the unchanged timeout error", body)
		}
	})

	t.Run("a command that ends in time returns its result", func(t *testing.T) {
		w, _ := execRequest(t, h, `{"command":"echo done","waitForCompletion":true}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		var p ProcessResponse
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Status != "completed" || strings.TrimSpace(p.Stdout) != "done" {
			t.Fatalf("got status %s stdout %q", p.Status, p.Stdout)
		}
	})
}
