package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/lib/blaxel"
)

func performStandby(t *testing.T, checkErr error) (*httptest.ResponseRecorder, chan struct{}) {
	t.Helper()
	requested := make(chan struct{}, 1)
	checkStandby = func() (string, error) { return "", checkErr }
	requestStandby = func() error { requested <- struct{}{}; return nil }
	standbyDelay = 0
	t.Cleanup(func() {
		checkStandby, requestStandby, standbyDelay = blaxel.CheckStandby, blaxel.RequestStandby, 100*time.Millisecond
	})

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/standby", nil)
	NewSystemHandler().HandleStandby(c)
	return w, requested
}

func TestHandleStandbyAccepts(t *testing.T) {
	w, requested := performStandby(t, nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", w.Code)
	}
	if got := w.Header().Get("Connection"); got != "close" {
		t.Errorf("Connection = %q, want close", got)
	}
	select {
	case <-requested:
	case <-time.After(time.Second):
		t.Fatal("standby was never requested")
	}
}

func TestHandleStandbyRefuses(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want int
	}{
		{blaxel.ErrHeldAwake, http.StatusConflict},
		{blaxel.ErrStandbyUnsupported, http.StatusNotImplemented},
	} {
		w, requested := performStandby(t, tt.err)
		if w.Code != tt.want {
			t.Errorf("%v: status = %d, want %d", tt.err, w.Code, tt.want)
		}
		select {
		case <-requested:
			t.Errorf("%v: standby requested despite the refusal", tt.err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
