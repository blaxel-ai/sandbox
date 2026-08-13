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
	t.Setenv("BL_METADATA_PATH", doc)

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

func TestHandleReloadRestoresPreExistingValue(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "metadata")
	t.Setenv("BL_METADATA_PATH", doc)
	t.Setenv("RELOAD_TEST_IMAGE", "from-image")

	h := NewEnvironmentHandler()

	// The metadata overrides a variable the process already had.
	if err := os.WriteFile(doc, []byte(`{"generation":1,"environment":{"RELOAD_TEST_IMAGE":"override"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if os.Getenv("RELOAD_TEST_IMAGE") != "override" {
		t.Fatalf("environment not overridden")
	}

	// When the next generation drops it, the original value comes back.
	if err := os.WriteFile(doc, []byte(`{"generation":2,"environment":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got := os.Getenv("RELOAD_TEST_IMAGE"); got != "from-image" {
		t.Fatalf("pre-existing value not restored, got %q", got)
	}
}

func TestHandleReloadWithoutMetadata(t *testing.T) {
	t.Setenv("BL_METADATA_PATH", filepath.Join(t.TempDir(), "missing"))

	if w := performReload(t, NewEnvironmentHandler()); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
