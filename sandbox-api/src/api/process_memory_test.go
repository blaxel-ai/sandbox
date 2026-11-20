package api

import (
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

// A long-lived sandbox runs thousands of commands and process records are never
// removed, so a finished process must not keep its output in sandbox-api's heap.
func TestFinishedProcessesDoNotKeepOutputInMemory(t *testing.T) {
	router := newProcessTestRouter(t)
	const processes = 200
	const outputBytes = 200 * 1024
	command := `{"command":"head -c 204800 /dev/zero | tr '\\0' x","waitForCompletion":true}`

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	var lastPID string
	for i := 0; i < processes; i++ {
		body := decodeProcessBody(t, serveProcessRequest(t, router, http.MethodPost, "/process", command).Body.Bytes())
		if got := len(body["stdout"].(string)); got != outputBytes {
			t.Fatalf("exec response stdout = %d bytes, want %d", got, outputBytes)
		}
		lastPID = body["pid"].(string)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	// Holding each process' output would be at least 200 x 200KiB = 40MiB.
	if grown := int64(after.HeapAlloc) - int64(before.HeapAlloc); grown > 10*1024*1024 {
		t.Fatalf("heap grew by %d MiB after %d finished processes", grown/(1024*1024), processes)
	}

	logs := decodeProcessBody(t, serveProcessRequest(t, router, http.MethodGet, "/process/"+lastPID+"/logs", "").Body.Bytes())
	if logs["stdout"] != strings.Repeat("x", outputBytes) {
		t.Fatalf("logs of a finished process: got %d bytes of stdout, want the full output", len(logs["stdout"].(string)))
	}
}

// Once a finished process' output is released, a missing log file must not be
// answered with a pointer to that file.
func TestFinishedProcessWithoutLogFilesReturnsEmptyOutput(t *testing.T) {
	router := newProcessTestRouter(t)
	body := decodeProcessBody(t, serveProcessRequest(t, router, http.MethodPost, "/process",
		`{"command":"echo hello","waitForCompletion":true}`).Body.Bytes())
	if body["stdout"] != "hello\n" {
		t.Fatalf("exec stdout = %q, want %q", body["stdout"], "hello\n")
	}

	entries, err := os.ReadDir(process.ProcessLogDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(process.ProcessLogDir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}

	got := decodeProcessBody(t, serveProcessRequest(t, router, http.MethodGet, "/process/"+body["pid"].(string), "").Body.Bytes())
	if got["stdout"] != "" || got["logs"] != "" {
		t.Fatalf("stdout = %q, logs = %q, want both empty", got["stdout"], got["logs"])
	}
}
