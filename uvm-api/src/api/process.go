package api

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler"
	"github.com/beamlit/uvm-api/src/handler/process"
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
	PID         int    `json:"pid" example:"1234"`
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

// HandleStartProcess handles POST requests to /process
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
		_, err := handler.GetProcessHandler().GetProcessByName(req.Name)
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

// HandleGetProcess handles GET requests to /process/:pid
func HandleGetProcess(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	processInfo, err := handler.GetProcessHandler().GetProcess(pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, processInfo)
}

// HandleGetProcessByName handles GET requests to /process/name/:name
// @Summary Get process by name
// @Description Get information about a process by its name
// @Tags process
// @Accept json
// @Produce json
// @Param name path string true "Process name"
// @Success 200 {object} ProcessResponse "Process information"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Router /process/name/{name} [get]
func HandleGetProcessByName(c *gin.Context) {
	name := c.Param("name")
	processInfo, err := handler.GetProcessHandler().GetProcessByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, processInfo)
}

// HandleGetProcessOutput handles GET requests to /process/:pid/output
func HandleGetProcessOutput(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	stdout, stderr, err := handler.GetProcessHandler().GetProcessOutput(pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"stdout": stdout,
		"stderr": stderr,
	})
}

// HandleStopProcess handles POST requests to /process/:pid/stop
func HandleStopProcess(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	err = handler.GetProcessHandler().StopProcess(pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Process stopped successfully"})
}

// HandleKillProcess handles POST requests to /process/:pid/kill
func HandleKillProcess(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	err = handler.GetProcessHandler().KillProcess(pid)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Process killed successfully"})
}

// HandleGetProcessLogs handles GET requests to /process/{pid}/logs
// @Summary Get process logs
// @Description Get the stdout and stderr output of a process
// @Tags process
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Success 200 {object} map[string]string "Process logs"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{pid}/logs [get]
func HandleGetProcessLogs(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	pm := process.GetProcessManager()
	_, exists := pm.GetProcess(pid)
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": "Process not found"})
		return
	}

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
		c.Writer.Write([]byte(fmt.Sprintf("data: {\"pid\": %d, \"streaming\": true}\n\n", pid)))
		c.Writer.Flush()

		// Attach the writer to the process
		err := pm.StreamProcessOutput(pid, outputWriter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Block until client disconnects
		<-clientGone

		// Clean up when client disconnects
		pm.RemoveLogWriter(pid, outputWriter)

		// Send final message before closing
		c.Writer.Write([]byte("data: [CONNECTION_CLOSED]\n\n"))
		c.Writer.Flush()
		return
	} else {
		// Non-streaming response - return all logs at once
		stdout, stderr, err := pm.GetProcessOutput(pid)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"pid":    pid,
			"stdout": stdout,
			"stderr": stderr,
		})
	}
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
