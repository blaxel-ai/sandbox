package handler

import (
	"context"
	"encoding/base64"
	stdjson "encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/gin-gonic/gin"
)

func TestWantsLogNDJSON(t *testing.T) {
	for _, tc := range []struct {
		accept string
		want   bool
	}{
		{"", false}, {"*/*", false}, {"application/*", false},
		{"text/event-stream", false}, {"application/json", false},
		{"application/x-ndjson", true}, {"text/plain, application/x-ndjson", true},
		{"application/x-ndjson; charset=utf-8", true},
		{"application/x-ndjson;q=0", false},
		{"text/plain;q=1, application/x-ndjson;q=0.2", false},
		{"text/plain;q=0.2, application/x-ndjson;q=1", true},
		{"application/x-ndjson;q=NaN", false}, {"application/x-ndjson;q=bad", false},
		{"application/x-ndjson;q=2", false}, {"application/x-ndjson;q=-1", false},
		{"application/x-ndjson-invalid", false},
	} {
		t.Run(tc.accept, func(t *testing.T) {
			if got := wantsLogNDJSON(tc.accept); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestLogJSONWriterPreservesBytesAndControlRecords(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	w := &logJSONStreamWriter{ResponseWriter: &ResponseWriter{gin: c}}
	chunks := []struct{ stream, data string }{
		{"stdout", "Name?"}, {"stderr", "oops\n"}, {"stdout", "Bob\n"},
		{"stdout", "\xe2"}, {"stdout", "\x82\xac"}, {"stderr", "\x00\xff"},
	}
	for _, chunk := range chunks {
		if _, err := w.WriteEvent(chunk.stream, chunk.data); err != nil {
			t.Fatal(err)
		}
	}
	for _, event := range []string{"keepalive", "truncated", "error"} {
		if _, err := w.WriteEvent(event, ""); err != nil {
			t.Fatal(err)
		}
	}
	decoder := stdjson.NewDecoder(strings.NewReader(recorder.Body.String()))
	for _, chunk := range chunks {
		var event ProcessLogEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		data := []byte(event.Data)
		if event.Encoding == "base64" {
			var err error
			data, err = base64.StdEncoding.DecodeString(event.Data)
			if err != nil {
				t.Fatal(err)
			}
		}
		if event.Type != chunk.stream || string(data) != chunk.data {
			t.Fatalf("got %#v decoded %q, want %#v", event, data, chunk)
		}
	}
	for _, kind := range []string{"keepalive", "truncated", "error"} {
		var event ProcessLogEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Type != kind || event.Data != "" || event.Encoding != "" {
			t.Fatalf("unexpected control record: %#v", event)
		}
	}
	var extra ProcessLogEvent
	if err := decoder.Decode(&extra); err != io.EOF {
		t.Fatalf("unexpected trailing data: %v", err)
	}
	if w.BytesSent() != recorder.Body.Len() {
		t.Fatal("incorrect transmitted byte count")
	}
	w.Close()
	if _, err := w.WriteEvent("stdout", "late"); err == nil {
		t.Fatal("write after close succeeded")
	}
}

func TestLogJSONWriterCancellation(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	ctx, cancel := context.WithCancel(context.Background())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	w := &logJSONStreamWriter{ResponseWriter: &ResponseWriter{gin: c}}
	cancel()
	if _, err := w.WriteEvent("keepalive", ""); err == nil {
		t.Fatal("write after cancellation succeeded")
	}
	if recorder.Body.Len() != 0 {
		t.Fatal("wrote after cancellation")
	}
}

func TestMissingLogStreamReturns404BeforeStreaming(t *testing.T) {
	for _, accept := range []string{"text/plain", "application/x-ndjson"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/process/missing/logs/stream", nil)
		c.Request.Header.Set("Accept", accept)
		c.Params = gin.Params{{Key: "identifier", Value: "missing"}}
		h := &ProcessHandler{BaseHandler: NewBaseHandler(), processManager: process.NewProcessManager()}
		h.HandleGetProcessLogsStream(c)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d", recorder.Code)
		}
		if strings.Contains(recorder.Header().Get("Content-Type"), "ndjson") {
			t.Fatal("error committed as stream")
		}
	}
}

func TestLegacyLogStreamRejectsNDJSONButKeepsText(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "legacy.log")
	if err := os.WriteFile(logPath, []byte("stdout:old output\n"), 0600); err != nil {
		t.Fatal(err)
	}
	completed := time.Now()
	state := process.ManagerState{Version: 1, Processes: map[string]process.ProcessState{
		"123": {PID: "123", Name: "legacy", Status: constants.ProcessStatusCompleted, CompletedAt: &completed, LogFile: logPath},
	}}
	encoded, err := stdjson.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SANDBOX_STATE_FILE", statePath)
	pm := process.NewProcessManager()
	if err := pm.LoadState(); err != nil {
		t.Fatal(err)
	}
	h := &ProcessHandler{BaseHandler: NewBaseHandler(), processManager: pm}
	for _, accept := range []string{"application/x-ndjson", "text/plain"} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/process/legacy/logs/stream", nil)
		c.Request.Header.Set("Accept", accept)
		c.Params = gin.Params{{Key: "identifier", Value: "legacy"}}
		h.HandleGetProcessLogsStream(c)
		if accept == "application/x-ndjson" {
			if recorder.Code != http.StatusConflict || strings.Contains(recorder.Header().Get("Content-Type"), "ndjson") {
				t.Fatalf("legacy NDJSON status=%d content-type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
			}
		} else if recorder.Code != http.StatusOK || recorder.Body.String() != "stdout:old output\n" {
			t.Fatalf("legacy text changed: status=%d body=%q", recorder.Code, recorder.Body.String())
		}
	}
}

func TestLogReplayFailureUsesNDJSONErrorRecord(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing=%t", missing), func(t *testing.T) {
			dir := t.TempDir()
			logPath := filepath.Join(dir, "journal.log")
			if !missing {
				if err := os.WriteFile(logPath, []byte("invalid journal\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			completed := time.Now()
			state := process.ManagerState{Version: 1, Processes: map[string]process.ProcessState{
				"123": {PID: "123", Name: "broken", Status: constants.ProcessStatusCompleted, CompletedAt: &completed, LogFile: logPath, LogFormat: "jsonl-v1"},
			}}
			encoded, err := stdjson.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(dir, "state.json")
			if err := os.WriteFile(statePath, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("SANDBOX_STATE_FILE", statePath)
			pm := process.NewProcessManager()
			if err := pm.LoadState(); err != nil {
				t.Fatal(err)
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/process/broken/logs/stream", nil)
			c.Request.Header.Set("Accept", "application/x-ndjson")
			c.Params = gin.Params{{Key: "identifier", Value: "broken"}}
			h := &ProcessHandler{BaseHandler: NewBaseHandler(), processManager: pm}
			h.HandleGetProcessLogsStream(c)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d", recorder.Code)
			}
			decoder := stdjson.NewDecoder(recorder.Body)
			var last ProcessLogEvent
			for {
				var event ProcessLogEvent
				if err := decoder.Decode(&event); err == io.EOF {
					break
				} else if err != nil {
					t.Fatal(err)
				}
				if event.Type != "error" && event.Type != "truncated" {
					t.Fatalf("failure emitted as output: %+v", event)
				}
				last = event
			}
			if last.Type != "error" || last.Data == "" {
				t.Fatalf("unexpected final event: %+v", last)
			}
		})
	}
}
