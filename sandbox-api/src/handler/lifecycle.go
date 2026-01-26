package handler

import (
	"net/http"
	"sync"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/constants"
	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// LifecycleHandler handles sandbox lifecycle operations (stop/status)
type LifecycleHandler struct {
	BaseHandler
	mu              sync.Mutex
	scheduledStopAt *time.Time
	stopTimer       *time.Timer
	stopProcessPIDs []string // PIDs to affect when scheduled stop fires
	processHandler  *ProcessHandler
}

// NewLifecycleHandler creates a new LifecycleHandler
func NewLifecycleHandler(processHandler *ProcessHandler) *LifecycleHandler {
	return &LifecycleHandler{
		processHandler: processHandler,
	}
}

// Lock acquires the mutex lock
func (h *LifecycleHandler) Lock() {
	h.mu.Lock()
}

// Unlock releases the mutex lock
func (h *LifecycleHandler) Unlock() {
	h.mu.Unlock()
}

// KeepAliveProcessInfo contains info about a process with keepAlive enabled
type KeepAliveProcessInfo struct {
	PID       string     `json:"pid" example:"1234"`
	Name      string     `json:"name" example:"my-process"`
	Command   string     `json:"command" example:"npm start"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
	Timeout   int        `json:"timeout" example:"600"`
} // @name KeepAliveProcessInfo

// StopRequest is the request body for stopping the sandbox
type StopRequest struct {
	Timeout int `json:"timeout" example:"0"`
} // @name StopRequest

// StatusResponse is the response body for sandbox status
type StatusResponse struct {
	State              string                 `json:"state" example:"awake"`
	ScheduledStopAt    *time.Time             `json:"scheduledStopAt,omitempty"`
	KeepAliveProcesses []KeepAliveProcessInfo `json:"keepAliveProcesses"`
} // @name StatusResponse

const (
	StateAwake = "awake" // At least one keepAlive process exists
	StateAuto  = "auto"  // No keepAlive processes, auto-hibernation enabled
)

// GetState returns the current state based on keepAlive processes
func (h *LifecycleHandler) GetState() string {
	keepAliveProcesses := h.GetKeepAliveProcesses()
	if len(keepAliveProcesses) > 0 {
		return StateAwake
	}
	return StateAuto
}

// GetScheduledStopAt returns the scheduled stop time
func (h *LifecycleHandler) GetScheduledStopAt() *time.Time {
	return h.scheduledStopAt
}

// GetKeepAliveProcesses returns processes with keepAlive enabled
func (h *LifecycleHandler) GetKeepAliveProcesses() []KeepAliveProcessInfo {
	if h.processHandler == nil {
		return []KeepAliveProcessInfo{}
	}

	processes := h.processHandler.ListProcesses()
	keepAliveProcesses := make([]KeepAliveProcessInfo, 0)

	for _, p := range processes {
		if p.KeepAlive && p.Status == string(constants.ProcessStatusRunning) {
			var startedAt *time.Time
			if p.StartedAt != "" {
				if t, err := time.Parse("Mon, 02 Jan 2006 15:04:05 GMT", p.StartedAt); err == nil {
					startedAt = &t
				}
			}
			keepAliveProcesses = append(keepAliveProcesses, KeepAliveProcessInfo{
				PID:       p.PID,
				Name:      p.Name,
				Command:   p.Command,
				StartedAt: startedAt,
				Timeout:   0, // We don't expose timeout per process in the list
			})
		}
	}

	return keepAliveProcesses
}

// getKeepAliveProcessPIDs returns PIDs of processes with keepAlive enabled
func (h *LifecycleHandler) getKeepAliveProcessPIDs() []string {
	keepAliveProcesses := h.GetKeepAliveProcesses()
	pids := make([]string, len(keepAliveProcesses))
	for i, p := range keepAliveProcesses {
		pids[i] = p.PID
	}
	return pids
}

// executeStop removes keepAlive from the specified processes
func (h *LifecycleHandler) executeStop(pids []string) {
	if h.processHandler == nil {
		return
	}

	for _, pid := range pids {
		if err := h.processHandler.RemoveKeepAlive(pid); err != nil {
			logrus.Warnf("[Lifecycle] Failed to remove keepAlive from process %s: %v", pid, err)
		} else {
			logrus.Infof("[Lifecycle] Removed keepAlive from process %s", pid)
		}
	}

	// Clear scheduled stop
	h.scheduledStopAt = nil
	h.stopProcessPIDs = nil
}

// cancelScheduledStop cancels any pending scheduled stop
func (h *LifecycleHandler) cancelScheduledStop() {
	if h.stopTimer != nil {
		h.stopTimer.Stop()
		h.stopTimer = nil
	}
	h.scheduledStopAt = nil
	h.stopProcessPIDs = nil
}

// executeStopWithTimeoutLocked executes or schedules a stop
// This method assumes the caller has ALREADY acquired the mutex
func (h *LifecycleHandler) executeStopWithTimeoutLocked(timeout int) {
	// Cancel any existing scheduled stop
	h.cancelScheduledStop()

	// Get current keepAlive process PIDs
	pids := h.getKeepAliveProcessPIDs()

	if len(pids) == 0 {
		logrus.Debugf("[Lifecycle] No keepAlive processes to stop")
		return
	}

	if timeout <= 0 {
		// Immediate stop
		logrus.Infof("[Lifecycle] Executing immediate stop for %d processes", len(pids))
		h.executeStop(pids)
	} else {
		// Scheduled stop
		h.stopProcessPIDs = pids
		stopAt := time.Now().Add(time.Duration(timeout) * time.Second)
		h.scheduledStopAt = &stopAt

		logrus.Infof("[Lifecycle] Scheduling stop in %d seconds for %d processes", timeout, len(pids))

		h.stopTimer = time.AfterFunc(time.Duration(timeout)*time.Second, func() {
			h.mu.Lock()
			defer h.mu.Unlock()

			if h.stopProcessPIDs != nil {
				logrus.Infof("[Lifecycle] Executing scheduled stop for %d processes", len(h.stopProcessPIDs))
				h.executeStop(h.stopProcessPIDs)
			}
		})
	}
}

// ExecuteStopWithTimeout executes or schedules a stop (called by MCP)
// This method acquires the mutex internally
func (h *LifecycleHandler) ExecuteStopWithTimeout(timeout int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.executeStopWithTimeoutLocked(timeout)
}

// HandleStop handles POST /stop
// @Summary Force stop sandbox (remove keepAlive from processes)
// @Description Force stop removes keepAlive from all current keepAlive processes, enabling auto-hibernation. Optionally schedule the stop with a timeout.
// @Tags lifecycle
// @Accept json
// @Produce json
// @Param request body StopRequest false "Stop request with optional timeout"
// @Success 200 {object} StatusResponse
// @Failure 500 {object} ErrorResponse
// @Router /stop [post]
func (h *LifecycleHandler) HandleStop(c *gin.Context) {
	var req StopRequest

	// Parse request body (optional)
	if err := c.ShouldBindJSON(&req); err != nil {
		// If no body or invalid JSON, use defaults (immediate stop)
		req.Timeout = 0
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	h.executeStopWithTimeoutLocked(req.Timeout)

	c.JSON(http.StatusOK, StatusResponse{
		State:              h.GetState(),
		ScheduledStopAt:    h.scheduledStopAt,
		KeepAliveProcesses: h.GetKeepAliveProcesses(),
	})
}

// HandleStatus handles GET /status
// @Summary Get sandbox status
// @Description Returns the current sandbox status, scheduled stop time, and active keepAlive processes.
// @Tags lifecycle
// @Produce json
// @Success 200 {object} StatusResponse
// @Router /status [get]
func (h *LifecycleHandler) HandleStatus(c *gin.Context) {
	h.mu.Lock()
	defer h.mu.Unlock()

	c.JSON(http.StatusOK, StatusResponse{
		State:              h.GetState(),
		ScheduledStopAt:    h.scheduledStopAt,
		KeepAliveProcesses: h.GetKeepAliveProcesses(),
	})
}
