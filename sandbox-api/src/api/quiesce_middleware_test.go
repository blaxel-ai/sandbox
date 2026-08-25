package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/archive"
)

// quiesceRouter is a router carrying only the gate, so the test exercises the
// routing decision without starting processes or touching the filesystem.
func quiesceRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(quiesceMiddleware())
	served := func(c *gin.Context) { c.String(http.StatusOK, "served") }
	for _, route := range []string{"/", "/health", "/terminal", "/terminal/ws", "/archive/status", "/archive/export", "/process", "/filesystem/tmp/file", "/drives/mount"} {
		r.GET(route, served)
	}
	return r
}

func TestQuiesceMiddleware(t *testing.T) {
	router := quiesceRouter()

	// Not frozen: everything is served.
	for _, path := range []string{"/process", "/filesystem/tmp/file"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s should be served when no export is running, got %d", path, recorder.Code)
		}
	}

	if err := archive.Freeze("archive export"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { archive.Resume() })

	// Frozen: the routes that could write to the filesystem are refused, and
	// the terminal stays reachable so an operator is not locked out of a frozen
	// sandbox.
	refused := []string{"/process", "/filesystem/tmp/file", "/drives/mount"}
	allowed := []string{"/", "/health", "/terminal", "/terminal/ws", "/archive/status", "/archive/export"}

	for _, path := range refused {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s should be refused during an export, got %d", path, recorder.Code)
		}
	}
	for _, path := range allowed {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s should stay available during an export, got %d", path, recorder.Code)
		}
	}

	archive.Resume()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/process", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("routes should be served again once the freeze is lifted, got %d", recorder.Code)
	}
}
