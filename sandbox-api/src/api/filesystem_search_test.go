package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newFilesystemTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	return SetupRouter(true, false)
}

// absoluteRoute addresses an absolute path the way clients do: the leading
// slash is sent encoded.
func absoluteRoute(prefix, path string) string {
	return prefix + "%2F" + strings.TrimPrefix(path, "/")
}

func serveFilesystem(router *gin.Engine, method, target string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestSearchesReturnEmptyArraysWhenNothingMatches(t *testing.T) {
	router := newFilesystemTestRouter(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("ordinary file content\n"), 0644); err != nil {
		t.Fatal(err)
	}

	for _, target := range []string{
		absoluteRoute("/filesystem-content-search", dir) + "?query=nothing-matches-this",
		absoluteRoute("/filesystem-find", dir) + "?patterns=*.nothing",
		absoluteRoute("/filesystem-search", dir) + "?query=nothing-matches-this",
	} {
		response := serveFilesystem(router, http.MethodGet, target)
		if response.Code != http.StatusOK {
			t.Fatalf("GET %s: HTTP %d: %s", target, response.Code, response.Body.String())
		}
		var body map[string]json.RawMessage
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if got := string(body["matches"]); got != "[]" {
			t.Errorf("GET %s: matches = %s, want []", target, got)
		}
	}
}
