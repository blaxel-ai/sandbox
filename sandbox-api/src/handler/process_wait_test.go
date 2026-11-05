package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func waitRequest(t *testing.T, h *ProcessHandler, path string) (*httptest.ResponseRecorder, time.Duration) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/process/:identifier/wait", h.HandleWaitProcess)
	w := httptest.NewRecorder()
	start := time.Now()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w, time.Since(start)
}

func TestHandleWaitProcess(t *testing.T) {
	useTempLogDir(t)
	h := NewProcessHandler()

	quick, err := h.ExecuteProcess("sleep 1; exit 3", "", "wait-quick", nil, false, 0, nil, false, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	slow, err := h.ExecuteProcess("sleep 30", "", "wait-slow", nil, false, 0, nil, false, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.KillProcess(slow.PID) })

	decode := func(w *httptest.ResponseRecorder) ProcessResponse {
		t.Helper()
		var p ProcessResponse
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatalf("decode %q: %v", w.Body.String(), err)
		}
		return p
	}

	t.Run("returns as soon as the process exits", func(t *testing.T) {
		w, took := waitRequest(t, h, "/process/"+quick.PID+"/wait")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		p := decode(w)
		if p.Status == "running" || p.ExitCode != 3 {
			t.Fatalf("expected finished with exit 3, got status=%s exit=%d", p.Status, p.ExitCode)
		}
		if took > 5*time.Second {
			t.Fatalf("wait took %s, expected about 1s", took)
		}
	})

	t.Run("already finished returns immediately, by name", func(t *testing.T) {
		w, took := waitRequest(t, h, "/process/wait-quick/wait")
		if w.Code != http.StatusOK || took > time.Second {
			t.Fatalf("status %d after %s", w.Code, took)
		}
	})

	t.Run("timeout returns the running process", func(t *testing.T) {
		w, took := waitRequest(t, h, "/process/"+slow.PID+"/wait?timeout=1")
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		if p := decode(w); p.Status != "running" {
			t.Fatalf("expected running, got %s", p.Status)
		}
		if took < time.Second || took > 3*time.Second {
			t.Fatalf("wait took %s, expected about 1s", took)
		}
	})

	t.Run("name reused during the wait still reports the waited process", func(t *testing.T) {
		first, err := h.ExecuteProcess("sleep 1", "", "wait-reused", nil, false, 0, nil, false, 0, false, false)
		if err != nil {
			t.Fatal(err)
		}
		go func() {
			time.Sleep(300 * time.Millisecond)
			if second, err := h.ExecuteProcess("sleep 30", "", "wait-reused", nil, false, 0, nil, false, 0, false, false); err == nil {
				t.Cleanup(func() { _ = h.KillProcess(second.PID) })
			}
		}()
		w, _ := waitRequest(t, h, "/process/wait-reused/wait?timeout=5")
		if p := decode(w); p.PID != first.PID || p.Status == "running" {
			t.Fatalf("expected finished pid %s, got pid %s status %s", first.PID, p.PID, p.Status)
		}
	})

	t.Run("invalid timeout is a 400", func(t *testing.T) {
		for _, q := range []string{"abc", "-1", "1.5"} {
			if w, _ := waitRequest(t, h, "/process/"+slow.PID+"/wait?timeout="+q); w.Code != http.StatusBadRequest {
				t.Fatalf("timeout=%s: status %d", q, w.Code)
			}
		}
	})

	t.Run("unknown process is a 404", func(t *testing.T) {
		if w, _ := waitRequest(t, h, "/process/nope-does-not-exist/wait?timeout=0"); w.Code != http.StatusNotFound {
			t.Fatalf("status %d", w.Code)
		}
	})
}
