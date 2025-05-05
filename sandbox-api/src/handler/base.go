package handler

import (
	"fmt"
	"net/http"

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
	Error string `json:"error" example:"Error message"`
} // @name ErrorResponse

// SuccessResponse represents a success response
type SuccessResponse struct {
	Path    string `json:"path" example:"/path/to/file"`
	Message string `json:"message" example:"File created successfully"`
} // @name SuccessResponse

// SendError sends a standardized error response
func (h *BaseHandler) SendError(c *gin.Context, status int, err error) {
	c.JSON(status, ErrorResponse{
		Error: err.Error(),
	})
}

// SendSuccess sends a standardized success response
func (h *BaseHandler) SendSuccess(c *gin.Context, message string) {
	c.JSON(http.StatusOK, SuccessResponse{
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
