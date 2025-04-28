package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler/process"
)

var processHandlerInstance *ProcessHandler

// GetProcessHandler returns the singleton process handler instance
func GetProcessHandler() *ProcessHandler {
	if processHandlerInstance == nil {
		processHandlerInstance = NewProcessHandler()
	}
	return processHandlerInstance
}

// ProcessHandler handles process operations
type ProcessHandler struct {
	*BaseHandler
	processManager *process.ProcessManager
}

// NewProcessHandler creates a new process handler
func NewProcessHandler() *ProcessHandler {
	return &ProcessHandler{
		BaseHandler:    NewBaseHandler(),
		processManager: process.NewProcessManager(),
	}
}

// ProcessRequest is the request body for executing a command
type ProcessRequest struct {
	Command           string `json:"command" binding:"required"`
	Name              string `json:"name"`
	WorkingDir        string `json:"workingDir"`
	WaitForCompletion bool   `json:"waitForCompletion"`
	Timeout           int    `json:"timeout"`
	StreamLogs        bool   `json:"streamLogs"`
	WaitForPorts      []int  `json:"waitForPorts"`
}

// ProcessResponse is the response body for a process
type ProcessResponse struct {
	PID         int    `json:"pid"`
	Name        string `json:"name,omitempty"`
	Command     string `json:"command"`
	Status      string `json:"status"`
	StartedAt   string `json:"startedAt"`
	CompletedAt string `json:"completedAt,omitempty"`
	ExitCode    int    `json:"exitCode,omitempty"`
	WorkingDir  string `json:"workingDir"`
}

// ExecuteProcess executes a process
func (h *ProcessHandler) ExecuteProcess(command string, workingDir string, name string, waitForCompletion bool, timeout int, waitForPorts []int) (ProcessResponse, error) {
	processInfo, err := h.processManager.ExecuteProcess(command, workingDir, name, waitForCompletion, timeout, waitForPorts)
	if err != nil {
		return ProcessResponse{}, err
	}

	return ProcessResponse{
		PID:        processInfo.PID,
		Name:       processInfo.Name,
		Command:    processInfo.Command,
		Status:     processInfo.Status,
		StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		WorkingDir: processInfo.WorkingDir,
	}, nil
}

// ListProcesses lists all running processes
func (h *ProcessHandler) ListProcesses() []ProcessResponse {
	processes := h.processManager.ListProcesses()
	result := make([]ProcessResponse, 0, len(processes))
	for _, p := range processes {
		result = append(result, ProcessResponse{
			PID:        p.PID,
			Name:       p.Name,
			Command:    p.Command,
			Status:     p.Status,
			StartedAt:  p.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
			WorkingDir: p.WorkingDir,
		})
	}
	return result
}

// GetProcess gets a process by PID
func (h *ProcessHandler) GetProcess(pid int) (ProcessResponse, error) {
	processInfo, exists := h.processManager.GetProcess(pid)
	if !exists {
		return ProcessResponse{}, fmt.Errorf("process not found")
	}

	return ProcessResponse{
		PID:        processInfo.PID,
		Name:       processInfo.Name,
		Command:    processInfo.Command,
		Status:     processInfo.Status,
		StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		WorkingDir: processInfo.WorkingDir,
	}, nil
}

// GetProcessByName gets a process by name
func (h *ProcessHandler) GetProcessByName(name string) (ProcessResponse, error) {
	processInfo, exists := h.processManager.GetProcessByName(name)
	if !exists {
		return ProcessResponse{}, fmt.Errorf("process not found")
	}

	return ProcessResponse{
		PID:        processInfo.PID,
		Name:       processInfo.Name,
		Command:    processInfo.Command,
		Status:     processInfo.Status,
		StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		WorkingDir: processInfo.WorkingDir,
	}, nil
}

// GetProcessOutput gets the output of a process
func (h *ProcessHandler) GetProcessOutput(pid int) (string, string, error) {
	return h.processManager.GetProcessOutput(pid)
}

// StopProcess stops a process
func (h *ProcessHandler) StopProcess(pid int) error {
	return h.processManager.StopProcess(pid)
}

// KillProcess kills a process
func (h *ProcessHandler) KillProcess(pid int) error {
	return h.processManager.KillProcess(pid)
}

// StreamProcessOutput streams the output of a process
func (h *ProcessHandler) StreamProcessOutput(pid int, writer io.Writer) error {
	return h.processManager.StreamProcessOutput(pid, writer)
}

// RemoveLogWriter removes a log writer from a process
func (h *ProcessHandler) RemoveLogWriter(pid int, writer io.Writer) {
	h.processManager.RemoveLogWriter(pid, writer)
}

// HandleListProcesses handles GET requests to /process/
func (h *ProcessHandler) HandleListProcesses(c *gin.Context) {
	processes := h.ListProcesses()
	h.SendJSON(c, http.StatusOK, processes)
}

// HandleExecuteCommand handles POST requests to /process/
func (h *ProcessHandler) HandleExecuteCommand(c *gin.Context) {
	var req ProcessRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// If a name is provided, check if a process with that name already exists
	if req.Name != "" {
		_, err := h.GetProcessByName(req.Name)
		if err == nil {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("process with name '%s' already exists", req.Name))
			return
		}
	}

	// Execute the process
	processInfo, err := h.ExecuteProcess(req.Command, req.WorkingDir, req.Name, req.WaitForCompletion, req.Timeout, req.WaitForPorts)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.SendJSON(c, http.StatusOK, processInfo)
}

// HandleGetProcessLogs handles GET requests to /process/{pid}/logs
func (h *ProcessHandler) HandleGetProcessLogs(c *gin.Context) {
	pidStr, err := h.GetPathParam(c, "pid")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid PID"))
		return
	}

	// Check if process exists
	_, err = h.GetProcess(pid)
	if err != nil {
		h.SendError(c, http.StatusNotFound, fmt.Errorf("process not found"))
		return
	}

	streamLogs := h.GetQueryParam(c, "stream", "false") == "true"
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
		err := h.StreamProcessOutput(pid, outputWriter)
		if err != nil {
			h.SendError(c, http.StatusInternalServerError, err)
			return
		}

		// Block until client disconnects
		<-clientGone

		// Clean up when client disconnects
		h.RemoveLogWriter(pid, outputWriter)

		// Send final message before closing
		c.Writer.Write([]byte("data: [CONNECTION_CLOSED]\n\n"))
		c.Writer.Flush()
		return
	}

	// Non-streaming response - return all logs at once
	stdout, stderr, err := h.GetProcessOutput(pid)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"pid":    pid,
		"stdout": stdout,
		"stderr": stderr,
	})
}

// HandleStopProcess handles DELETE requests to /process/{pid}
func (h *ProcessHandler) HandleStopProcess(c *gin.Context) {
	pidStr, err := h.GetPathParam(c, "pid")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid PID"))
		return
	}

	err = h.StopProcess(pid)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.SendSuccess(c, "Process stopped successfully")
}

// HandleKillProcess handles POST requests to /process/{pid}/kill
func (h *ProcessHandler) HandleKillProcess(c *gin.Context) {
	pidStr, err := h.GetPathParam(c, "pid")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid PID"))
		return
	}

	err = h.KillProcess(pid)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.SendSuccess(c, "Process killed successfully")
}

// HandleGetProcessByName handles GET requests to /process/name/:name
func (h *ProcessHandler) HandleGetProcessByName(c *gin.Context) {
	name := c.Param("name")
	processInfo, err := h.GetProcessByName(name)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendJSON(c, http.StatusOK, processInfo)
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
		return 0, fmt.Errorf("writer closed")
	}

	// Use recover to catch any panics that might occur during writing
	defer func() {
		if r := recover(); r != nil {
			// Log the panic but continue
		}
	}()

	// Check if the request context is still valid
	select {
	case <-w.gin.Request.Context().Done():
		w.closed = true
		return 0, fmt.Errorf("client connection closed")
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
				// Log the panic but continue
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
