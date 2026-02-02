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
	Status        string                `json:"status" binding:"required" example:"ok"`
	Version       string                `json:"version" binding:"required" example:"v0.1.0"`
	GitCommit     string                `json:"gitCommit" binding:"required" example:"abc123"`
	BuildTime     string                `json:"buildTime" binding:"required" example:"2026-01-29T17:36:52Z"`
	GoVersion     string                `json:"goVersion" binding:"required" example:"go1.25.0"`
	OS            string                `json:"os" binding:"required" example:"linux"`
	Arch          string                `json:"arch" binding:"required" example:"amd64"`
	Uptime        string                `json:"uptime" binding:"required" example:"1h30m"`
	UptimeSeconds float64               `json:"uptimeSeconds" binding:"required" example:"5400.5"`
	UpgradeCount  int                   `json:"upgradeCount" binding:"required" example:"0"`
	StartedAt     string                `json:"startedAt" binding:"required" example:"2026-01-29T18:45:49Z"`
	LastUpgrade   process.UpgradeStatus `json:"lastUpgrade" binding:"required"`
} // @name HealthResponse

// HandleHealth handles GET requests to /health
// @Summary Health check
// @Description Returns health status and system information including upgrade count and binary details
// @Description Also includes last upgrade attempt status with detailed error information if available
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
		LastUpgrade:   process.GetLastUpgradeStatus(),
	})
}

// UpgradeRequest represents the request body for the upgrade endpoint
type UpgradeRequest struct {
	Version string `json:"version" example:"develop"`                                        // Version to upgrade to: "develop", "main", "latest", or specific tag like "v1.0.0"
	BaseURL string `json:"baseUrl" example:"https://github.com/blaxel-ai/sandbox/releases"` // Base URL for releases (useful for forks)
} // @name UpgradeRequest

// HandleUpgrade handles POST requests to /upgrade
// @Summary Upgrade the sandbox-api
// @Description Triggers an upgrade of the sandbox-api process. Returns 200 immediately before upgrading.
// @Description The upgrade will: download the specified binary from GitHub releases, validate it, and restart.
// @Description All running processes will be preserved across the upgrade.
// @Description Available versions: "develop" (default), "main", "latest", or specific tag like "v1.0.0"
// @Description You can also specify a custom baseUrl for forks (defaults to https://github.com/blaxel-ai/sandbox/releases)
// @Tags system
// @Accept json
// @Produce json
// @Param request body UpgradeRequest false "Upgrade options"
// @Success 200 {object} SuccessResponse "Upgrade initiated"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /upgrade [post]
func (h *SystemHandler) HandleUpgrade(c *gin.Context) {
	// Parse request body (optional)
	var req UpgradeRequest
	c.ShouldBindJSON(&req) // Ignore errors - empty body is valid

	// Default to "develop" if no version specified
	version := req.Version
	if version == "" {
		version = "develop"
	}

	// Default base URL
	baseURL := req.BaseURL
	if baseURL == "" {
		baseURL = "https://github.com/blaxel-ai/sandbox/releases"
	}

	// Save state before responding
	if err := h.processManager.SaveState(); err != nil {
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	// Send response immediately
	h.SendJSON(c, http.StatusOK, gin.H{
		"message": "Upgrade initiated. Process state saved. The server will upgrade shortly.",
		"version": version,
		"baseUrl": baseURL,
	})

	// Flush the response to ensure the client receives it
	if flusher, ok := c.Writer.(http.Flusher); ok {
		flusher.Flush()
	}

	// Trigger upgrade in background goroutine after a short delay
	// This gives time for the response to be sent
	go func() {
		time.Sleep(500 * time.Millisecond)
		process.TriggerUpgrade(version, baseURL)
	}()
}
