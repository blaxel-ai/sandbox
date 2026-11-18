package handler

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"syscall"

	"github.com/gin-gonic/gin"
)

// BaseHandler provides common functionality for both MCP and API handlers
type BaseHandler struct {
	// Add any common fields here
}

// NewBaseHandler creates a new base handler
func NewBaseHandler() *BaseHandler {
	return &BaseHandler{}
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error string `json:"error" example:"Error message" binding:"required"`
	Code  string `json:"code,omitempty" example:"FILE_ALREADY_EXISTS"`
} // @name ErrorResponse

// SuccessResponse represents a success response
type SuccessResponse struct {
	Path    string `json:"path" example:"/path/to/file"`
	Message string `json:"message" example:"File created successfully" binding:"required"`
} // @name SuccessResponse

// SendError sends a standardized error response
func (h *BaseHandler) SendError(c *gin.Context, status int, err error) {
	if path, ok := diskFullPath(err); ok {
		// The gateway strips X-Blaxel-* headers from sandbox responses, so
		// callers read the code in the body; the gateway still logs it.
		c.Header("X-Blaxel-Error-Code", codeDiskFull)
		c.JSON(status, ErrorResponse{Error: diskFullMessage(err, path), Code: codeDiskFull})
		return
	}
	c.JSON(status, ErrorResponse{
		Error: err.Error(),
	})
}

// codeDiskFull marks a request that failed because the filesystem it wrote to
// had no space left. The status stays whatever the handler sent before.
const codeDiskFull = "DISK_FULL"

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
