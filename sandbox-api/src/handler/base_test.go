package handler

import (
	stdjson "encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHandleWelcome(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	NewBaseHandler().HandleWelcome(ctx)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var body map[string]string
	if err := stdjson.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"apiReference":  "/swagger/doc.json",
		"message":       "Welcome to your Blaxel Sandbox. Discover your capabilities here: /swagger/doc.json",
		"documentation": "https://docs.blaxel.ai/Sandboxes/Overview",
		"description":   "This sandbox provides a full-featured environment for running code securely. Visit the documentation to learn how to manage processes, access the filesystem, and more.",
	}
	for key, value := range want {
		if body[key] != value {
			t.Errorf("%s = %q, want %q", key, body[key], value)
		}
	}
}
