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
	// Capture logrus output
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
	// Verify key fields are present in the JSON output
	for _, expected := range []string{
		`"source":"audit"`,
		`"user_id":"user-456"`,
		`"request_id":"req-xyz"`,
		`"action":"test_action"`,
		`"extra_key":"extra_value"`,
	} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected log output to contain %s, got: %s", expected, output)
		}
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
		"session_id": "sess-1",
	})

	output := buf.String()
	for _, expected := range []string{
		`"source":"audit"`,
		`"user_id":"user-789"`,
		`"subject_type":"service"`,
		`"auth_method":"bearer_token"`,
		`"request_id":"req-direct"`,
		`"action":"terminal_disconnect"`,
		`"session_id":"sess-1"`,
	} {
		if !bytes.Contains([]byte(output), []byte(expected)) {
			t.Errorf("expected log output to contain %s, got: %s", expected, output)
		}
	}
}

func TestGetIdentity_WithoutMiddleware(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req, _ := http.NewRequest("GET", "/test", nil)
	c.Request = req

	// Don't run middleware - all values should be empty
	id := GetIdentity(c)
	if id.UserID != "" || id.SubjectType != "" || id.AuthMethod != "" || id.RequestID != "" {
		t.Errorf("expected all empty identity fields without middleware, got: %+v", id)
	}
}
