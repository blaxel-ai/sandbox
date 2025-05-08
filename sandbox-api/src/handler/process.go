package handler

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/sandbox-api/src/handler/process"
	"github.com/beamlit/sandbox-api/src/lib"
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
		processManager: process.GetProcessManager(),
	}
}

// ProcessRequest is the request body for executing a command
type ProcessRequest struct {
	Command           string `json:"command" binding:"required" example:"ls -la"`
	Name              string `json:"name" example:"my-process"`
	WorkingDir        string `json:"workingDir" example:"/home/user"`
	WaitForCompletion bool   `json:"waitForCompletion" example:"false"`
	Timeout           int    `json:"timeout" example:"30"`
	StreamLogs        bool   `json:"streamLogs" example:"true"`
	WaitForPorts      []int  `json:"waitForPorts" example:"3000,8080"`
} // @name ProcessRequest

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
} // @name ProcessResponse

// ProcessKillRequest is the request body for killing a process
type ProcessKillRequest struct {
	Signal string `json:"signal" example:"SIGTERM"`
} // @name ProcessKillRequest

// ExecuteProcess executes a process
func (h *ProcessHandler) ExecuteProcess(command string, workingDir string, name string, waitForCompletion bool, timeout int, waitForPorts []int) (ProcessResponse, error) {
	processInfo, err := h.processManager.ExecuteProcess(command, workingDir, name, waitForCompletion, timeout, waitForPorts)
	if err != nil {
		return ProcessResponse{}, err
	}

	completedAt := ""
	if processInfo.CompletedAt != nil {
		completedAt = processInfo.CompletedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT")
	}

	return ProcessResponse{
		PID:         processInfo.PID,
		Name:        processInfo.Name,
		Command:     processInfo.Command,
		Status:      processInfo.Status,
		StartedAt:   processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		CompletedAt: completedAt,
		ExitCode:    processInfo.ExitCode,
		WorkingDir:  processInfo.WorkingDir,
	}, nil
}

// ListProcesses lists all running processes
func (h *ProcessHandler) ListProcesses() []ProcessResponse {
	processes := h.processManager.ListProcesses()
	result := make([]ProcessResponse, 0, len(processes))
	for _, p := range processes {
		completedAt := ""
		if p.CompletedAt != nil {
			completedAt = p.CompletedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT")
		}
		result = append(result, ProcessResponse{
			PID:         p.PID,
			Name:        p.Name,
			Command:     p.Command,
			Status:      p.Status,
			StartedAt:   p.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
			CompletedAt: completedAt,
			ExitCode:    p.ExitCode,
			WorkingDir:  p.WorkingDir,
		})
	}
	return result
}

// GetProcess gets a process by identifier (PID or name)
func (h *ProcessHandler) GetProcess(identifier string) (ProcessResponse, error) {
	processInfo, exists := h.processManager.GetProcessByIdentifier(identifier)
	if !exists {
		return ProcessResponse{}, fmt.Errorf("process not found")
	}

	completedAt := ""
	if processInfo.CompletedAt != nil {
		completedAt = processInfo.CompletedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT")
	}
	return ProcessResponse{
		PID:         processInfo.PID,
		Name:        processInfo.Name,
		Command:     processInfo.Command,
		Status:      processInfo.Status,
		StartedAt:   processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
		CompletedAt: completedAt,
		ExitCode:    processInfo.ExitCode,
		WorkingDir:  processInfo.WorkingDir,
	}, nil
}

// GetProcessOutput gets the output of a process
func (h *ProcessHandler) GetProcessOutput(identifier string) (string, string, error) {
	return h.processManager.GetProcessOutput(identifier)
}

// StopProcess stops a process
func (h *ProcessHandler) StopProcess(identifier string) error {
	return h.processManager.StopProcess(identifier)
}

// KillProcess kills a process
func (h *ProcessHandler) KillProcess(identifier string) error {
	return h.processManager.KillProcess(identifier)
}

// StreamProcessOutput streams the output of a process
func (h *ProcessHandler) StreamProcessOutput(identifier string, writer io.Writer) error {
	return h.processManager.StreamProcessOutput(identifier, writer)
}

// RemoveLogWriter removes a log writer from a process
func (h *ProcessHandler) RemoveLogWriter(identifier string, writer io.Writer) {
	h.processManager.RemoveLogWriter(identifier, writer)
}

// HandleListProcesses handles GET requests to /process/
// @Summary List all processes
// @Description Get a list of all running and completed processes
// @Tags process
// @Accept json
// @Produce json
// @Success 200 {array} ProcessResponse "Process list"
// @Router /process [get]
func (h *ProcessHandler) HandleListProcesses(c *gin.Context) {
	processes := h.ListProcesses()
	h.SendJSON(c, http.StatusOK, processes)
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
func (h *ProcessHandler) HandleExecuteCommand(c *gin.Context) {
	var req ProcessRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	if req.WorkingDir != "" {
		formattedWorkingDir, err := lib.FormatPath(req.WorkingDir)
		if err != nil {
			h.SendError(c, http.StatusBadRequest, err)
			return
		}
		req.WorkingDir = formattedWorkingDir
	}

	// If a name is provided, check if a process with that name already exists
	if req.Name != "" {
		alreadyExists, err := GetProcessHandler().GetProcess(req.Name)
		if err == nil && alreadyExists.Status == "running" {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("process with name '%s' already exists and is running", req.Name)})
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

// HandleGetProcessLogs handles GET requests to /process/{identifier}/logs
// @Summary Get process logs
// @Description Get the stdout and stderr output of a process
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Param stream query bool false "Stream logs"
// @Success 200 {object} map[string]string "Process logs"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{identifier}/logs [get]
func (h *ProcessHandler) HandleGetProcessLogs(c *gin.Context) {
	identifier, err := h.GetPathParam(c, "identifier")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	stdout, stderr, err := h.GetProcessOutput(identifier)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"stdout": stdout,
		"stderr": stderr,
	})
}

// HandleGetProcessLogsStream handles GET requests to /process/{identifier}/logs/stream
// @Summary Get process logs in realtime
// @Description Get the stdout and stderr output of a process in realtime
// @Tags process
// @Accept json
// @Produce json
// @Param identifier path string true "Process identifier (PID or name)"
// @Param stream query bool false "Stream logs"
// @Success 200 {object} map[string]string "Process logs"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{identifier}/logs/stream [get]
func (h *ProcessHandler) HandleGetProcessLogsStream(c *gin.Context) {
	identifier, err := h.GetPathParam(c, "identifier")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}
	err = h.StreamProcessOutput(identifier, c.Writer)
	if err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}
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
func (h *ProcessHandler) HandleStopProcess(c *gin.Context) {
	identifier, err := h.GetPathParam(c, "identifier")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	err = h.StopProcess(identifier)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{"message": "Process stopped successfully"})
}

// HandleKillProcess handles DELETE requests to /process/{identifier}/kill
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
// @Router /process/{identifier}/kill [delete]
func (h *ProcessHandler) HandleKillProcess(c *gin.Context) {
	identifier, err := h.GetPathParam(c, "identifier")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	err = h.KillProcess(identifier)
	if err != nil {
		h.SendError(c, http.StatusNotFound, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{"message": "Process killed successfully"})
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
func (h *ProcessHandler) HandleGetProcess(c *gin.Context) {
	identifier, err := h.GetPathParam(c, "identifier")
	if err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	processInfo, err := h.GetProcess(identifier)
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
