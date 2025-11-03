package mcp

import (
	"context"
	"fmt"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

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
	Timeout           *int              `json:"timeout,omitempty" jsonschema:"Timeout in seconds for the command (default: 30)"`
	WaitForPorts      []int             `json:"waitForPorts,omitempty" jsonschema:"List of ports to wait for before returning"`
	IncludeLogs       *bool             `json:"includeLogs,omitempty" jsonschema:"Whether to include logs in the response"`
	RestartOnFailure  *bool             `json:"restartOnFailure,omitempty" jsonschema:"Whether to restart the process on failure (default: false)"`
	MaxRestarts       *int              `json:"maxRestarts,omitempty" jsonschema:"Maximum number of restarts (default: 0)"`
}

type ProcessExecuteOutput struct {
	Process  handler.ProcessResponse         `json:"process,omitempty"`
	WithLogs handler.ProcessResponseWithLogs `json:"withLogs,omitempty"`
}

type ProcessIdentifierInput struct {
	Identifier string `json:"identifier" jsonschema:"Process identifier (PID or name)"`
}

type ProcessInfoOutput struct {
	Process handler.ProcessResponse `json:"process"`
}

type ProcessLogsOutput struct {
	Logs string `json:"logs"`
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

		includeLogs := false
		if input.IncludeLogs != nil {
			includeLogs = *input.IncludeLogs
		}

		restartOnFailure := false
		if input.RestartOnFailure != nil {
			restartOnFailure = *input.RestartOnFailure
		}

		maxRestarts := 0
		if input.MaxRestarts != nil {
			maxRestarts = *input.MaxRestarts
		}
		processInfo, err := s.handlers.Process.ExecuteProcess(
			input.Command,
			workingDir,
			name,
			env,
			waitForCompletion,
			timeout,
			waitForPorts,
			restartOnFailure,
			maxRestarts,
		)
		if err != nil {
			return nil, ProcessExecuteOutput{}, err
		}

		if !includeLogs {
			return nil, ProcessExecuteOutput{Process: processInfo}, nil
		}

		logs, err := s.handlers.Process.GetProcessOutput(processInfo.PID)
		if err != nil {
			return nil, ProcessExecuteOutput{}, fmt.Errorf("failed to get process output: %w", err)
		}

		withLogs := handler.ProcessResponseWithLogs{
			ProcessResponse: processInfo,
			Logs:            logs.Logs,
		}

		return nil, ProcessExecuteOutput{WithLogs: withLogs}, nil
	}))

	// Get process by identifier
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processGet",
		Description: "Get process information by identifier (PID or name)",
	}, LogToolCall("processGet", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, ProcessInfoOutput, error) {
		process, err := s.handlers.Process.GetProcess(input.Identifier)
		if err != nil {
			return nil, ProcessInfoOutput{}, fmt.Errorf("failed to get process: %w", err)
		}
		return nil, ProcessInfoOutput{Process: process}, nil
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
	}, LogToolCall("processStop", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, map[string]string, error) {
		if err := s.handlers.Process.StopProcess(input.Identifier); err != nil {
			return nil, nil, fmt.Errorf("failed to stop process: %w", err)
		}
		return nil, map[string]string{"status": "stopped"}, nil
	}))

	// Kill process
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "processKill",
		Description: "Kill a specific process",
	}, LogToolCall("processKill", func(ctx context.Context, req *mcp.CallToolRequest, input ProcessIdentifierInput) (*mcp.CallToolResult, map[string]string, error) {
		if err := s.handlers.Process.KillProcess(input.Identifier); err != nil {
			return nil, nil, fmt.Errorf("failed to kill process: %w", err)
		}
		return nil, map[string]string{"status": "killed"}, nil
	}))

	return nil
}
