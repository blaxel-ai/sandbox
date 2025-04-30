package api

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler/network"
)

// PortMonitorRequest is the request body for monitoring ports
type PortMonitorRequest struct {
	Callback string `json:"callback" example:"http://localhost:3000/callback"` // URL to call when a new port is detected
} // @name PortMonitorRequest

// PortsResponse is the response for the GetPorts endpoint
type PortsResponse struct {
	PID   int                `json:"pid" example:"1234"`
	Ports []network.PortInfo `json:"ports"`
} // @name PortsResponse

// MonitorResponse is the response for the port monitoring endpoints
type MonitorResponse struct {
	PID     int    `json:"pid" example:"1234"`
	Message string `json:"message" example:"Port monitoring started"`
} // @name MonitorResponse

// HandleGetPorts handles GET requests to /network/process/{pid}/ports
// @Summary Get open ports for a process
// @Description Get a list of all open ports for a process
// @Tags network
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Success 200 {object} map[string]interface{} "Object containing PID and array of network.PortInfo"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/ports [get]
func HandleGetPorts(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	net := network.GetNetwork()
	ports, err := net.GetPortsForPID(pid)

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"pid":   pid,
		"ports": ports,
	})
}

// HandleMonitorPorts handles POST requests to /network/process/{pid}/monitor
// @Summary Start monitoring ports for a process
// @Description Start monitoring for new ports opened by a process
// @Tags network
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Param request body PortMonitorRequest true "Port monitor configuration"
// @Success 200 {object} map[string]interface{} "Object containing PID and success message"
// @Failure 400 {object} ErrorResponse "Invalid request"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/monitor [post]
func HandleMonitorPorts(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	var req PortMonitorRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Register a callback to be called when a new port is detected
	// For this API, we just store that monitoring was requested, we don't actually
	// make HTTP callbacks to the client (that would be a more complex implementation)
	net := network.GetNetwork()
	net.RegisterPortOpenCallback(pid, func(pid int, port *network.PortInfo) {
		type PortCallbackRequest struct {
			PID  int `json:"pid"`
			Port int `json:"port"`
		}
		json, err := json.Marshal(PortCallbackRequest{PID: pid, Port: port.LocalPort})
		if err != nil {
			log.Printf("Error marshalling port callback request: %v", err)
			return
		}
		resp, err := http.Post(req.Callback, "application/json", bytes.NewBuffer(json))
		if err != nil {
			log.Printf("Error sending port callback request: %v", err)
			return
		}
		defer resp.Body.Close()
		log.Printf("Port callback request sent to %s", req.Callback)
	})

	c.JSON(http.StatusOK, gin.H{
		"pid":     pid,
		"message": "Port monitoring started",
	})
}

// HandleStopMonitoringPorts handles DELETE requests to /network/process/{pid}/monitor
// @Summary Stop monitoring ports for a process
// @Description Stop monitoring for new ports opened by a process
// @Tags network
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Success 200 {object} map[string]interface{} "Object containing PID and success message"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/monitor [delete]
func HandleStopMonitoringPorts(c *gin.Context) {
	pidStr := c.Param("pid")
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid PID"})
		return
	}

	net := network.GetNetwork()
	net.UnregisterPortOpenCallback(pid)

	c.JSON(http.StatusOK, gin.H{
		"pid":     pid,
		"message": "Port monitoring stopped",
	})
}
