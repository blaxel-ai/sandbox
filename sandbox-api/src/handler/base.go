package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/lib/apierrors"
	"github.com/blaxel-ai/sandbox-api/src/lib/audit"
)

// BaseHandler provides common functionality for both MCP and API handlers
type BaseHandler struct {
	// Add any common fields here
}

// NewBaseHandler creates a new base handler
func NewBaseHandler() *BaseHandler {
	return &BaseHandler{}
}

// ErrorResponse is the body of every error: the message in error, as it has
// always been, and the platform error fields, the same as the platform's other
// errors. SendError builds it from the code's registry entry
// (src/lib/apierrors). See https://docs.blaxel.ai/troubleshooting/error-codes.
type ErrorResponse struct {
	// The error message, for clients that read only this field
	Error string `json:"error" example:"process with name 'web' already exists and is running" binding:"required"`
	// Stable error code: branch on it, not on the message
	Code string `json:"code" example:"PROCESS_NAME_IN_USE"`
	// Same as code, under the name every platform error has
	ErrorCode string `json:"error_code" example:"PROCESS_NAME_IN_USE"`
	// Same as error
	Message string `json:"message" example:"process with name 'web' already exists and is running"`
	// The one next step that works
	Action string `json:"action" example:"Use another name, or stop the running process (DELETE /process/{name}) or wait for it (GET /process/{name}/wait) before reusing its name."`
	// The tempting wrong move, when there is one
	DoNot string `json:"do_not,omitempty" example:"Do not resend unchanged while the process runs: the name stays taken."`
	// The code's entry in the error codes reference
	DocsURL string `json:"docs_url" example:"https://docs.blaxel.ai/troubleshooting/error-codes#process-name-in-use"`
	// The same request may succeed later without changes
	Retryable bool `json:"retryable" example:"false"`
	// Resending the request cannot do its work twice: retryable and not_dispatched
	SafeToRetryRequest bool `json:"safe_to_retry_request" example:"false"`
	// Whether the operation may have started: not_dispatched (nothing changed) or dispatched_unknown (it may have, for example the command ran)
	DispatchState string `json:"dispatch_state" example:"not_dispatched" enums:"not_dispatched,dispatched_unknown"`
	// The HTTP status of the response, an integer (on a stream error event, the status the same error gets as a response). The archive freeze answer (code SANDBOX_FROZEN) keeps its archive status object here instead, so the schema leaves the type open.
	Status any `json:"status"`
	// Always platform
	Origin string `json:"origin" example:"platform"`
	// When the error was answered, RFC 3339 in UTC
	Timestamp string `json:"timestamp" example:"2026-11-18T12:00:00.123456Z"`
	// The request's X-Request-Id, or the ID the sandbox generated for it
	RequestID string `json:"request_id,omitempty" example:"4bf92f35-77b3-4da6-a3ce-929d0e0e4736"`
} // @name ErrorResponse

// SuccessResponse represents a success response
type SuccessResponse struct {
	Path    string `json:"path" example:"/path/to/file"`
	Message string `json:"message" example:"File created successfully" binding:"required"`
} // @name SuccessResponse

// SendError answers err with its registry entry: the entry's status, code and
// platform error fields and headers, and err's message. An error that ran out
// of disk space is answered as DISK_FULL, keeping the entry's status.
//
// The platform gateway strips the X-Blaxel-* headers from sandbox responses,
// so callers read the code in the body; the headers are set for the gateway's
// logs and for callers inside the sandbox.
func (h *BaseHandler) SendError(c *gin.Context, entry apierrors.Entry, err error) {
	sent, message := resolveError(entry, err)
	writeError(c, entry.Status, sent, message)
}

// WriteError answers with entry and message, for errors that are not a Go
// error value.
func WriteError(c *gin.Context, entry apierrors.Entry, message string) {
	writeError(c, entry.Status, entry, message)
}

func writeError(c *gin.Context, status int, entry apierrors.Entry, message string) {
	entry.SetHeaders(c.Header)
	c.JSON(status, entry.Body(status, message, errorMeta(c)))
}

// ErrorBody is the body WriteError sends, for writers that add to it.
func ErrorBody(c *gin.Context, entry apierrors.Entry, message string) map[string]any {
	return entry.Body(entry.Status, message, errorMeta(c))
}

// streamErrorEvent is the error event of an NDJSON stream: type error, the
// message in data (as before) and the platform error fields. Headers are
// already sent, so status is the status the same error gets as a response.
func streamErrorEvent(c *gin.Context, entry apierrors.Entry, err error) map[string]any {
	sent, message := resolveError(entry, err)
	event := sent.Fields(entry.Status, message, errorMeta(c))
	event["type"] = "error"
	event["data"] = message
	return event
}

// resolveError is the entry err is answered with and its message: entry,
// unless err ran out of disk space.
func resolveError(entry apierrors.Entry, err error) (apierrors.Entry, string) {
	if path, ok := diskFullPath(err); ok {
		return apierrors.DiskFull, diskFullMessage(err, path)
	}
	return entry, err.Error()
}

func errorMeta(c *gin.Context) apierrors.Meta {
	return apierrors.Meta{RequestID: c.GetString(audit.ContextKeyRequestID), Now: time.Now()}
}

// diskFullPath reports whether err is a filesystem operation that failed for
// lack of space, and the path it failed on. ENOSPC alone is not enough: inotify
// answers ENOSPC when its watch limit is reached, which is not a full disk.
func diskFullPath(err error) (string, bool) {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) && errors.Is(pathErr.Err, syscall.ENOSPC) {
		return pathErr.Path, true
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) && errors.Is(linkErr.Err, syscall.ENOSPC) {
		return linkErr.New, true
	}
	return "", false
}

// diskFullMessage keeps the original error, then says what is full, how big it
// is when that can be read, and what to do.
func diskFullMessage(err error, path string) string {
	size := ""
	var st syscall.Statfs_t
	// The path may not exist (a write that could not create it): its
	// directory is on the same filesystem.
	if syscall.Statfs(path, &st) == nil || syscall.Statfs(filepath.Dir(path), &st) == nil {
		size = fmt.Sprintf(" (%s in all)", humanBytes(uint64(st.Blocks)*uint64(st.Bsize)))
	}
	return fmt.Sprintf("%s. The sandbox's disk is full: no space is left on the filesystem that holds %s%s. Free space by deleting files you no longer need (DELETE /filesystem/{path}, or rm in a process), then resend the request. Don't resend it unchanged: it fails the same way until space is freed.",
		err, path, size)
}

// humanBytes formats n as a size in decimal units, as df -H does.
func humanBytes(n uint64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

// SendSuccess sends a standardized success response
func (h *BaseHandler) SendSuccess(c *gin.Context, message string) {
	c.JSON(http.StatusOK, SuccessResponse{
		Message: message,
	})
}

func (h *BaseHandler) SendSuccessWithPath(c *gin.Context, path string, message string) {
	c.JSON(http.StatusOK, SuccessResponse{
		Path:    path,
		Message: message,
	})
}

// SendJSON sends a JSON response with the given status code
func (h *BaseHandler) SendJSON(c *gin.Context, status int, data interface{}) {
	c.JSON(status, data)
}

// GetPathParam gets a path parameter and returns an error if it's invalid
func (h *BaseHandler) GetPathParam(c *gin.Context, param string) (string, error) {
	value := c.Param(param)
	if value == "" {
		return "", fmt.Errorf("missing required path parameter: %s", param)
	}
	return value, nil
}

// GetQueryParam gets a query parameter with a default value
func (h *BaseHandler) GetQueryParam(c *gin.Context, param string, defaultValue string) string {
	value := c.Query(param)
	if value == "" {
		return defaultValue
	}
	return value
}

// BindJSON binds the request body to a struct and returns an error if it fails
func (h *BaseHandler) BindJSON(c *gin.Context, obj interface{}) error {
	if err := c.ShouldBindJSON(obj); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

const sandboxAPIReference = "/swagger/doc.json"

type WelcomeResponse struct {
	Message       string `json:"message" example:"Welcome to your Blaxel Sandbox. Discover your capabilities here: /swagger/doc.json"`
	APIReference  string `json:"apiReference" example:"/swagger/doc.json"`
	Documentation string `json:"documentation" example:"https://docs.blaxel.ai/Sandboxes/Overview"`
	Description   string `json:"description" example:"This sandbox provides a full-featured environment for running code securely"`
}

func (h *BaseHandler) HandleWelcome(c *gin.Context) {
	c.JSON(http.StatusOK, WelcomeResponse{
		Message:       "Welcome to your Blaxel Sandbox. Discover your capabilities here: " + sandboxAPIReference,
		APIReference:  sandboxAPIReference,
		Documentation: "https://docs.blaxel.ai/Sandboxes/Overview",
		Description:   "This sandbox provides a full-featured environment for running code securely. Visit the documentation to learn how to manage processes, access the filesystem, and more.",
	})
}
