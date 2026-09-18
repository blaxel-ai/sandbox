package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/archive"
)

type call struct {
	method string
	path   string
}

// quiesceRouter is a router carrying only the gate, so the test exercises the
// routing decision without starting processes or touching the filesystem.
func quiesceRouter(calls []call) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(quiesceMiddleware())
	served := func(c *gin.Context) { c.String(http.StatusOK, "served") }
	for _, c := range calls {
		r.Handle(c.method, c.path, served)
	}
	return r
}

func TestQuiesceMiddleware(t *testing.T) {
	// Refused while frozen: they would write to the filesystem, or start work on
	// a sandbox that is being archived.
	refused := []call{
		{http.MethodPut, "/filesystem/tmp/file"},
		{http.MethodDelete, "/filesystem/tmp/file"},
		{http.MethodPost, "/process"},
		{http.MethodPost, "/drives/mount"},
		{http.MethodPut, "/codegen/fastapply/tmp/file"},
		{http.MethodPost, "/upgrade"},
	}
	// Served while frozen: reading anything, watching the export, interrupting
	// what it could not stop, and the terminal.
	allowed := []call{
		{http.MethodGet, "/"},
		{http.MethodGet, "/health"},
		{http.MethodGet, "/terminal"},
		{http.MethodGet, "/terminal/ws"},
		{http.MethodGet, "/archive/status"},
		{http.MethodPost, "/archive/export"},
		{http.MethodPost, "/archive/resume"},
		{http.MethodGet, "/process"},
		{http.MethodGet, "/process/api/logs"},
		{http.MethodGet, "/filesystem/tmp/file"},
		{http.MethodDelete, "/process/api"},
		{http.MethodDelete, "/process/api/kill"},
		// The host pings this once after an environment update, so a freeze
		// refusing it drops that generation for good. It writes nothing but
		// this process's own environ.
		{http.MethodPost, "/environment/reload"},
	}
	router := quiesceRouter(append(append([]call(nil), refused...), allowed...))

	// Not frozen: everything is served.
	for _, c := range refused {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(c.method, c.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s should be served when no export is running, got %d", c.method, c.path, recorder.Code)
		}
	}

	if err := archive.Freeze("archive export"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = archive.Resume() })

	for _, c := range refused {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(c.method, c.path, nil))
		if recorder.Code != http.StatusServiceUnavailable {
			t.Errorf("%s %s should be refused during an export, got %d", c.method, c.path, recorder.Code)
		}
	}
	for _, c := range allowed {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(c.method, c.path, nil))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s %s should stay available during an export, got %d", c.method, c.path, recorder.Code)
		}
	}

	_, _ = archive.Resume()
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/process", nil))
	if recorder.Code != http.StatusOK {
		t.Errorf("routes should be served again once the freeze is lifted, got %d", recorder.Code)
	}
}
