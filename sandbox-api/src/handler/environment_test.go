package handler

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func performReload(t *testing.T, h *EnvironmentHandler) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/environment/reload", nil)
	h.HandleReload(c)
	return w
}

func TestHandleReloadAppliesAndRemoves(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "metadata")
	orig := metadataPath
	metadataPath = doc
	t.Cleanup(func() { metadataPath = orig })

	h := NewEnvironmentHandler()
	t.Cleanup(func() {
		os.Unsetenv("RELOAD_TEST_A")
		os.Unsetenv("RELOAD_TEST_B")
	})

	if err := os.WriteFile(doc, []byte(`{"generation":1,"environment":{"RELOAD_TEST_A":"1","RELOAD_TEST_B":"2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if os.Getenv("RELOAD_TEST_A") != "1" || os.Getenv("RELOAD_TEST_B") != "2" {
		t.Fatalf("environment not applied")
	}

	// A variable the next generation no longer carries is unset; one it still
	// carries is updated.
	if err := os.WriteFile(doc, []byte(`{"generation":2,"environment":{"RELOAD_TEST_A":"updated"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if os.Getenv("RELOAD_TEST_A") != "updated" {
		t.Fatalf("environment not updated")
	}
	if _, ok := os.LookupEnv("RELOAD_TEST_B"); ok {
		t.Fatalf("removed variable still set")
	}
}

func TestHandleReloadWithoutMetadata(t *testing.T) {
	orig := metadataPath
	metadataPath = filepath.Join(t.TempDir(), "missing")
	t.Cleanup(func() { metadataPath = orig })

	if w := performReload(t, NewEnvironmentHandler()); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
