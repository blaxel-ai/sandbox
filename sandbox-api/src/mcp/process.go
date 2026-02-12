package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxWaitForCompletionTimeout is the maximum time to wait for a process when waitForCompletion is true.
// This prevents CloudFront/proxy timeouts (typically 60 seconds).
const MaxWaitForCompletionTimeout = 58

// Process tool input/output types
type ListProcessesInput struct{}

type ListProcessesOutput struct {
	Processes []handler.ProcessResponse `json:"processes"`
}

type ProcessExecuteInput struct {
	Command           string            `json:"command" jsonschema:"The command to execute"`
	Name              *string           `json:"name,omitempty" jsonschema:"Technical name for the process"`
	WorkingDir        *string           `json:"workingDir,omitempty" jsonschema:"The working directory for the command (default: /)"`
	Env               map[string]string `json:"env,omitempty" jsonschema:"Environment variables to set for the command"`
	WaitForCompletion *bool             `json:"waitForCompletion,omitempty" jsonschema:"Whether to wait for the command to complete before returning"`
	Timeout           *int              `json:"timeout,omitempty" jsonschema:"Timeout in seconds. When keepAlive is true, defaults to 600s. Set to 0 for infinite."`
	WaitForPorts      []int             `json:"waitForPorts,omitempty" jsonschema:"List of ports to wait for before returning"`
	RestartOnFailure  *bool             `json:"restartOnFailure,omitempty" jsonschema:"Whether to restart the process on failure (default: false)"`
	MaxRestarts       *int              `json:"maxRestarts,omitempty" jsonschema:"Maximum number of restarts (default: 0)"`
	KeepAlive         *bool             `json:"keepAlive,omitempty" jsonschema:"Disable scale-to-zero while process runs. Default timeout 600s. Set timeout to 0 for infinite."`
}

// ProcessExecuteOutput is the output for processExecute tool
type ProcessExecuteOutput struct {
	PID          string `json:"pid"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	ExitCode     int    `json:"exitCode"`
	Logs         string `json:"logs,omitempty"`
	Message      string `json:"message,omitempty"`
	PollRequired bool   `json:"pollRequired,omitempty"`
}

type ProcessIdentifierInput struct {
	Identifier string `json:"identifier" jsonschema:"Process identifier (PID or name)"`
}

type ProcessLogsOutput struct {
	Logs string `json:"logs"`
}

type ProcessStatusOutput struct {
	Status string `json:"status"`
}

// registerProcessTools registers process-related tools
func (s *Server) registerProcessTools() error {
	// List processes
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processesList",
		Description: "List all running processes",
	}, LogToolCall("processesList", func(ctx context.Context, req *mcp.CallToolRequest, input ListProcessesInput) (*mcp.CallToolResult, ListProcessesOutput, error) {
		processes := s.handlers.Process.ListProcesses()
		return nil, ListProcessesOutput{Processes: processes}, nil
	}))

	// Execute command
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processExecute",
		Description: "Execute a command",
	}, LogToolCall("processExecute", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessExecuteInput) (*mcp.CallToolResult, ProcessExecuteOutput, error) {
		// Apply defaults for optional fields
		name := ""
		if input.Name != nil {
			name = *input.Name
		}

		workingDir := "/"
		if input.WorkingDir != nil {
			workingDir = *input.WorkingDir
		}

		env := input.Env
		if env == nil {
			env = map[string]string{}
		}

		waitForCompletion := false
		if input.WaitForCompletion != nil {
			waitForCompletion = *input.WaitForCompletion
		}

		timeout := 30
		if input.Timeout != nil {
			timeout = *input.Timeout
		}

		waitForPorts := input.WaitForPorts
		if waitForPorts == nil {
			waitForPorts = []int{}
		}

		restartOnFailure := false
		if input.RestartOnFailure != nil {
			restartOnFailure = *input.RestartOnFailure
		}

		maxRestarts := 0
		if input.MaxRestarts != nil {
			maxRestarts = *input.MaxRestarts
		}

		keepAlive := false
		if input.KeepAlive != nil {
			keepAlive = *input.KeepAlive
		}

		// Set default timeout for keepAlive if not specified (default: 600s = 10 minutes)
		// Timeout of 0 means infinite (no auto-kill)
		if keepAlive && input.Timeout == nil {
			timeout = 600 // Default 10 minutes for keepAlive
		}

		// Cap the effective timeout for waitForCompletion to prevent proxy timeouts
		effectiveTimeout := timeout
		originalTimeoutExceeded := false
		if waitForCompletion && timeout > MaxWaitForCompletionTimeout {
			effectiveTimeout = MaxWaitForCompletionTimeout
			originalTimeoutExceeded = true
		}

		processInfo, err := s.handlers.Process.ExecuteProcess(
			input.Command,
			workingDir,
			name,
			env,
			waitForCompletion,
			effectiveTimeout,
			waitForPorts,
			restartOnFailure,
			maxRestarts,
			keepAlive,
		)

		// Check if this is a timeout error due to the capped timeout (CloudFront workaround)
		// In this case, it's not an error - we just need to tell the agent to poll
		// ExecuteProcess returns the process info even on timeout, so we can use it
		isTimeoutError := err != nil && strings.Contains(err.Error(), "process timed out")
		hasProcessInfo := processInfo.PID != "" // Check if we got valid process info
		if isTimeoutError && originalTimeoutExceeded && hasProcessInfo {
			output := ProcessExecuteOutput{
				PID:          processInfo.PID,
				Name:         processInfo.Name,
				Status:       processInfo.Status,
				ExitCode:     processInfo.ExitCode,
				PollRequired: true,
				Message: fmt.Sprintf(
					"Process is still running after %d seconds. Poll processGet with identifier '%s' (or PID %s) in a loop until the status is 'completed', 'failed', 'killed', or 'stopped'. The process continues running in the background.",
					MaxWaitForCompletionTimeout+2,
					processInfo.Name,
					processInfo.PID,
				),
			}
			if processInfo.Logs != nil {
				output.Logs = *processInfo.Logs
			}
			return nil, output, nil
		}

		if err != nil {
			return nil, ProcessExecuteOutput{}, err
		}

		// Build response
		output := ProcessExecuteOutput{
			PID:      processInfo.PID,
			Name:     processInfo.Name,
			Status:   processInfo.Status,
			ExitCode: processInfo.ExitCode,
		}

		if processInfo.Logs != nil {
			output.Logs = *processInfo.Logs
		}

		return nil, output, nil
	}))

	// Get process by identifier
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processGet",
		Description: "Get process information by identifier (PID or name)",
	}, LogToolCall("processGet", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, handler.ProcessResponse, error) {
		process, err := s.handlers.Process.GetProcess(input.Identifier)
		if err != nil {
			return nil, handler.ProcessResponse{}, fmt.Errorf("failed to get process: %w", err)
		}
		return nil, process, nil
	}))

	// Get process logs
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processGetLogs",
		Description: "Get logs for a specific process",
	}, LogToolCall("processGetLogs", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, ProcessLogsOutput, error) {
		logs, err := s.handlers.Process.GetProcessOutput(input.Identifier)
		if err != nil {
			return nil, ProcessLogsOutput{}, fmt.Errorf("failed to get process logs: %w", err)
		}
		return nil, ProcessLogsOutput{Logs: logs.Logs}, nil
	}))

	// Stop process
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processStop",
		Description: "Stop a specific process",
	}, LogToolCall("processStop", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, ProcessStatusOutput, error) {
		if err := s.handlers.Process.StopProcess(input.Identifier); err != nil {
			return nil, ProcessStatusOutput{}, fmt.Errorf("failed to stop process: %w", err)
		}
		return nil, ProcessStatusOutput{Status: "stopped"}, nil
	}))

	// Kill process
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processKill",
		Description: "Kill a specific process",
	}, LogToolCall("processKill", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, ProcessStatusOutput, error) {
		if err := s.handlers.Process.KillProcess(input.Identifier); err != nil {
			return nil, ProcessStatusOutput{}, fmt.Errorf("failed to kill process: %w", err)
		}
		return nil, ProcessStatusOutput{Status: "killed"}, nil
	}))

	return nil
}
