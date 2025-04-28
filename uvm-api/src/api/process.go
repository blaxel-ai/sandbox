package api

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler"
)

// ProcessRequest is the request body for executing a command
type ProcessRequest struct {
	Command           string `json:"command" binding:"required" example:"ls -la"`
	Name              string `json:"name" example:"my-process"`
	WorkingDir        string `json:"workingDir" example:"/home/user"`
	WaitForCompletion bool   `json:"waitForCompletion" example:"false"`
	Timeout           int    `json:"timeout" example:"30"`
	StreamLogs        bool   `json:"streamLogs" example:"true"`
	WaitForPorts      []int  `json:"waitForPorts" example:"3000,8080"`
}

// ProcessResponse is the response body for a process
type ProcessResponse struct {
	PID         string `json:"pid" example:"1234"`
	Name        string `json:"name,omitempty" example:"my-process"`
	Command     string `json:"command" example:"ls -la"`
	Status      string `json:"status" example:"running"`
	StartedAt   string `json:"startedAt" example:"Wed, 01 Jan 2023 12:00:00 GMT"`
	CompletedAt string `json:"completedAt,omitempty" example:"Wed, 01 Jan 2023 12:01:00 GMT"`
	ExitCode    int    `json:"exitCode,omitempty" example:"0"`
	WorkingDir  string `json:"workingDir" example:"/home/user"`
}

// ProcessKillRequest is the request body for killing a process
type ProcessKillRequest struct {
	Signal string `json:"signal" example:"SIGTERM"`
}

// HandleListProcesses handles GET requests to /process/
// @Summary List all processes
// @Description Get a list of all running and completed processes
// @Tags process
// @Accept json
// @Produce json
// @Success 200 {array} ProcessResponse "Process list"
// @Router /process [get]
func HandleListProcesses(c *gin.Context) {
	processes := handler.GetProcessHandler().ListProcesses()
	c.JSON(http.StatusOK, processes)
}

// HandleExecuteCommand handles POST requests to /process/
// @Summary Execute a command
// @Description Execute a command and return process information
// @Tags process
// @Accept json
// @Produce json
// @Param request body ProcessRequest true "Process execution request"
// @Success 200 {object} ProcessResponse "Process information"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process [post]
func HandleStartProcess(c *gin.Context) {
	var req struct {
		Command           string `json:"command" binding:"required"`
		WorkingDir        string `json:"workingDir"`
		Name              string `json:"name"`
		WaitForCompletion bool   `json:"waitForCompletion"`
		Timeout           int    `json:"timeout"`
		WaitForPorts      []int  `json:"waitForPorts"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If a name is provided, check if a process with that name already exists
	if req.Name != "" {
		_, err := handler.GetProcessHandler().GetProcess(req.Name)
		if err == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("process with name '%s' already exists", req.Name)})
			return
		}
	}

	// Execute the process
	processInfo, err := handler.GetProcessHandler().ExecuteProcess(req.Command, req.WorkingDir, req.Name, req.WaitForCompletion, req.Timeout, req.WaitForPorts)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, processInfo)
}

// HandleGetProcess handles GET requests to /process/:identifier
// @Summary Get process by identifier
// @Description Get information about a process by its PID or name
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Success 200 {object} ProcessResponse "Process information"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Router /process/{identifier} [get]
func HandleGetProcess(c *gin.Context) {
	identifier := c.Param("identifier")
	processInfo, err := handler.GetProcessHandler().GetProcess(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, processInfo)
}

// HandleStopProcess handles DELETE requests to /process/{identifier}
// @Summary Stop a process
// @Description Gracefully stop a running process
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Success 200 {object} SuccessResponse "Process stopped"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{identifier} [delete]
func HandleStopProcess(c *gin.Context) {
	identifier := c.Param("identifier")
	err := handler.GetProcessHandler().StopProcess(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Process stopped successfully"})
}

// HandleKillProcess handles POST requests to /process/{identifier}/kill
// @Summary Kill a process
// @Description Forcefully kill a running process
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Param request body ProcessKillRequest false "Kill options"
// @Success 200 {object} SuccessResponse "Process killed"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{identifier}/kill [post]
func HandleKillProcess(c *gin.Context) {
	identifier := c.Param("identifier")
	err := handler.GetProcessHandler().KillProcess(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Process killed successfully"})
}

// HandleGetProcessLogs handles GET requests to /process/{identifier}/logs
// @Summary Get process logs
// @Description Get the stdout and stderr output of a process
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Success 200 {object} map[string]string "Process logs"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{identifier}/logs [get]
func HandleGetProcessLogs(c *gin.Context) {
	identifier := c.Param("identifier")
	streamLogs := c.Query("stream") == "true"

	if streamLogs {
		// Set headers for streaming
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Transfer-Encoding", "chunked")
		c.Header("X-Accel-Buffering", "no")
		c.Writer.Flush()

		// Create a channel to signal when the client disconnects
		clientGone := c.Request.Context().Done()

		// Create a ResponseWriter wrapper
		outputWriter := &ResponseWriter{
			gin: c,
		}

		// Send initial message
		c.Writer.Write([]byte(fmt.Sprintf("data: {\"identifier\": %s, \"streaming\": true}\n\n", identifier)))
		c.Writer.Flush()

		// Attach the writer to the process
		err := handler.GetProcessHandler().StreamProcessOutput(identifier, outputWriter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Block until client disconnects
		<-clientGone

		// Clean up when client disconnects
		handler.GetProcessHandler().RemoveLogWriter(identifier, outputWriter)

		// Send final message before closing
		c.Writer.Write([]byte("data: [CONNECTION_CLOSED]\n\n"))
		c.Writer.Flush()
		return
	}

	// Non-streaming response - return all logs at once
	stdout, stderr, err := handler.GetProcessHandler().GetProcessOutput(identifier)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stdout": stdout,
		"stderr": stderr,
	})
}

// ResponseWriter is a custom writer for SSE responses that also flushes after each write
type ResponseWriter struct {
	buffer bytes.Buffer
	gin    *gin.Context
	closed bool
	mu     sync.Mutex // Protects the closed field
}

// Write writes data to the buffer and flushes to the client in a safe manner
func (w *ResponseWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Don't attempt to write if the writer is marked as closed
	if w.closed {
		return 0, errors.New("writer closed")
	}

	// Use recover to catch any panics that might occur during writing
	defer func() {
		if r := recover(); r != nil {
			log.Printf("Recovered from panic in ResponseWriter.Write: %v", r)
			w.closed = true
		}
	}()

	// Check if the request context is still valid
	select {
	case <-w.gin.Request.Context().Done():
		w.closed = true
		return 0, errors.New("client connection closed")
	default:
		// Context still valid, proceed with write
	}

	prefix := []byte("data: ")
	w.buffer.Write(prefix)
	w.buffer.Write(data)
	w.buffer.Write([]byte("\n\n"))
	content := w.buffer.Bytes()
	w.buffer.Reset()

	// Safely write and flush
	n, err := w.gin.Writer.Write(content)
	if err != nil {
		w.closed = true
		return 0, err
	}

	// Flush safely with panic recovery
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("Recovered from panic in ResponseWriter flush: %v", r)
				w.closed = true
			}
		}()
		w.gin.Writer.Flush()
	}()

	return n, nil
}

// Close marks the writer as closed to prevent further writes
func (w *ResponseWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
}
