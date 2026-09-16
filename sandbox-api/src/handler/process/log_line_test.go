package process

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// captureProcessLine runs one process output line through logProcessLine with
// the same fields readAndBroadcast attaches and the same JSON formatter
// sandbox-api ships with, returning the emitted telemetry record.
func captureProcessLine(t *testing.T, streamType, line string) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := logrus.New()
	logger.SetOutput(&buf)
	logger.SetFormatter(&logrus.JSONFormatter{})
	logger.SetLevel(logrus.DebugLevel)
	entry := logger.WithFields(logrus.Fields{
		"source":       "process",
		"process-name": "build",
		"process-pid":  "42",
		"stream":       streamType,
	})

	logProcessLine(entry, streamType, []byte(line+"\n"))

	if buf.Len() == 0 {
		return nil
	}
	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("emitted record is not JSON: %v\n%s", err, buf.String())
	}
	return record
}

// A builder printing structured logs on stdout used to come out as an INFO
// record whose msg was the whole inner JSON as text: the build's ERROR
// severity and trace ids were unreachable to anything querying telemetry.
func TestStructuredLineKeepsSeverityAndTrace(t *testing.T) {
	line := `{"message":"error: failed to solve: \"/hub/astro/entrypoint.sh\": not found",` +
		`"severity":"ERROR","labels":{"blaxel-workspace":"canary"},` +
		`"trace_id":"7290becbf792fbfcb03821cdc75b3364","span_id":"83fbf516e463c3f8"}`

	got := captureProcessLine(t, "stdout", line)

	if got["msg"] != `error: failed to solve: "/hub/astro/entrypoint.sh": not found` {
		t.Errorf("msg = %q, want the inner message", got["msg"])
	}
	if got["level"] != "error" {
		t.Errorf("level = %q, want error from the line's severity, not the stream", got["level"])
	}
	if got["trace_id"] != "7290becbf792fbfcb03821cdc75b3364" || got["span_id"] != "83fbf516e463c3f8" {
		t.Errorf("trace_id/span_id = %v/%v, want them lifted to top level", got["trace_id"], got["span_id"])
	}
	labels, _ := got["labels"].(map[string]any)
	if labels["blaxel-workspace"] != "canary" {
		t.Errorf("labels = %v, want the line's labels preserved", got["labels"])
	}
	for key, want := range map[string]string{
		"source": "process", "process-name": "build", "process-pid": "42", "stream": "stdout",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}
	if _, nested := got["message"]; nested {
		t.Errorf("record still carries a nested message field: %v", got)
	}
}

// Structured stderr that says it is INFO stays INFO; the stream is only the
// default for lines that do not carry their own severity.
func TestStructuredStderrUsesLineSeverity(t *testing.T) {
	got := captureProcessLine(t, "stderr", `{"msg":"pulling layer","level":"info"}`)
	if got["level"] != "info" {
		t.Errorf("level = %q, want info", got["level"])
	}
	if got["msg"] != "pulling layer" {
		t.Errorf("msg = %q", got["msg"])
	}
	if got["stream"] != "stderr" {
		t.Errorf("stream = %q, want stderr", got["stream"])
	}
}

func TestStructuredLineUnknownSeverityFallsBackToStream(t *testing.T) {
	got := captureProcessLine(t, "stderr", `{"message":"x","severity":"LOUD"}`)
	if got["level"] != "error" {
		t.Errorf("level = %q, want the stderr default", got["level"])
	}
	got = captureProcessLine(t, "stdout", `{"message":"x"}`)
	if got["level"] != "info" {
		t.Errorf("level = %q, want the stdout default", got["level"])
	}
}

// A process must not be able to impersonate another one or hide where its
// output came from.
func TestStructuredLineCannotOverrideProcessFields(t *testing.T) {
	got := captureProcessLine(t, "stdout",
		`{"message":"x","source":"api","process-name":"other","process-pid":"1","stream":"stderr"}`)
	for key, want := range map[string]string{
		"source": "process", "process-name": "build", "process-pid": "42", "stream": "stdout",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}
}

// Fatal and panic severities are logged as errors; logrus would otherwise exit
// or panic sandbox-api on behalf of the process.
func TestStructuredFatalIsLoggedAsError(t *testing.T) {
	for _, sev := range []string{"FATAL", "panic", "CRITICAL"} {
		got := captureProcessLine(t, "stdout", `{"message":"boom","severity":"`+sev+`"}`)
		if got["level"] != "error" {
			t.Errorf("%s: level = %q, want error", sev, got["level"])
		}
	}
}

// Plain text keeps the previous behaviour: verbatim message, stream severity.
func TestPlainTextLineIsUnchanged(t *testing.T) {
	cases := []struct {
		stream, line, level string
	}{
		{"stdout", "#24 [stage-1 8/10] COPY /hub/astro/entrypoint.sh /entrypoint.sh", "info"},
		{"stderr", "error: build failed: the image build failed", "error"},
		{"stdout", `{"message":"cut off by the read window`, "info"},
		{"stdout", `{"count":3}`, "info"},
		{"stdout", `{"message":42}`, "info"},
		{"stdout", `[1,2,3]`, "info"},
	}
	for _, tc := range cases {
		got := captureProcessLine(t, tc.stream, tc.line)
		if got["msg"] != tc.line {
			t.Errorf("%q: msg = %q, want verbatim", tc.line, got["msg"])
		}
		if got["level"] != tc.level {
			t.Errorf("%q: level = %q, want %s", tc.line, got["level"], tc.level)
		}
		if _, ok := got["message"]; ok {
			t.Errorf("%q: unexpected lifted fields: %v", tc.line, got)
		}
	}
}

func TestEmptyLineIsNotLogged(t *testing.T) {
	if got := captureProcessLine(t, "stdout", ""); got != nil {
		t.Errorf("empty line emitted %v", got)
	}
}

func TestOverlongStructuredLineIsTruncatedVerbatim(t *testing.T) {
	line := `{"message":"` + strings.Repeat("a", maxLoggedLineBytes) + `","severity":"ERROR"}`
	got := captureProcessLine(t, "stdout", line)
	if got["level"] != "info" {
		t.Errorf("level = %q, want the stream default once the JSON is cut", got["level"])
	}
	if msg, _ := got["msg"].(string); len(msg) != maxLoggedLineBytes {
		t.Errorf("msg length = %d, want %d", len(msg), maxLoggedLineBytes)
	}
}
