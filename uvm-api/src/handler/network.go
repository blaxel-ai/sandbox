package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/beamlit/uvm-api/src/handler/network"
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
	Callback string `json:"callback"` // URL to call when a new port is detected
}

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
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	h.SendJSON(c, http.StatusOK, gin.H{
		"pid":   pid,
		"ports": ports,
	})
}

// HandleMonitorPorts handles POST requests to /network/process/{pid}/monitor
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

	h.SendSuccess(c, "Port monitoring started")
}

// HandleStopMonitoringPorts handles DELETE requests to /network/process/{pid}/monitor
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
