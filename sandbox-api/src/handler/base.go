package handler

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// jsonContentType is the Content-Type header value for JSON responses.
// Pre-allocated as a slice to allow direct header map assignment without allocation.
var jsonContentType = []string{"application/json; charset=utf-8"}

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
} // @name ErrorResponse

// SuccessResponse represents a success response
type SuccessResponse struct {
	Path    string `json:"path" example:"/path/to/file"`
	Message string `json:"message" example:"File created successfully" binding:"required"`
} // @name SuccessResponse

// writeJSONResponse serializes data using jsoniter and writes it directly to the
// response writer, bypassing Gin's c.JSON() which uses the slower encoding/json.
// The package-level `json` variable (defined in filesystem.go) is
// jsoniter.ConfigCompatibleWithStandardLibrary.
func writeJSONResponse(c *gin.Context, status int, data interface{}) {
	buf, err := json.Marshal(data)
	if err != nil {
		c.Status(http.StatusInternalServerError)
		return
	}
	c.Status(status)
	c.Writer.Header()["Content-Type"] = jsonContentType
	_, _ = c.Writer.Write(buf)
}

// SendError sends a standardized error response
func (h *BaseHandler) SendError(c *gin.Context, status int, err error) {
	writeJSONResponse(c, status, ErrorResponse{
		Error: err.Error(),
	})
}

// SendSuccess sends a standardized success response
func (h *BaseHandler) SendSuccess(c *gin.Context, message string) {
	writeJSONResponse(c, http.StatusOK, SuccessResponse{
		Message: message,
	})
}

func (h *BaseHandler) SendSuccessWithPath(c *gin.Context, path string, message string) {
	writeJSONResponse(c, http.StatusOK, SuccessResponse{
		Path:    path,
		Message: message,
	})
}

// SendJSON sends a JSON response with the given status code
func (h *BaseHandler) SendJSON(c *gin.Context, status int, data interface{}) {
	writeJSONResponse(c, status, data)
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

// BindJSON reads the request body and deserializes it using jsoniter,
// bypassing Gin's ShouldBindJSON which uses the slower encoding/json.
func (h *BaseHandler) BindJSON(c *gin.Context, obj interface{}) error {
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(obj); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

type WelcomeResponse struct {
	Message       string `json:"message" example:"Welcome to your Blaxel Sandbox"`
	Documentation string `json:"documentation" example:"https://docs.blaxel.ai/Sandboxes/Overview"`
	Description   string `json:"description" example:"This sandbox provides a full-featured environment for running code securely"`
}

func (h *BaseHandler) HandleWelcome(c *gin.Context) {
	writeJSONResponse(c, http.StatusOK, WelcomeResponse{
		Message:       "Welcome to your Blaxel Sandbox",
		Documentation: "https://docs.blaxel.ai/Sandboxes/Overview",
		Description:   "This sandbox provides a full-featured environment for running code securely. Visit the documentation to learn how to manage processes, access the filesystem, and more.",
	})
}
