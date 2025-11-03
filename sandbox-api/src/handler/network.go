package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/network"
)

// NetworkHandler handles network operations
type NetworkHandler struct {
	*BaseHandler
	net *network.Network
}

// NewNetworkHandler creates a new network handler
func NewNetworkHandler() *NetworkHandler {
	return &NetworkHandler{
		BaseHandler: NewBaseHandler(),
		net:         network.GetNetwork(),
	}
}

// PortMonitorRequest is the request body for monitoring ports
type PortMonitorRequest struct {
	Callback string `json:"callback" example:"http://localhost:3000/callback"` // URL to call when a new port is detected
} // @name PortMonitorRequest

// GetPortsForPID gets the ports for a process
func (h *NetworkHandler) GetPortsForPID(pid int) ([]*network.PortInfo, error) {
	return h.net.GetPortsForPID(pid)
}

// RegisterPortOpenCallback registers a callback for when a port is opened
func (h *NetworkHandler) RegisterPortOpenCallback(pid int, callback func(int, *network.PortInfo)) {
	h.net.RegisterPortOpenCallback(pid, callback)
}

// UnregisterPortOpenCallback unregisters a callback for when a port is opened
func (h *NetworkHandler) UnregisterPortOpenCallback(pid int) {
	h.net.UnregisterPortOpenCallback(pid)
}

// HandleGetPorts handles GET requests to /network/process/{pid}/ports
// @Summary Get open ports for a process
// @Description Get a list of all open ports for a process
// @Tags network
// @Accept json
// @Produce json
// @Param pid path int true "Process ID"
// @Success 200 {object} map[string]interface{} "Object containing PID and array of network.PortInfo"
// @Failure 400 {object} ErrorResponse "Invalid process ID"
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/ports [get]
func (h *NetworkHandler) HandleGetPorts(c *gin.Context) {
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

	ports, err := h.GetPortsForPID(pid)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
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
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/monitor [post]
func (h *NetworkHandler) HandleMonitorPorts(c *gin.Context) {
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

	var req PortMonitorRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	// Register a callback to be called when a new port is detected
	h.RegisterPortOpenCallback(pid, func(pid int, port *network.PortInfo) {
		type PortCallbackRequest struct {
			PID  int `json:"pid"`
			Port int `json:"port"`
		}
		json, err := json.Marshal(PortCallbackRequest{PID: pid, Port: port.LocalPort})
		if err != nil {
			logrus.Debugf("Error marshalling port callback request: %v", err)
			return
		}
		resp, err := http.Post(req.Callback, "application/json", bytes.NewBuffer(json))
		if err != nil {
			logrus.Debugf("Error sending port callback request: %v", err)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		logrus.Debugf("Port callback request sent to %s", req.Callback)
	})

	h.SendSuccess(c, "Port monitoring started")
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
// @Failure 422 {object} ErrorResponse "Unprocessable entity"
// @Failure 500 {object} ErrorResponse "Internal server error"
// @Router /network/process/{pid}/monitor [delete]
func (h *NetworkHandler) HandleStopMonitoringPorts(c *gin.Context) {
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

	h.UnregisterPortOpenCallback(pid)

	h.SendSuccess(c, "Port monitoring stopped")
}
