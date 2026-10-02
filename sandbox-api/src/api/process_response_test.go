package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/gin-gonic/gin"
)

func newProcessTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	originalLogDir := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = originalLogDir })
	gin.SetMode(gin.TestMode)
	return SetupRouter(true, false)
}

func serveProcessRequest(t *testing.T, router *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("%s %s: HTTP %d: %s", method, path, response.Code, response.Body.String())
	}
	return response
}

func decodeProcessBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding %s: %v", raw, err)
	}
	return body
}

func TestProcessResponseReportsInheritedWorkingDir(t *testing.T) {
	router := newProcessTestRouter(t)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	response := serveProcessRequest(t, router, http.MethodPost, "/process",
		`{"command":"pwd","waitForCompletion":true}`)
	body := decodeProcessBody(t, response.Body.Bytes())
	if body["workingDir"] != cwd {
		t.Fatalf("workingDir = %v, want %q", body["workingDir"], cwd)
	}
	if got := strings.TrimSpace(body["stdout"].(string)); got != cwd {
		t.Fatalf("process ran in %q, want %q", got, cwd)
	}

	dir := t.TempDir()
	response = serveProcessRequest(t, router, http.MethodPost, "/process",
		`{"command":"pwd","waitForCompletion":true,"workingDir":"`+dir+`"}`)
	if got := decodeProcessBody(t, response.Body.Bytes())["workingDir"]; got != dir {
		t.Fatalf("explicit workingDir = %v, want %q", got, dir)
	}
}
