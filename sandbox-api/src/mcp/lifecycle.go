package mcp

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// LifecycleStopInput is the input for stop tool
type LifecycleStopInput struct {
	Timeout *int `json:"timeout,omitempty" jsonschema:"Optional timeout in seconds before the stop takes effect. Default is 0 (immediate). If set, the stop will be scheduled."`
}

// LifecycleStatusInput is the input for status tool (empty)
type LifecycleStatusInput struct{}

// KeepAliveProcessInfo contains info about a process with keepAlive enabled
type KeepAliveProcessInfo struct {
	PID       string     `json:"pid"`
	Name      string     `json:"name"`
	Command   string     `json:"command"`
	StartedAt *time.Time `json:"startedAt,omitempty"`
}

// LifecycleStatusOutput is the output for lifecycle tools
type LifecycleStatusOutput struct {
	State              string                 `json:"state"`
	ScheduledStopAt    *time.Time             `json:"scheduledStopAt,omitempty"`
	KeepAliveProcesses []KeepAliveProcessInfo `json:"keepAliveProcesses"`
}

const (
	StateAwake = "awake" // At least one keepAlive process exists
	StateAuto  = "auto"  // No keepAlive processes
)

func (s *Server) registerLifecycleTools() {
	// stop tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "stop",
		Description: "Force stop the sandbox by removing keepAlive from all current keepAlive processes, enabling auto-hibernation. Optionally schedule the stop with a timeout in seconds.",
	}, LogToolCall("stop", func(ctx context.Context, req *mcp.CallToolRequest, input LifecycleStopInput) (*mcp.CallToolResult, LifecycleStatusOutput, error) {
		timeout := 0
		if input.Timeout != nil {
			timeout = *input.Timeout
		}
		result, err := s.lifecycleStop(timeout)
		if err != nil {
			return nil, LifecycleStatusOutput{}, err
		}

		return nil, *result, nil
	}))

	// status tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "status",
		Description: "Get the current sandbox status, scheduled stop time, and active keepAlive processes.",
	}, LogToolCall("status", func(ctx context.Context, req *mcp.CallToolRequest, input LifecycleStatusInput) (*mcp.CallToolResult, LifecycleStatusOutput, error) {
		result := s.lifecycleStatus()
		return nil, *result, nil
	}))
}

// getKeepAliveProcesses returns processes with keepAlive enabled
func (s *Server) getKeepAliveProcesses() []KeepAliveProcessInfo {
	processes := s.handlers.Lifecycle.GetKeepAliveProcesses()
	result := make([]KeepAliveProcessInfo, len(processes))
	for i, p := range processes {
		result[i] = KeepAliveProcessInfo{
			PID:       p.PID,
			Name:      p.Name,
			Command:   p.Command,
			StartedAt: p.StartedAt,
		}
	}
	return result
}

// lifecycleStop force stops the sandbox by removing keepAlive from processes
func (s *Server) lifecycleStop(timeout int) (*LifecycleStatusOutput, error) {
	lh := s.handlers.Lifecycle

	// Use the shared stop logic (handles both immediate and scheduled stops)
	lh.ExecuteStopWithTimeout(timeout)

	// Return the updated status
	lh.Lock()
	defer lh.Unlock()

	return &LifecycleStatusOutput{
		State:              lh.GetState(),
		ScheduledStopAt:    lh.GetScheduledStopAt(),
		KeepAliveProcesses: s.getKeepAliveProcesses(),
	}, nil
}

// lifecycleStatus returns the current sandbox status
func (s *Server) lifecycleStatus() *LifecycleStatusOutput {
	lh := s.handlers.Lifecycle

	lh.Lock()
	defer lh.Unlock()

	return &LifecycleStatusOutput{
		State:              lh.GetState(),
		ScheduledStopAt:    lh.GetScheduledStopAt(),
		KeepAliveProcesses: s.getKeepAliveProcesses(),
	}
}
