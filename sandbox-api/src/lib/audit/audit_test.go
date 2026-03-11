package audit

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func setupTestContext(headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c, w
}

func TestIdentityMiddleware_ExtractsHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set(HeaderSubjectID, "user-123")
	req.Header.Set(HeaderSubjectType, "user")
	req.Header.Set(HeaderAuthMethod, "api_key")
	req.Header.Set(HeaderRequestID, "req-abc")
	c.Request = req

	handler := IdentityMiddleware()
	handler(c)

	id := GetIdentity(c)
	if id.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got '%s'", id.UserID)
	}
	if id.SubjectType != "user" {
		t.Errorf("expected SubjectType 'user', got '%s'", id.SubjectType)
	}
	if id.AuthMethod != "api_key" {
		t.Errorf("expected AuthMethod 'api_key', got '%s'", id.AuthMethod)
	}
	if id.RequestID != "req-abc" {
		t.Errorf("expected RequestID 'req-abc', got '%s'", id.RequestID)
	}
}

func TestIdentityMiddleware_GeneratesRequestID(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req

	handler := IdentityMiddleware()
	handler(c)

	id := GetIdentity(c)
	if id.RequestID == "" {
		t.Error("expected a generated RequestID, got empty string")
	}
}

func TestIdentityMiddleware_EmptyHeaders(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req

	handler := IdentityMiddleware()
	handler(c)

	id := GetIdentity(c)
	if id.UserID != "" {
		t.Errorf("expected empty UserID, got '%s'", id.UserID)
	}
	if id.SubjectType != "" {
		t.Errorf("expected empty SubjectType, got '%s'", id.SubjectType)
	}
	if id.AuthMethod != "" {
		t.Errorf("expected empty AuthMethod, got '%s'", id.AuthMethod)
	}
}

func TestLogEvent_EmitsAuditFields(t *testing.T) {
	var buf bytes.Buffer
	logrus.SetOutput(&buf)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	defer func() {
		logrus.SetOutput(os.Stderr)
		logrus.SetFormatter(&logrus.TextFormatter{})
	}()

	c, _ := setupTestContext(map[string]string{
		HeaderSubjectID: "user-456",
		HeaderRequestID: "req-xyz",
	})

	handler := IdentityMiddleware()
	handler(c)

	LogEvent(c, "test_action", logrus.Fields{
		"extra_key": "extra_value",
	})

	output := buf.String()
	for _, expected := range []string{
		`"source":"audit"`,
		`"sub-id":"user-456"`,
		`"rid":"req-xyz"`,
		`"action":"test_action"`,
		`"extra_key":"extra_value"`,
	} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected log output to contain %s, got: %s", expected, output)
		}
	}
	if bytes.Contains([]byte(output), []byte(`"msg":"audit event"`)) {
		t.Errorf("expected msg to contain field details, not generic 'audit event', got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte(`type=test_action`)) {
		t.Errorf("expected msg to contain type=test_action, got: %s", output)
	}
	if !bytes.Contains([]byte(output), []byte(`sub-id=user-456`)) {
		t.Errorf("expected msg to contain sub-id, got: %s", output)
	}
}

func TestLogEventDirect_EmitsAuditFields(t *testing.T) {
	var buf bytes.Buffer
	logrus.SetOutput(&buf)
	logrus.SetFormatter(&logrus.JSONFormatter{})
	defer func() {
		logrus.SetOutput(os.Stderr)
		logrus.SetFormatter(&logrus.TextFormatter{})
	}()

	id := Identity{
		UserID:      "user-789",
		SubjectType: "service",
		AuthMethod:  "bearer_token",
		RequestID:   "req-direct",
	}

	LogEventDirect(id, "terminal_disconnect", logrus.Fields{
		"session-id": "sess-1",
	})

	output := buf.String()
	for _, expected := range []string{
		`"source":"audit"`,
		`"sub-id":"user-789"`,
		`"sub-type":"service"`,
		`"auth-method":"bearer_token"`,
		`"rid":"req-direct"`,
		`"action":"terminal_disconnect"`,
		`"session-id":"sess-1"`,
	} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected log output to contain %s, got: %s", expected, output)
		}
	}
	if bytes.Contains([]byte(output), []byte(`"msg":"audit event"`)) {
		t.Errorf("expected msg to contain field details, not generic 'audit event', got: %s", output)
	}
	expectedInMsg := []string{
		"type=terminal_disconnect",
		"sub-id=user-789",
		"sub-type=service",
		"auth-method=bearer_token",
		"rid=req-direct",
		"session-id=sess-1",
	}
	for _, s := range expectedInMsg {
		if !bytes.Contains([]byte(output), []byte(s)) {
			t.Errorf("expected msg to contain '%s', got: %s", s, output)
		}
	}
}

func TestIdentityMiddleware_FallbackHeaders(t *testing.T) {
	c, _ := setupTestContext(map[string]string{
		HeaderFallbackSubjectID:   "fallback-user",
		HeaderFallbackSubjectType: "fallback-type",
	})

	handler := IdentityMiddleware()
	handler(c)

	id := GetIdentity(c)
	if id.UserID != "fallback-user" {
		t.Errorf("expected UserID 'fallback-user' from fallback header, got '%s'", id.UserID)
	}
	if id.SubjectType != "fallback-type" {
		t.Errorf("expected SubjectType 'fallback-type' from fallback header, got '%s'", id.SubjectType)
	}
}

func TestIdentityMiddleware_BlaxelHeadersPriority(t *testing.T) {
	c, _ := setupTestContext(map[string]string{
		HeaderSubjectID:           "blaxel-user",
		HeaderSubjectType:         "blaxel-type",
		HeaderFallbackSubjectID:   "fallback-user",
		HeaderFallbackSubjectType: "fallback-type",
	})

	handler := IdentityMiddleware()
	handler(c)

	id := GetIdentity(c)
	if id.UserID != "blaxel-user" {
		t.Errorf("expected X-Blaxel-Subject-Id to take priority, got '%s'", id.UserID)
	}
	if id.SubjectType != "blaxel-type" {
		t.Errorf("expected X-Blaxel-Subject-Type to take priority, got '%s'", id.SubjectType)
	}
}

func TestGetIdentity_WithoutMiddleware(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req

	id := GetIdentity(c)
	if id.UserID != "" || id.SubjectType != "" || id.AuthMethod != "" || id.RequestID != "" {
		t.Errorf("expected all empty identity fields without middleware, got: %+v", id)
	}
}

func TestBuildMessage_QuotesValuesWithSpaces(t *testing.T) {
	id := Identity{
		UserID:    "John Doe",
		RequestID: "req-123",
	}
	msg := buildMessage(id, "test_action", logrus.Fields{"cmd": "echo hello world"})
	if !bytes.Contains([]byte(msg), []byte(`sub-id="John Doe"`)) {
		t.Errorf("expected double-quoted UserID, got: %s", msg)
	}
	if !bytes.Contains([]byte(msg), []byte(`cmd="echo hello world"`)) {
		t.Errorf("expected double-quoted cmd, got: %s", msg)
	}
	if !bytes.Contains([]byte(msg), []byte("rid=req-123")) {
		t.Errorf("expected unquoted RequestID (no spaces), got: %s", msg)
	}
	if !bytes.Contains([]byte(msg), []byte("type=test_action")) {
		t.Errorf("expected type= prefix on action, got: %s", msg)
	}
}

func TestBuildMessage_EscapesQuotesInValues(t *testing.T) {
	id := Identity{RequestID: "req-123"}
	msg := buildMessage(id, "process_exec", logrus.Fields{"cmd": `echo "hello world"`})
	if !bytes.Contains([]byte(msg), []byte(`cmd="echo \"hello world\""`)) {
		t.Errorf("expected escaped inner quotes, got: %s", msg)
	}
}

func TestBuildMessage_SanitizesNewlines(t *testing.T) {
	id := Identity{
		UserID:    "user\n{\"fake\":\"inject\"}",
		RequestID: "req-123",
	}
	msg := buildMessage(id, "test_action", logrus.Fields{"cmd": "ls"})
	if bytes.Contains([]byte(msg), []byte("\n")) {
		t.Errorf("msg should not contain raw newlines, got: %s", msg)
	}
	if !bytes.Contains([]byte(msg), []byte(`\n`)) {
		t.Errorf("msg should contain escaped newline, got: %s", msg)
	}
}
