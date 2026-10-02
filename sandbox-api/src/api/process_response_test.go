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

func TestProcessCompletedAtWhileRunning(t *testing.T) {
	router := newProcessTestRouter(t)

	// cat blocks on its open stdin, so the process is reliably still running.
	created := serveProcessRequest(t, router, http.MethodPost, "/process",
		`{"command":"cat","name":"completed-at-running","stdin":true}`)
	t.Cleanup(func() {
		request := httptest.NewRequest(http.MethodDelete, "/process/completed-at-running/kill", nil)
		router.ServeHTTP(httptest.NewRecorder(), request)
	})
	body := decodeProcessBody(t, created.Body.Bytes())
	if body["status"] != "running" || body["completedAt"] != "" {
		t.Fatalf("POST /process: status = %v, completedAt = %#v, want running and \"\"", body["status"], body["completedAt"])
	}

	detail := decodeProcessBody(t, serveProcessRequest(t, router, http.MethodGet, "/process/completed-at-running", "").Body.Bytes())
	if detail["completedAt"] != "" {
		t.Fatalf("GET /process/{id}: completedAt = %#v, want \"\"", detail["completedAt"])
	}

	list := serveProcessRequest(t, router, http.MethodGet, "/process", "")
	var listed []map[string]any
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range listed {
		if item["name"] == "completed-at-running" {
			found = true
			if completedAt, present := item["completedAt"]; !present || completedAt != nil {
				t.Fatalf("GET /process: completedAt = %#v (present=%v), want null", completedAt, present)
			}
		}
	}
	if !found {
		t.Fatal("running process missing from the list")
	}
}

func TestProcessExecuteStreamsNDJSONForEitherAccept(t *testing.T) {
	router := newProcessTestRouter(t)
	for _, accept := range []string{"application/x-ndjson", "text/event-stream"} {
		request := httptest.NewRequest(http.MethodPost, "/process", strings.NewReader(`{"command":"echo hi"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", accept)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		if got := response.Header().Get("Content-Type"); got != "application/x-ndjson" {
			t.Fatalf("Accept %s: Content-Type = %q, want application/x-ndjson", accept, got)
		}
		lines := strings.Split(strings.TrimSpace(response.Body.String()), "\n")
		var last struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
			t.Fatalf("Accept %s: last line %q is not JSON: %v", accept, lines[len(lines)-1], err)
		}
		if last.Type != "result" {
			t.Fatalf("Accept %s: last event type = %q, want result", accept, last.Type)
		}
	}
}
