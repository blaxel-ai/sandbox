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

func TestFuzzySearchMatchesQueryNotPath(t *testing.T) {
	router := newFilesystemTestRouter(t)
	dir := t.TempDir()
	for _, name := range []string{"main.go", "readme.md"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}

	withoutQuery := serveFilesystem(router, http.MethodGet, absoluteRoute("/filesystem-search", dir))
	if withoutQuery.Code != http.StatusOK {
		t.Fatalf("without query: HTTP %d, want 200 (path used as the pattern)", withoutQuery.Code)
	}

	response := serveFilesystem(router, http.MethodGet, absoluteRoute("/filesystem-search", dir)+"?query=mngo")
	if response.Code != http.StatusOK {
		t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Matches) != 1 || body.Matches[0].Path != "main.go" {
		t.Fatalf("matches = %+v, want only main.go", body.Matches)
	}
}

func TestContentSearchContextLines(t *testing.T) {
	router := newFilesystemTestRouter(t)
	dir := t.TempDir()
	content := "one\ntwo\nthree needle\nfour\nfive\n"
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	search := func(query string) (int, []map[string]any) {
		response := serveFilesystem(router, http.MethodGet, absoluteRoute("/filesystem-content-search", dir)+"?query=needle"+query)
		var body struct {
			Matches []map[string]any `json:"matches"`
		}
		_ = json.Unmarshal(response.Body.Bytes(), &body)
		return response.Code, body.Matches
	}

	for query, want := range map[string]string{
		"&contextLines=1":   "two\nthree needle\nfour",
		"&contextLines=5":   "one\ntwo\nthree needle\nfour\nfive\n",
		"&contextLines=999": "one\ntwo\nthree needle\nfour\nfive\n",
	} {
		code, matches := search(query)
		if code != http.StatusOK || len(matches) != 1 {
			t.Fatalf("%s: HTTP %d, matches %v", query, code, matches)
		}
		if got := matches[0]["context"]; got != want {
			t.Errorf("%s: context = %q, want %q", query, got, want)
		}
	}

	if _, matches := search(""); len(matches) != 1 || matches[0]["context"] != nil {
		t.Errorf("default: matches = %v, want one match without context", matches)
	}
	for _, invalid := range []string{"&contextLines=-1", "&contextLines=abc"} {
		if code, matches := search(invalid); code != http.StatusOK || len(matches) != 1 || matches[0]["context"] != nil {
			t.Errorf("%s: HTTP %d, matches %v, want one match without context", invalid, code, matches)
		}
	}
}
