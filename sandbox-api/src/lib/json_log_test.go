package lib

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

func setupJSONFormatter(buf *bytes.Buffer) func() {
	logrus.SetOutput(buf)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	return func() {
		logrus.SetOutput(os.Stderr)
		logrus.SetFormatter(&logrus.TextFormatter{})
	}
}

func parseJSON(t *testing.T, data []byte) map[string]interface{} {
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("failed to parse JSON log: %v\nraw: %s", err, string(data))
	}
	return m
}

func assertField(t *testing.T, m map[string]interface{}, key, expected string) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("expected field %q to be present", key)
		return
	}
	if s, ok := got.(string); ok {
		if s != expected {
			t.Errorf("field %q: expected %q, got %q", key, expected, s)
		}
	} else {
		t.Errorf("field %q: expected string, got %T", key, got)
	}
}

// TestJSONLog_AuditLog verifies JSON log output for audit events contains all
// expected fields with kebab-case keys.
func TestJSONLog_AuditLog(t *testing.T) {
	var buf bytes.Buffer
	defer setupJSONFormatter(&buf)()

	cases := []struct {
		name     string
		msg      string
		fields   logrus.Fields
		expected map[string]string
	}{
		{
			name: "terminal connect with all fields",
			msg:  "terminal_connect sub-id=user-123 sub-type=user",
			fields: logrus.Fields{
				"source":      "audit",
				"sub-id":      "user-123",
				"sub-type":    "user",
				"auth-method": "api_key",
				"rid":         "req-abc",
				"action":      "terminal_connect",
				"session-id":  "sess-1",
				"shell":       "bash",
				"working-dir": "/home/user",
			},
			expected: map[string]string{
				"source":      "audit",
				"sub-id":      "user-123",
				"sub-type":    "user",
				"auth-method": "api_key",
				"rid":         "req-abc",
				"action":      "terminal_connect",
				"session-id":  "sess-1",
				"shell":       "bash",
				"working-dir": "/home/user",
			},
		},
		{
			name: "process exec with multi-word command",
			msg:  "process_exec sub-id=user-123 rid=req-abc command=npm run dev working-dir=/blaxel/app",
			fields: logrus.Fields{
				"source":      "audit",
				"sub-id":      "user-123",
				"rid":         "req-abc",
				"action":      "process_exec",
				"command":     "npm run dev",
				"working-dir": "/blaxel/app",
			},
			expected: map[string]string{
				"source":      "audit",
				"action":      "process_exec",
				"command":     "npm run dev",
				"working-dir": "/blaxel/app",
			},
		},
		{
			name: "empty value fields are preserved",
			msg:  "terminal_connect sub-id=user-789",
			fields: logrus.Fields{
				"source":      "audit",
				"sub-id":      "user-789",
				"action":      "terminal_connect",
				"shell":       "",
				"working-dir": "",
			},
			expected: map[string]string{
				"source": "audit",
				"sub-id": "user-789",
				"action": "terminal_connect",
				"shell":  "",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.WithFields(tc.fields).Info(tc.msg)
			m := parseJSON(t, buf.Bytes())

			assertField(t, m, "msg", tc.msg)
			assertField(t, m, "level", "info")
			for k, v := range tc.expected {
				assertField(t, m, k, v)
			}
		})
	}
}

// TestJSONLog_AccessLog verifies JSON log output for access log events.
func TestJSONLog_AccessLog(t *testing.T) {
	var buf bytes.Buffer
	defer setupJSONFormatter(&buf)()

	cases := []struct {
		name string
		msg  string
	}{
		{"single word msg", "ok"},
		{"full sentence msg", "GET /process 200 1024 5ms"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.WithField("source", "access").Info(tc.msg)
			m := parseJSON(t, buf.Bytes())

			assertField(t, m, "msg", tc.msg)
			assertField(t, m, "source", "access")
		})
	}
}

// TestJSONLog_ProcessLog verifies JSON log output for process log events
// (stdout/stderr streaming).
func TestJSONLog_ProcessLog(t *testing.T) {
	var buf bytes.Buffer
	defer setupJSONFormatter(&buf)()

	cases := []struct {
		name   string
		msg    string
		stream string
	}{
		{"single word stdout", "started", "stdout"},
		{"full sentence stdout", "server listening on port 8080", "stdout"},
		{"single word stderr", "error", "stderr"},
		{"full sentence stderr", "failed to bind address already in use", "stderr"},
		{"msg with quotes stdout", `Run "astro telemetry disable" to opt-out.`, "stdout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.WithFields(logrus.Fields{
				"source":       "process",
				"process-name": "my-server",
				"process-pid":  "42",
				"stream":       tc.stream,
			}).Info(tc.msg)
			m := parseJSON(t, buf.Bytes())

			assertField(t, m, "msg", tc.msg)
			assertField(t, m, "source", "process")
			assertField(t, m, "process-name", "my-server")
			assertField(t, m, "process-pid", "42")
			assertField(t, m, "stream", tc.stream)
		})
	}
}
