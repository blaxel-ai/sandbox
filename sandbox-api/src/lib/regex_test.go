package lib

import (
	"bytes"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

// logRegexp is the regex used by SigNoz to parse sandbox log lines.
// Fields are ordered alphabetically to match logrus TextFormatter output.
//
// Full regex (copy-paste ready):
// time="(?P<time>[^"]+)" level=(?P<level>\w+) msg="(?P<msg>[^"]+)"(?: action=(?P<blaxel_action>[^\s]*))?(?: authMethod=(?P<blaxel_auth_method>[^\s]*))?(?: command=(?P<blaxel_command>[^\s]*))?(?: processIdentifier=(?P<blaxel_process_identifier>[^\s]*))?(?: processName=(?P<blaxel_pname>[^\s]*))?(?: processPid=(?P<blaxel_pid>[^\s]*))?(?: rid=(?P<blaxel_rid>[^\s]*))?(?: sessionId=(?P<blaxel_session_id>[^\s]*))?(?: shell=(?P<blaxel_shell>[^\s]*))?(?: source=(?P<blaxel_source>[^\s]*))?(?: stream=(?P<blaxel_stream>[^\s]*))?(?: subId=(?P<blaxel_sub_id>[^\s]*))?(?: subType=(?P<blaxel_sub_type>[^\s]*))?(?: workingDir=(?P<blaxel_working_dir>[^\s]*))?
const logRegexp = `time="(?P<time>[^"]+)" level=(?P<level>\w+) msg="(?P<msg>[^"]+)"` +
	`(?: action=(?P<blaxel_action>[^\s]*))?` +
	`(?: authMethod=(?P<blaxel_auth_method>[^\s]*))?` +
	`(?: command=(?P<blaxel_command>[^\s]*))?` +
	`(?: processIdentifier=(?P<blaxel_process_identifier>[^\s]*))?` +
	`(?: processName=(?P<blaxel_pname>[^\s]*))?` +
	`(?: processPid=(?P<blaxel_pid>[^\s]*))?` +
	`(?: rid=(?P<blaxel_rid>[^\s]*))?` +
	`(?: sessionId=(?P<blaxel_session_id>[^\s]*))?` +
	`(?: shell=(?P<blaxel_shell>[^\s]*))?` +
	`(?: source=(?P<blaxel_source>[^\s]*))?` +
	`(?: stream=(?P<blaxel_stream>[^\s]*))?` +
	`(?: subId=(?P<blaxel_sub_id>[^\s]*))?` +
	`(?: subType=(?P<blaxel_sub_type>[^\s]*))?` +
	`(?: workingDir=(?P<blaxel_working_dir>[^\s]*))?`

func setupQuotedFormatter(buf *bytes.Buffer) func() {
	logrus.SetOutput(buf)
	logrus.SetFormatter(&QuotedMsgFormatter{
		TextFormatter: logrus.TextFormatter{
			DisableColors: true,
		},
	})
	return func() {
		logrus.SetOutput(os.Stderr)
		logrus.SetFormatter(&logrus.TextFormatter{})
	}
}

func extractGroups(re *regexp.Regexp, line string) map[string]string {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	groups := make(map[string]string)
	for i, name := range re.SubexpNames() {
		if name != "" {
			groups[name] = m[i]
		}
	}
	return groups
}

// TestLogFormatter_MsgAlwaysQuoted verifies that QuotedMsgFormatter quotes msg
// for both single-word and multi-word messages, making the regex reliable.
func TestLogFormatter_MsgAlwaysQuoted(t *testing.T) {
	var buf bytes.Buffer
	defer setupQuotedFormatter(&buf)()

	cases := []struct {
		name string
		msg  string
	}{
		{"single word", "connected"},
		{"full sentence", "terminal session started successfully"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.Info(tc.msg)
			line := strings.TrimSpace(buf.String())
			want := `msg="` + tc.msg + `"`
			if !strings.Contains(line, want) {
				t.Errorf("expected log line to contain %s, got: %s", want, line)
			}
		})
	}
}

// TestLogRegexp_AuditLog verifies the regex matches audit log lines emitted by
// audit.LogEvent / audit.LogEventDirect, for both single-word and sentence msgs.
func TestLogRegexp_AuditLog(t *testing.T) {
	re := regexp.MustCompile(logRegexp)
	var buf bytes.Buffer
	defer setupQuotedFormatter(&buf)()

	cases := []struct {
		name     string
		msg      string
		fields   logrus.Fields
		expected map[string]string
	}{
		{
			name: "single word msg",
			msg:  "connected",
			fields: logrus.Fields{
				"source":     "audit",
				"subId":      "user-123",
				"subType":    "user",
				"authMethod": "api_key",
				"rid":        "req-abc",
				"action":     "terminal_connect",
				"sessionId":  "sess-1",
				"shell":      "bash",
				"workingDir": "/home/user",
			},
			expected: map[string]string{
				"msg":                  "connected",
				"blaxel_source":        "audit",
				"blaxel_sub_id":        "user-123",
				"blaxel_sub_type":      "user",
				"blaxel_auth_method":   "api_key",
				"blaxel_rid":           "req-abc",
				"blaxel_action":        "terminal_connect",
				"blaxel_session_id":    "sess-1",
				"blaxel_shell":         "bash",
				"blaxel_working_dir":   "/home/user",
			},
		},
		{
			// Regression: empty-value fields (shell=, workingDir=) must not break
			// parsing of subsequent fields like source=audit.
			name: "empty value fields do not block source capture",
			msg:  "audit event",
			fields: logrus.Fields{
				"source":     "audit",
				"subId":      "user-789",
				"subType":    "user",
				"authMethod": "bearer_token",
				"rid":        "req-abc",
				"action":     "terminal_connect",
				"sessionId":  "default",
				"shell":      "",
				"workingDir": "",
			},
			expected: map[string]string{
				"msg":                "audit event",
				"blaxel_source":     "audit",
				"blaxel_sub_id":     "user-789",
				"blaxel_action":     "terminal_connect",
				"blaxel_rid":        "req-abc",
			},
		},
		{
			name: "full sentence msg",
			msg:  "audit event logged successfully",
			fields: logrus.Fields{
				"source":     "audit",
				"subId":      "user-456",
				"subType":    "service",
				"authMethod": "bearer_token",
				"rid":        "req-xyz",
				"action":     "process_exec",
				"workingDir": "/workspace",
			},
			expected: map[string]string{
				"msg":                "audit event logged successfully",
				"blaxel_source":     "audit",
				"blaxel_sub_id":     "user-456",
				"blaxel_sub_type":   "service",
				"blaxel_auth_method": "bearer_token",
				"blaxel_rid":        "req-xyz",
				"blaxel_action":     "process_exec",
				"blaxel_working_dir": "/workspace",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.WithFields(tc.fields).Info(tc.msg)
			line := strings.TrimSpace(buf.String())

			groups := extractGroups(re, line)
			if groups == nil {
				t.Fatalf("regexp did not match audit log line: %s", line)
			}
			for k, v := range tc.expected {
				if groups[k] != v {
					t.Errorf("field %q: expected %q, got %q\n  line: %s", k, v, groups[k], line)
				}
			}
		})
	}
}

// TestLogRegexp_AccessLog verifies the regex matches access log lines emitted by
// logrusMiddleware (source=access, msg contains "METHOD PATH STATUS SIZE LATms").
func TestLogRegexp_AccessLog(t *testing.T) {
	re := regexp.MustCompile(logRegexp)
	var buf bytes.Buffer
	defer setupQuotedFormatter(&buf)()

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
			line := strings.TrimSpace(buf.String())

			groups := extractGroups(re, line)
			if groups == nil {
				t.Fatalf("regexp did not match access log line: %s", line)
			}
			if groups["blaxel_source"] != "access" {
				t.Errorf("expected source=access, got %q\n  line: %s", groups["blaxel_source"], line)
			}
			if groups["msg"] != tc.msg {
				t.Errorf("expected msg=%q, got %q\n  line: %s", tc.msg, groups["msg"], line)
			}
		})
	}
}

// TestLogRegexp_ProcessLog verifies the regex matches process log lines emitted
// when streaming stdout/stderr (source=process, processName, processPid, stream).
func TestLogRegexp_ProcessLog(t *testing.T) {
	re := regexp.MustCompile(logRegexp)
	var buf bytes.Buffer
	defer setupQuotedFormatter(&buf)()

	cases := []struct {
		name   string
		msg    string
		stream string
	}{
		{"single word msg stdout", "started", "stdout"},
		{"full sentence msg stdout", "server listening on port 8080", "stdout"},
		{"single word msg stderr", "error", "stderr"},
		{"full sentence msg stderr", "failed to bind address already in use", "stderr"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			buf.Reset()
			logrus.WithFields(logrus.Fields{
				"source":      "process",
				"processName": "my-server",
				"processPid":  "42",
				"stream":      tc.stream,
			}).Info(tc.msg)
			line := strings.TrimSpace(buf.String())

			groups := extractGroups(re, line)
			if groups == nil {
				t.Fatalf("regexp did not match process log line: %s", line)
			}
			if groups["msg"] != tc.msg {
				t.Errorf("field msg: expected %q, got %q\n  line: %s", tc.msg, groups["msg"], line)
			}
			if groups["blaxel_source"] != "process" {
				t.Errorf("field source: expected %q, got %q\n  line: %s", "process", groups["blaxel_source"], line)
			}
			if groups["blaxel_pname"] != "my-server" {
				t.Errorf("field processName: expected %q, got %q\n  line: %s", "my-server", groups["blaxel_pname"], line)
			}
			if groups["blaxel_pid"] != "42" {
				t.Errorf("field processPid: expected %q, got %q\n  line: %s", "42", groups["blaxel_pid"], line)
			}
			if groups["blaxel_stream"] != tc.stream {
				t.Errorf("field stream: expected %q, got %q\n  line: %s", tc.stream, groups["blaxel_stream"], line)
			}
		})
	}
}
