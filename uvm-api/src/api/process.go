package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/network"
	"github.com/beamlit/uvm-api/src/process"
)

// ProcessRequest is the request body for executing a command
type ProcessRequest struct {
	Command           string `json:"command" binding:"required" example:"ls -la"`
	WorkingDir        string `json:"workingDir" example:"/home/user"`
	WaitForCompletion bool   `json:"waitForCompletion" example:"false"`
	Timeout           int    `json:"timeout" example:"30"`
	StreamLogs        bool   `json:"streamLogs" example:"true"`
	WaitForPorts      []int  `json:"waitForPorts" example:"3000,8080"`
}

// ProcessResponse is the response body for a process
type ProcessResponse struct {
	PID         int    `json:"pid" example:"1234"`
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
	pm := process.GetProcessManager()
	processes := pm.ListProcesses()

	response := make([]ProcessResponse, 0, len(processes))
	for _, p := range processes {
		resp := ProcessResponse{
			PID:        p.PID,
			Command:    p.Command,
			Status:     p.Status,
			StartedAt:  p.StartedAt.Format(http.TimeFormat),
			WorkingDir: p.WorkingDir,
		}

		if p.CompletedAt != nil {
			resp.CompletedAt = p.CompletedAt.Format(http.TimeFormat)
			resp.ExitCode = p.ExitCode
		}

		response = append(response, resp)
	}

	c.JSON(http.StatusOK, response)
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
func HandleExecuteCommand(c *gin.Context) {
	var req ProcessRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	pm := process.GetProcessManager()

	portCh := make(chan int)
	completionCh := make(chan int)

	// Add flags to track if channels have been closed
	portChClosed := false
	completionChClosed := false

	// Use a mutex to protect the flags
	var mu sync.Mutex

	// Defer closing the channels if they're not already closed
	defer func() {
		mu.Lock()
		defer mu.Unlock()

		if !portChClosed {
			close(portCh)
		}

		if !completionChClosed {
			close(completionCh)
		}
	}()

	// Default timeout to 30 seconds if not specified
	timeout := 30
	if req.Timeout > 0 {
		timeout = req.Timeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	// Set up streaming if requested
	if req.StreamLogs {
		// Set headers for streaming
		c.Header("Content-Type", "text/plain; charset=utf-8")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("Transfer-Encoding", "chunked")
		c.Header("X-Accel-Buffering", "no")
		c.Writer.Flush()

		// Create a channel to signal when the client disconnects
		clientGone := c.Request.Context().Done()

		// Create a buffered output writer that we'll send to the process manager
		outputWriter := &ResponseWriter{
			gin: c,
		}

		// Create a channel to notify when the HTTP connection is fully closed
		connClosed := make(chan struct{})
		defer close(connClosed)

		// Start the process with streaming setup
		pid, err := pm.StartProcess(req.Command, req.WorkingDir, func(p *process.ProcessInfo) {
			// Notify that process completed via channel
			select {
			case completionCh <- p.PID:
				// Successfully sent completion notification
			default:
				// Channel closed or blocked, can happen if HTTP connection already ended
			}

			// Check if the connection is still alive before writing
			select {
			case <-connClosed:
				// Connection is already closed, don't try to write
				return
			default:
				// Connection might still be open, try to write safely
				statusMsg := fmt.Sprintf("\n[Process completed with exit code %d]\n", p.ExitCode)
				_, _ = outputWriter.Write([]byte(statusMsg)) // Ignore errors
			}
		})

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Set up port monitoring if requested
		if len(req.WaitForPorts) > 0 {
			// Check for Mac OS and skip port monitoring if needed
			if runtime.GOOS == "darwin" {
				log.Printf("Warning: Port monitoring not fully supported on macOS, skipping waitForPorts")
				// Just close the channel without trying to monitor ports
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("Recovered from panic in portCh close: %v", r)
						}
					}()

					mu.Lock()
					if !portChClosed {
						close(portCh)
						portChClosed = true
					}
					mu.Unlock()
				}()
			} else {
				n := network.GetNetwork()
				ports := make([]int, 0, len(req.WaitForPorts))
				n.RegisterPortOpenCallback(pid, func(pid int, port *network.PortInfo) {
					if slices.Contains(req.WaitForPorts, port.LocalPort) {
						ports = append(ports, port.LocalPort)
						// Send port open notification through the stream
						portMsg := fmt.Sprintf("\n[Port %d is now open]\n", port.LocalPort)
						outputWriter.Write([]byte(fmt.Sprintf("data: %s\n", portMsg)))
					}
					if len(ports) == len(req.WaitForPorts) {
						portMsg := "\n[All requested ports are now open]\n"
						outputWriter.Write([]byte(fmt.Sprintf("data: %s\n", portMsg)))
						// Safely close the channel with defer-recover to prevent panics
						func() {
							defer func() {
								if r := recover(); r != nil {
									log.Printf("Recovered from panic in streaming port callback: %v", r)
								}
							}()

							mu.Lock()
							if !portChClosed {
								close(portCh)
								portChClosed = true
							}
							mu.Unlock()
						}()
					}
				})
			}
		}

		// Send initial process info
		processInfo, exists := pm.GetProcess(pid)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Process creation failed"})
			return
		}

		// Write the process info as the first message
		infoMsg := fmt.Sprintf("{\"pid\": %d, \"command\": \"%s\", \"status\": \"%s\"}",
			processInfo.PID, processInfo.Command, processInfo.Status)
		c.Writer.Write([]byte(fmt.Sprintf("data: %s\n\n", infoMsg)))
		c.Writer.Flush()

		// Attach the writer to the process for streaming logs
		err = pm.StreamProcessOutput(pid, outputWriter)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		// Block until process completes or client disconnects
		select {
		case <-clientGone:
			// Mark writer as closed to prevent further writes
			outputWriter.Close()
			pm.RemoveLogWriter(pid, outputWriter)

			// Try to send final message (will safely handle errors)
			_, _ = c.Writer.Write([]byte("data: [CONNECTION_CLOSED]\n\n"))
			func() {
				defer func() { recover() }() // Safely handle panics
				c.Writer.Flush()
			}()

			// No need to close the channel here as it will be closed by defer
			return
		case <-completionCh:
			// Send final message and ensure it's flushed (safely)
			_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
			func() {
				defer func() { recover() }() // Safely handle panics
				c.Writer.Flush()
			}()

			// Mark writer as closed
			outputWriter.Close()
			// No need to close the channel here as it will be closed by defer
			return
		case <-ctx.Done():
			// Mark writer as closed to prevent further writes
			outputWriter.Close()

			// Try to send timeout message safely
			_, _ = c.Writer.Write([]byte(fmt.Sprintf("data: %s\n", "\n[Process execution timed out]\n")))
			_, _ = c.Writer.Write([]byte("data: [DONE]\n\n"))
			func() {
				defer func() { recover() }() // Safely handle panics
				c.Writer.Flush()
			}()

			// No need to close the channel here as it will be closed by defer
			return
		}
	} else {
		// Regular non-streaming process execution
		pid, err := pm.StartProcess(req.Command, req.WorkingDir, func(p *process.ProcessInfo) {
			if req.WaitForCompletion {
				completionCh <- p.PID
			}
		})

		if len(req.WaitForPorts) > 0 {
			// Check for Mac OS and skip port monitoring if needed
			if runtime.GOOS == "darwin" {
				log.Printf("Warning: Port monitoring not fully supported on macOS, skipping waitForPorts")
				// Just close the channel without trying to monitor ports
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("Recovered from panic in portCh close: %v", r)
						}
					}()

					mu.Lock()
					if !portChClosed {
						close(portCh)
						portChClosed = true
					}
					mu.Unlock()
				}()
			} else {
				n := network.GetNetwork()
				ports := make([]int, 0, len(req.WaitForPorts))
				n.RegisterPortOpenCallback(pid, func(pid int, port *network.PortInfo) {
					if slices.Contains(req.WaitForPorts, port.LocalPort) {
						ports = append(ports, port.LocalPort)
					}
					if len(ports) == len(req.WaitForPorts) {
						// Safely close the channel with defer-recover to prevent panics
						func() {
							defer func() {
								if r := recover(); r != nil {
									log.Printf("Recovered from panic in port callback: %v", r)
								}
							}()

							mu.Lock()
							if !portChClosed {
								close(portCh)
								portChClosed = true
							}
							mu.Unlock()
						}()
					}
				})
			}
		}

		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		if req.WaitForCompletion {
			select {
			case pid := <-completionCh:
				_, exists := pm.GetProcess(pid)
				if !exists {
					c.JSON(http.StatusInternalServerError, gin.H{"error": "Process creation failed"})
					return
				}
				break
			case <-ctx.Done():
				c.JSON(http.StatusRequestTimeout, gin.H{"error": "Process timed out"})
				return
			}
		}

		// Get the process info
		processInfo, exists := pm.GetProcess(pid)
		if !exists {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Process creation failed"})
			return
		}

		// Create a proper response using the ProcessResponse struct
		response := ProcessResponse{
			PID:        processInfo.PID,
			Command:    processInfo.Command,
			Status:     processInfo.Status,
			StartedAt:  processInfo.StartedAt.Format(http.TimeFormat),
			WorkingDir: processInfo.WorkingDir,
		}

		if processInfo.CompletedAt != nil {
			response.CompletedAt = processInfo.CompletedAt.Format(http.TimeFormat)
			response.ExitCode = processInfo.ExitCode
		}

		c.JSON(http.StatusOK, response)
	}
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

// HandleStopProcess handles DELETE requests to /process/{pid}
// @Summary Stop a process
// @Description Gracefully stop a running process
// @Tags process
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Success 200 {object} SuccessResponse "Process stopped"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{pid} [delete]
func HandleStopProcess(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	pm := process.GetProcessManager()
	err = pm.StopProcess(pid)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pid":     pid,
		"message": "Process stopped successfully",
	})
}

// HandleKillProcess handles POST requests to /process/{pid}/kill
// @Summary Kill a process
// @Description Forcefully kill a running process
// @Tags process
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Param request body ProcessKillRequest false "Kill options"
// @Success 200 {object} SuccessResponse "Process killed"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 404 {object} ErrorResponse "Process not found"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /process/{pid}/kill [post]
func HandleKillProcess(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	pm := process.GetProcessManager()
	err = pm.KillProcess(pid)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pid":     pid,
		"message": "Process killed successfully",
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
