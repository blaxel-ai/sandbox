package mcp

import (
	"fmt"

	mcp_golang "github.com/metoro-io/mcp-golang"
)

// registerProcessTools registers process-related tools
func (s *Server) registerProcessTools() error {
	// List processes
	type ListProcessesArgs struct{}
	if err := s.mcpServer.RegisterTool("processesList", "List all running processes",
		func(args ListProcessesArgs) (*mcp_golang.ToolResponse, error) {
			processes := s.handlers.Process.ListProcesses()
			return CreateJSONResponse(processes)
		}); err != nil {
		return fmt.Errorf("failed to register listProcesses tool: %w", err)
	}

	// Execute command
	if err := s.mcpServer.RegisterTool("processExecute", "Execute a command",
		func(args ProcessArgs) (*mcp_golang.ToolResponse, error) {
			processInfo, err := s.handlers.Process.ExecuteProcess(args.Command, args.WorkingDir, args.Name, args.WaitForCompletion, args.Timeout, args.WaitForPorts)
			if err != nil {
				return nil, err
			}
			return CreateJSONResponse(processInfo)
		}); err != nil {
		return fmt.Errorf("failed to register executeCommand tool: %w", err)
	}

	// Get process by name
	if err := s.mcpServer.RegisterTool("processGetByName", "Get process information by name",
		func(args ProcessNameArgs) (*mcp_golang.ToolResponse, error) {
			processInfo, err := s.handlers.Process.GetProcessByName(args.Name)
			if err != nil {
				return nil, fmt.Errorf("process with name '%s' not found", args.Name)
			}
			return CreateJSONResponse(processInfo)
		}); err != nil {
		return fmt.Errorf("failed to register getProcessByName tool: %w", err)
	}

	// Get process logs
	if err := s.mcpServer.RegisterTool("processGetLogs", "Get logs for a specific process",
		func(args ProcessIDArgs) (*mcp_golang.ToolResponse, error) {
			stdout, stderr, err := s.handlers.Process.GetProcessOutput(args.PID)
			if err != nil {
				return nil, fmt.Errorf("failed to get process output: %w", err)
			}

			response := map[string]interface{}{
				"pid":    args.PID,
				"stdout": stdout,
				"stderr": stderr,
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register getProcessLogs tool: %w", err)
	}

	// Stop process
	if err := s.mcpServer.RegisterTool("processStop", "Stop a specific process",
		func(args ProcessIDArgs) (*mcp_golang.ToolResponse, error) {
			err := s.handlers.Process.StopProcess(args.PID)
			if err != nil {
				return nil, fmt.Errorf("failed to stop process: %w", err)
			}

			response := map[string]interface{}{
				"pid":     args.PID,
				"message": "Process stopped successfully",
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register stopProcess tool: %w", err)
	}

	// Kill process
	if err := s.mcpServer.RegisterTool("processKill", "Kill a specific process",
		func(args ProcessIDArgs) (*mcp_golang.ToolResponse, error) {
			err := s.handlers.Process.KillProcess(args.PID)
			if err != nil {
				return nil, fmt.Errorf("failed to kill process: %w", err)
			}

			response := map[string]interface{}{
				"pid":     args.PID,
				"message": "Process killed successfully",
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register killProcess tool: %w", err)
	}

	return nil
}
