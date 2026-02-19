package handler

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/network"
	networking "github.com/blaxel-ai/sandbox-api/src/lib/networking"
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

// TunnelConfigRequest is the request body for updating the network tunnel configuration.
// The config field is a base64-encoded JSON matching the tunnel config schema.
//
// To generate the base64 config:
//
//	echo -n '{"local_ip":"10.0.0.1/32","peer_endpoint":"1.2.3.4:51820","peer_public_key":"<base64-key>","private_key":"<base64-key>"}' | base64
//
// Optional fields: mtu (default 1420), listen_port (default 51820), interface_name (default "wg0"),
// allowed_ips (default ["0.0.0.0/0"]), persistent_keepalive (default 25, 0 to disable), route_all (default false).
type TunnelConfigRequest struct {
	Config string `json:"config" binding:"required" example:"eyJsb2NhbF9pcCI6ICIxMC4wLjAuMS8zMiIsIC4uLn0="` // Base64-encoded tunnel config JSON
} // @name TunnelConfigRequest

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

// HandleUpdateTunnelConfig handles PUT requests to /network/tunnel/config
// @Summary Update tunnel configuration
// @Description Apply a new tunnel configuration on the fly. The existing tunnel is torn down and a new one is established. This endpoint is write-only; there is no corresponding GET to read the config back.
// @Tags network
// @Accept json
// @Produce json
// @Param request body TunnelConfigRequest true "Base64-encoded tunnel configuration"
// @Success 200 {object} SuccessResponse "Configuration applied"
// @Failure 400 {object} ErrorResponse "Invalid request body"
// @Failure 422 {object} ErrorResponse "Invalid tunnel configuration"
// @Failure 500 {object} ErrorResponse "Failed to apply configuration"
// @Router /network/tunnel/config [put]
func (h *NetworkHandler) HandleUpdateTunnelConfig(c *gin.Context) {
	var req TunnelConfigRequest
	if err := h.BindJSON(c, &req); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	config, err := networking.ParseBase64Config(req.Config)
	if err != nil {
		h.SendError(c, http.StatusUnprocessableEntity, fmt.Errorf("invalid tunnel config: %w", err))
		return
	}

	if err := networking.UpdateWireGuardConfig(config); err != nil {
		h.SendError(c, http.StatusInternalServerError, fmt.Errorf("failed to apply tunnel config: %w", err))
		return
	}

	h.SendSuccess(c, "Tunnel configuration updated successfully")
}

// HandleDisconnectTunnel handles DELETE requests to /network/tunnel
// @Summary Disconnect tunnel
// @Description Stop the network tunnel and restore the original network configuration. WARNING: After disconnecting, the sandbox will lose all outbound internet connectivity (no egress). Inbound connections to the sandbox will still work. Use PUT /network/tunnel/config to re-establish the tunnel.
// @Tags network
// @Produce json
// @Success 200 {object} SuccessResponse "Tunnel disconnected"
// @Failure 400 {object} ErrorResponse "No tunnel is running"
// @Failure 500 {object} ErrorResponse "Failed to stop tunnel"
// @Router /network/tunnel [delete]
func (h *NetworkHandler) HandleDisconnectTunnel(c *gin.Context) {
	if err := networking.StopWireGuard(); err != nil {
		h.SendError(c, http.StatusBadRequest, err)
		return
	}

	h.SendSuccess(c, "Tunnel disconnected")
}
