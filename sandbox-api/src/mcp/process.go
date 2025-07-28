package mcp

import (
	"fmt"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	mcp_golang "github.com/metoro-io/mcp-golang"
)

// registerProcessTools registers process-related tools
func (s *Server) registerProcessTools() error {
	// List processes
	type ListProcessesArgs struct{}
	if err := s.mcpServer.RegisterTool("processesList", "List all running processes",
		LogToolCall("processesList", func(args ListProcessesArgs) (*mcp_golang.ToolResponse, error) {
			processes := s.handlers.Process.ListProcesses()
			return CreateJSONResponse(processes)
		})); err != nil {
		return fmt.Errorf("failed to register listProcesses tool: %w", err)
	}

	// Execute command
	if err := s.mcpServer.RegisterTool("processExecute", "Execute a command",
		LogToolCall("processExecute", func(args ProcessExecuteArgs) (*mcp_golang.ToolResponse, error) {
			processInfo, err := s.handlers.Process.ExecuteProcess(args.Command, args.WorkingDir, args.Name, args.Env, args.WaitForCompletion, args.Timeout, args.WaitForPorts, args.RestartOnFailure, args.MaxRestarts)
			if err != nil {
				return nil, err
			}
			if !args.IncludeLogs {
				return CreateJSONResponse(processInfo)
			}
			logs, err := s.handlers.Process.GetProcessOutput(processInfo.PID)
			if err != nil {
				return nil, fmt.Errorf("failed to get process output: %w", err)
			}
			processResponseWithLogs := handler.ProcessResponseWithLogs{
				ProcessResponse: processInfo,
				Logs:            logs.Logs,
			}
			fmt.Println(processResponseWithLogs.Logs)
			return CreateJSONResponse(processResponseWithLogs)
		})); err != nil {
		return fmt.Errorf("failed to register executeCommand tool: %w", err)
	}

	// Get process by identifier
	if err := s.mcpServer.RegisterTool("processGet", "Get process information by identifier (PID or name)",
		LogToolCall("processGet", func(args ProcessIdentifierArgs) (*mcp_golang.ToolResponse, error) {
			processInfo, err := s.handlers.Process.GetProcess(args.Identifier)
			if err != nil {
				return nil, fmt.Errorf("process with identifier '%s' not found", args.Identifier)
			}
			return CreateJSONResponse(processInfo)
		})); err != nil {
		return fmt.Errorf("failed to register getProcess tool: %w", err)
	}

	// Get process logs
	if err := s.mcpServer.RegisterTool("processGetLogs", "Get logs for a specific process",
		LogToolCall("processGetLogs", func(args ProcessIdentifierArgs) (*mcp_golang.ToolResponse, error) {
			logs, err := s.handlers.Process.GetProcessOutput(args.Identifier)
			if err != nil {
				return nil, fmt.Errorf("failed to get process output: %w", err)
			}
			return CreateJSONResponse(logs)
		})); err != nil {
		return fmt.Errorf("failed to register getProcessLogs tool: %w", err)
	}

	// Stop process
	if err := s.mcpServer.RegisterTool("processStop", "Stop a specific process",
		LogToolCall("processStop", func(args ProcessIdentifierArgs) (*mcp_golang.ToolResponse, error) {
			err := s.handlers.Process.StopProcess(args.Identifier)
			if err != nil {
				return nil, fmt.Errorf("failed to stop process: %w", err)
			}

			response := map[string]interface{}{
				"identifier": args.Identifier,
				"message":    "Process stopped successfully",
			}

			return CreateJSONResponse(response)
		})); err != nil {
		return fmt.Errorf("failed to register stopProcess tool: %w", err)
	}

	// Kill process
	if err := s.mcpServer.RegisterTool("processKill", "Kill a specific process",
		LogToolCall("processKill", func(args ProcessIdentifierArgs) (*mcp_golang.ToolResponse, error) {
			err := s.handlers.Process.KillProcess(args.Identifier)
			if err != nil {
				return nil, fmt.Errorf("failed to kill process: %w", err)
			}

			response := map[string]interface{}{
				"identifier": args.Identifier,
				"message":    "Process killed successfully",
			}

			return CreateJSONResponse(response)
		})); err != nil {
		return fmt.Errorf("failed to register killProcess tool: %w", err)
	}

	return nil
}
