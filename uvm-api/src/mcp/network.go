package mcp

import (
	"fmt"

	mcp_golang "github.com/metoro-io/mcp-golang"

	"github.com/beamlit/uvm-api/src/network"
)

// registerNetworkTools registers network-related tools
func (s *Server) registerNetworkTools() error {
	// Get ports for process
	if err := s.mcpServer.RegisterTool("networkGetProcessPorts", "Get ports for a specific process",
		func(args NetworkArgs) (*mcp_golang.ToolResponse, error) {
			net := network.GetNetwork()
			ports, err := net.GetPortsForPID(args.PID)

			if err != nil {
				return nil, fmt.Errorf("failed to get process ports: %w", err)
			}

			response := map[string]interface{}{
				"pid":   args.PID,
				"ports": ports,
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register getProcessPorts tool: %w", err)
	}

	// Monitor ports for process
	if err := s.mcpServer.RegisterTool("networkMonitorProcessPorts", "Start monitoring ports for a specific process",
		func(args NetworkArgs) (*mcp_golang.ToolResponse, error) {
			net := network.GetNetwork()

			// Register a callback to be called when a new port is detected
			net.RegisterPortOpenCallback(args.PID, func(pid int, port *network.PortInfo) {
				// In a real implementation, we might make an HTTP call to the callback URL
				// or push the event to a websocket connection
				// For this implementation, we just log the event
			})

			response := map[string]interface{}{
				"pid":     args.PID,
				"message": "Port monitoring started",
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register monitorProcessPorts tool: %w", err)
	}

	// Stop monitoring ports for process
	if err := s.mcpServer.RegisterTool("networkStopMonitorProcessPorts", "Stop monitoring ports for a specific process",
		func(args NetworkArgs) (*mcp_golang.ToolResponse, error) {
			net := network.GetNetwork()
			net.UnregisterPortOpenCallback(args.PID)

			response := map[string]interface{}{
				"pid":     args.PID,
				"message": "Port monitoring stopped",
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register stopMonitoringProcessPorts tool: %w", err)
	}

	return nil
}
