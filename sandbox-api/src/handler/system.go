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
	upgradeCount = 0
)

func init() {
	// Load upgrade count from environment (set by previous instance before upgrade)
	if countStr := os.Getenv("SANDBOX_UPGRADE_COUNT"); countStr != "" {
		if count, err := strconv.Atoi(countStr); err == nil {
			upgradeCount = count
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
	UpgradeCount  int     `json:"upgradeCount"`
	StartedAt     string  `json:"startedAt"`
} // @name HealthResponse

// HandleHealth handles GET requests to /health
// @Summary Health check
// @Description Returns health status and system information including upgrade count and binary details
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
		UpgradeCount:  upgradeCount,
		StartedAt:     startTime.Format(time.RFC3339),
	})
}

// HandleUpgrade handles POST requests to /upgrade
// @Summary Upgrade the sandbox-api
// @Description Triggers an upgrade of the sandbox-api process. Returns 200 immediately before upgrading.
// @Description The upgrade will: download the latest binary from GitHub releases, validate it, and restart.
// @Description All running processes will be preserved across the upgrade.
// @Description Set SANDBOX_UPGRADE_VERSION environment variable to specify a version (defaults to "latest").
// @Tags system
// @Accept json
// @Produce json
// @Success 200 {object} SuccessResponse "Upgrade initiated"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /upgrade [post]
func (h *SystemHandler) HandleUpgrade(c *gin.Context) {
	// Save state before responding
	if err := h.processManager.SaveState(); err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	// Send response immediately
	h.SendJSON(c, http.StatusOK, gin.H{
		"message": "Upgrade initiated. Process state saved. The server will upgrade shortly.",
	})

	// Flush the response to ensure the client receives it
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger upgrade in background goroutine after a short delay
	// This gives time for the response to be sent
	go func() {
		time.Sleep(500 * time.Millisecond)
		process.TriggerUpgrade()
	}()
}
