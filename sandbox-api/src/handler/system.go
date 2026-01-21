package handler

import (
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

// Build information - set via ldflags at build time
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildTime = "unknown"
)

// Runtime information
var (
	startTime    = time.Now()
	restartCount = 0
)

func init() {
	// Load restart count from environment (set by previous instance before restart)
	if countStr := os.Getenv("SANDBOX_RESTART_COUNT"); countStr != "" {
		if count, err := strconv.Atoi(countStr); err == nil {
			restartCount = count
		}
	}
}

// SystemHandler handles system-level operations
type SystemHandler struct {
	*BaseHandler
	processManager *process.ProcessManager
}

// NewSystemHandler creates a new system handler
func NewSystemHandler() *SystemHandler {
	return &SystemHandler{
		BaseHandler:    NewBaseHandler(),
		processManager: process.GetProcessManager(),
	}
}

// HealthResponse is the response body for the health endpoint
type HealthResponse struct {
	Status        string  `json:"status"`
	Version       string  `json:"version"`
	GitCommit     string  `json:"gitCommit"`
	BuildTime     string  `json:"buildTime"`
	GoVersion     string  `json:"goVersion"`
	OS            string  `json:"os"`
	Arch          string  `json:"arch"`
	Uptime        string  `json:"uptime"`
	UptimeSeconds float64 `json:"uptimeSeconds"`
	RestartCount  int     `json:"restartCount"`
	StartedAt     string  `json:"startedAt"`
} // @name HealthResponse

// HandleHealth handles GET requests to /health
// @Summary Health check
// @Description Returns health status and system information including restart count and binary details
// @Tags system
// @Produce json
// @Success 200 {object} HealthResponse "Health status"
// @Router /health [get]
func (h *SystemHandler) HandleHealth(c *gin.Context) {
	uptime := time.Since(startTime)

	h.SendJSON(c, http.StatusOK, HealthResponse{
		Status:        "ok",
		Version:       Version,
		GitCommit:     GitCommit,
		BuildTime:     BuildTime,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		Uptime:        uptime.Round(time.Second).String(),
		UptimeSeconds: uptime.Seconds(),
		RestartCount:  restartCount,
		StartedAt:     startTime.Format(time.RFC3339),
	})
}

// HandleRestart handles POST requests to /restart
// @Summary Restart the sandbox-api
// @Description Triggers a restart of the sandbox-api process. Returns 200 immediately before restarting.
// @Description The restart will: save process state, rebuild from source (if configured), and restart.
// @Description All running processes will be preserved across the restart.
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} SuccessResponse "Restart initiated"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /restart [post]
func (h *SystemHandler) HandleRestart(c *gin.Context) {
	// Save state before responding
	if err := h.processManager.SaveState(); err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	// Send response immediately
	h.SendJSON(c, http.StatusOK, gin.H{
		"message": "Restart initiated. Process state saved. The server will restart shortly.",
	})

	// Flush the response to ensure the client receives it
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger restart in background goroutine after a short delay
	// This gives time for the response to be sent
	go func() {
		time.Sleep(500 * time.Millisecond)
		process.TriggerRestart()
	}()
}
