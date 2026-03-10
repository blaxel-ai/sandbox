// Package audit provides audit logging for sandbox access events.
// Audit logs are emitted with source="audit" to distinguish them
// from process logs (source="process") and HTTP access logs in SigNoz.
package audit

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// Header names for identity context forwarded by the cluster-gateway.
// X-Blaxel-* headers take priority over the generic X-Subject-* headers.
const (
	HeaderSubjectID        = "X-Blaxel-Subject-Id"
	HeaderSubjectType      = "X-Blaxel-Subject-Type"
	HeaderAuthMethod       = "X-Blaxel-Auth-Method"
	HeaderRequestID        = "X-Request-Id"
	HeaderFallbackSubjectID   = "X-Subject-Id"
	HeaderFallbackSubjectType = "X-Subject-Type"
)

// Context keys used to store identity information in gin.Context.
const (
	ContextKeyUserID      = "audit_user_id"
	ContextKeySubjectType = "audit_subject_type"
	ContextKeyAuthMethod  = "audit_auth_method"
	ContextKeyRequestID   = "audit_request_id"
)

// headerWithFallback returns the value of the primary header, falling back
// to the fallback header if the primary is empty.
func headerWithFallback(c *gin.Context, primary, fallback string) string {
	if v := c.GetHeader(primary); v != "" {
		return v
	}
	return c.GetHeader(fallback)
}

// IdentityMiddleware extracts user identity and request ID from request headers
// and stores them in the gin context for use by audit logging throughout the
// request lifecycle. X-Blaxel-* headers take priority over X-Subject-* headers.
func IdentityMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := headerWithFallback(c, HeaderSubjectID, HeaderFallbackSubjectID)
		subjectType := headerWithFallback(c, HeaderSubjectType, HeaderFallbackSubjectType)
		authMethod := c.GetHeader(HeaderAuthMethod)
		requestID := c.GetHeader(HeaderRequestID)

		if requestID == "" {
			requestID = uuid.New().String()
		}

		c.Set(ContextKeyUserID, userID)
		c.Set(ContextKeySubjectType, subjectType)
		c.Set(ContextKeyAuthMethod, authMethod)
		c.Set(ContextKeyRequestID, requestID)

		c.Next()
	}
}

// Identity holds the extracted identity fields from a request context.
type Identity struct {
	UserID      string
	SubjectType string
	AuthMethod  string
	RequestID   string
}

// GetIdentity extracts the identity information stored by IdentityMiddleware.
func GetIdentity(c *gin.Context) Identity {
	return Identity{
		UserID:      getStringFromContext(c, ContextKeyUserID),
		SubjectType: getStringFromContext(c, ContextKeySubjectType),
		AuthMethod:  getStringFromContext(c, ContextKeyAuthMethod),
		RequestID:   getStringFromContext(c, ContextKeyRequestID),
	}
}

// baseFields returns the common logrus fields for all audit log entries.
func (id Identity) baseFields() logrus.Fields {
	return logrus.Fields{
		"source":    "audit",
		"subId":     id.UserID,
		"subType":   id.SubjectType,
		"authMethod": id.AuthMethod,
		"rid":       id.RequestID,
	}
}

// sanitize strips newlines and carriage returns to prevent log injection.
var newlineReplacer = strings.NewReplacer("\n", "\\n", "\r", "\\r")

// buildMessage builds a descriptive audit message that includes the action
// and identity fields. Extra fields are intentionally excluded from the
// message to avoid conflicts with QuotedMsgFormatter (which rewrites
// unquoted command= values) and to prevent log injection from user-controlled
// values. Extra fields remain available as structured log attributes.
func buildMessage(id Identity, action string) string {
	parts := []string{newlineReplacer.Replace(action)}

	if id.UserID != "" {
		parts = append(parts, fmt.Sprintf("subId=%s", newlineReplacer.Replace(id.UserID)))
	}
	if id.SubjectType != "" {
		parts = append(parts, fmt.Sprintf("subType=%s", newlineReplacer.Replace(id.SubjectType)))
	}
	if id.AuthMethod != "" {
		parts = append(parts, fmt.Sprintf("authMethod=%s", newlineReplacer.Replace(id.AuthMethod)))
	}
	if id.RequestID != "" {
		parts = append(parts, fmt.Sprintf("rid=%s", newlineReplacer.Replace(id.RequestID)))
	}

	return strings.Join(parts, " ")
}

// LogEvent emits an audit log entry for a sandbox access event.
// The action describes what happened (e.g. "terminal_connect", "process_exec").
// Extra fields are merged into the log entry for additional context.
// The log message itself contains all fields for easy reading.
func LogEvent(c *gin.Context, action string, extra logrus.Fields) {
	id := GetIdentity(c)
	fields := id.baseFields()
	fields["action"] = action
	for k, v := range extra {
		fields[k] = v
	}
	logrus.WithFields(fields).Info(buildMessage(id, action))
}

// LogEventDirect emits an audit log entry using an Identity directly,
// without requiring a gin.Context. Useful for deferred events like
// WebSocket disconnections where the original context may be done.
func LogEventDirect(id Identity, action string, extra logrus.Fields) {
	fields := id.baseFields()
	fields["action"] = action
	for k, v := range extra {
		fields[k] = v
	}
	logrus.WithFields(fields).Info(buildMessage(id, action))
}

func getStringFromContext(c *gin.Context, key string) string {
	if val, exists := c.Get(key); exists {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}
