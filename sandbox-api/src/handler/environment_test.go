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

func TestReloadAppliesEnvironment(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "metadata")
	t.Setenv("BL_METADATA_PATH", doc)
	t.Cleanup(func() {
		os.Unsetenv("RELOAD_TEST_DIRECT")
	})

	if err := os.WriteFile(doc, []byte(`{"generation":3,"environment":{"RELOAD_TEST_DIRECT":"value"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := NewEnvironmentHandler().Reload()
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if response != (ReloadResponse{Generation: 3, Applied: 1}) {
		t.Fatalf("Reload() = %+v, want generation 3 and one applied variable", response)
	}
	if got := os.Getenv("RELOAD_TEST_DIRECT"); got != "value" {
		t.Fatalf("RELOAD_TEST_DIRECT = %q, want %q", got, "value")
	}
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

func TestHandleReloadRemovesBootTimeVariable(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "metadata")
	t.Setenv("BL_METADATA_PATH", doc)
	// A host-injected variable present in the process since boot: the initrd
	// applied it from the same metadata document before exec.
	t.Setenv("RELOAD_TEST_BOOT", "from-host")

	h := NewEnvironmentHandler()

	if err := os.WriteFile(doc, []byte(`{"generation":1,"environment":{"RELOAD_TEST_BOOT":"from-host"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if os.Getenv("RELOAD_TEST_BOOT") != "from-host" {
		t.Fatalf("environment not applied")
	}

	// The document is the host's complete set: dropping the key removes the
	// variable, it does not resurface the boot-time value.
	if err := os.WriteFile(doc, []byte(`{"generation":2,"environment":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if w := performReload(t, h); w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if got, ok := os.LookupEnv("RELOAD_TEST_BOOT"); ok {
		t.Fatalf("removed variable still set, got %q", got)
	}
}

func TestHandleReloadWithoutMetadata(t *testing.T) {
	t.Setenv("BL_METADATA_PATH", filepath.Join(t.TempDir(), "missing"))

	if w := performReload(t, NewEnvironmentHandler()); w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestReloadRemovesDroppedHostManagedVariables(t *testing.T) {
	doc := filepath.Join(t.TempDir(), "metadata")
	t.Setenv("BL_METADATA_PATH", doc)
	t.Setenv("RELOAD_TEST_DROPPED", "inherited")
	t.Setenv("RELOAD_TEST_KEPT", "inherited")

	h := NewEnvironmentHandler()
	h.TrackHostManaged([]string{"RELOAD_TEST_DROPPED", "RELOAD_TEST_KEPT"})

	if err := os.WriteFile(doc, []byte(`{"generation":4,"environment":{"RELOAD_TEST_KEPT":"updated"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	response, err := h.Reload()
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if response.Removed != 1 {
		t.Errorf("Reload() removed %d variables, want 1", response.Removed)
	}
	if response.Applied != 1 {
		t.Errorf("Reload() applied %d variables, want 1", response.Applied)
	}
	if _, ok := os.LookupEnv("RELOAD_TEST_DROPPED"); ok {
		t.Fatal("dropped host-managed variable still set")
	}
	if got := os.Getenv("RELOAD_TEST_KEPT"); got != "updated" {
		t.Fatalf("RELOAD_TEST_KEPT = %q, want %q", got, "updated")
	}
}

func TestReloadWithoutMetadata(t *testing.T) {
	t.Setenv("BL_METADATA_PATH", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("RELOAD_TEST_NO_METADATA", "inherited")

	h := NewEnvironmentHandler()
	h.TrackHostManaged([]string{"RELOAD_TEST_NO_METADATA"})

	_, err := h.Reload()
	if !os.IsNotExist(err) {
		t.Fatalf("Reload() error = %v, want not-exist error", err)
	}
	if got := os.Getenv("RELOAD_TEST_NO_METADATA"); got != "inherited" {
		t.Fatalf("RELOAD_TEST_NO_METADATA = %q, want %q", got, "inherited")
	}
}
