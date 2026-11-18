package handler

import (
	"bufio"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// serveExec sends one POST /process to the handler, streamed when accept asks
// for it.
func serveExec(t *testing.T, h *ProcessHandler, body, accept string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/process", h.HandleExecuteCommand)
	req := httptest.NewRequest(http.MethodPost, "/process", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func execBody(t *testing.T, workingDir string) string {
	t.Helper()
	b, err := json.Marshal(map[string]any{"command": "pwd", "workingDir": workingDir, "waitForCompletion": true})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sortedKeys(m map[string]any) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func TestHandleExecuteCommandWorkingDirUnusable(t *testing.T) {
	useTempLogDir(t)
	h := NewProcessHandler()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ name, dir, next string }{
		{"path is a file", file, "Set workingDir to a directory"},
		{"parent is a file", filepath.Join(file, "sub", "dir"), "Set workingDir to a directory"},
	}
	if os.Geteuid() != 0 {
		// Root may create anywhere, so only a non-root API sees the permission error.
		readOnly := filepath.Join(t.TempDir(), "read-only")
		if err := os.Mkdir(readOnly, 0o555); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct{ name, dir, next string }{"user may not create it", filepath.Join(readOnly, "work"), "the command's user can create"})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := serveExec(t, h, execBody(t, tc.dir), "")
			if w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode %q: %v", w.Body.String(), err)
			}
			if keys := sortedKeys(body); keys != "code,error" {
				t.Errorf("body keys %s, want code,error: %s", keys, w.Body.String())
			}
			if body["code"] != codeProcessWorkingDirUnusable {
				t.Errorf("code = %v", body["code"])
			}
			msg, _ := body["error"].(string)
			checkWorkingDirMessage(t, msg, tc.dir, tc.next)
			if got := w.Header().Get("X-Blaxel-Error-Code"); got != codeProcessWorkingDirUnusable {
				t.Errorf("X-Blaxel-Error-Code = %q", got)
			}
		})

		t.Run(tc.name+", streamed", func(t *testing.T) {
			w := serveExec(t, h, execBody(t, tc.dir), "application/x-ndjson")
			// Headers go out before the process starts, so the failure arrives
			// as the stream's error event, as it always has.
			if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") {
				t.Fatalf("status %d, content type %q", w.Code, w.Header().Get("Content-Type"))
			}
			var lines []string
			scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
			for scanner.Scan() {
				if line := strings.TrimSpace(scanner.Text()); line != "" {
					lines = append(lines, line)
				}
			}
			if err := scanner.Err(); err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 {
				t.Fatalf("want one error event, got %q", w.Body.String())
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
				t.Fatal(err)
			}
			if keys := sortedKeys(event); keys != "code,data,type" {
				t.Errorf("event keys %s, want code,data,type: %s", keys, lines[0])
			}
			if event["type"] != "error" || event["code"] != codeProcessWorkingDirUnusable {
				t.Errorf("event %s", lines[0])
			}
			data, _ := event["data"].(string)
			checkWorkingDirMessage(t, data, tc.dir, tc.next)
		})
	}

	t.Run("a missing directory is created on both paths", func(t *testing.T) {
		base := t.TempDir()
		if w := serveExec(t, h, execBody(t, filepath.Join(base, "json", "work")), ""); w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
		w := serveExec(t, h, execBody(t, filepath.Join(base, "stream", "work")), "application/x-ndjson")
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"type":"result"`) || strings.Contains(w.Body.String(), `"type":"error"`) {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	})
}

// checkWorkingDirMessage checks that an unusable working directory's message
// keeps the original error, names the directory, says the command did not run
// and gives the fix for its cause.
func checkWorkingDirMessage(t *testing.T, msg, dir, next string) {
	t.Helper()
	if !strings.HasPrefix(msg, "failed to start process: ") || !strings.Contains(msg, "'"+dir+"'") {
		t.Errorf("message %q changed its wording or does not name %s", msg, dir)
	}
	if !strings.Contains(msg, ". The command did not run") || !strings.Contains(msg, next) || !strings.HasSuffix(msg, "then resend.") {
		t.Errorf("message %q does not say the command did not run and %q", msg, next)
	}
}

func TestWorkingDirErrorIgnoresOtherErrors(t *testing.T) {
	if code, _, ok := workingDirError(errors.New("failed to start process: working directory")); ok || code != "" {
		t.Fatalf("a plain error got code %q", code)
	}
}
