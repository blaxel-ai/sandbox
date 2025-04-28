package mcp

import (
	"context"
	"fmt"
	"time"

	mcp_golang "github.com/metoro-io/mcp-golang"

	"github.com/beamlit/uvm-api/src/api"
	"github.com/beamlit/uvm-api/src/process"
)

// registerProcessTools registers process-related tools
func (s *Server) registerProcessTools() error {
	// List processes
	type ListProcessesArgs struct{}
	if err := s.mcpServer.RegisterTool("processesList", "List all running processes",
		func(args ListProcessesArgs) (*mcp_golang.ToolResponse, error) {
			pm := process.GetProcessManager()
			processes := pm.ListProcesses()

			response := make([]api.ProcessResponse, 0, len(processes))
			for _, p := range processes {
				resp := api.ProcessResponse{
					PID:        p.PID,
					Name:       p.Name,
					Command:    p.Command,
					Status:     p.Status,
					StartedAt:  p.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
					WorkingDir: p.WorkingDir,
				}

				if p.CompletedAt != nil {
					resp.CompletedAt = p.CompletedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT")
					resp.ExitCode = p.ExitCode
				}

				response = append(response, resp)
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register listProcesses tool: %w", err)
	}

	// Execute command
	if err := s.mcpServer.RegisterTool("processExecute", "Execute a command",
		func(args ProcessArgs) (*mcp_golang.ToolResponse, error) {
			var pid int
			var err error
			pm := process.GetProcessManager()

			timeout := 60 // Default timeout of 60 seconds
			if args.Timeout > 0 {
				timeout = args.Timeout
			}

			// Create a context with the specified timeout
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
			defer cancel()

			// Create a completion channel
			completionCh := make(chan int)
			defer close(completionCh)

			// Create a callback function
			callback := func(p *process.ProcessInfo) {
				completionCh <- p.PID
			}
			if args.Name != "" {
				pid, err = pm.StartProcessWithName(args.Command, args.WorkingDir, args.Name, callback)
				if err != nil {
					return nil, fmt.Errorf("failed to start process: %w", err)
				}
			} else {
				pid, err = pm.StartProcess(args.Command, args.WorkingDir, callback)
				if err != nil {
					return nil, fmt.Errorf("failed to start process: %w", err)
				}
			}

			// If wait for completion is true, wait for the process to complete
			if args.WaitForCompletion {
				select {
				case <-completionCh:
					// Process completed
					break
				case <-ctx.Done():
					// Timeout reached
					return nil, fmt.Errorf("process timed out after %d seconds", timeout)
				}
			}

			processInfo, exists := pm.GetProcess(pid)
			if !exists {
				return nil, fmt.Errorf("process creation failed")
			}

			response := api.ProcessResponse{
				PID:        processInfo.PID,
				Name:       processInfo.Name,
				Command:    processInfo.Command,
				Status:     processInfo.Status,
				StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
				WorkingDir: processInfo.WorkingDir,
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register executeCommand tool: %w", err)
	}

	// Get process by name
	if err := s.mcpServer.RegisterTool("processGetByName", "Get process information by name",
		func(args ProcessNameArgs) (*mcp_golang.ToolResponse, error) {
			pm := process.GetProcessManager()
			processInfo, exists := pm.GetProcessByName(args.Name)
			if !exists {
				return nil, fmt.Errorf("process with name '%s' not found", args.Name)
			}

			response := api.ProcessResponse{
				PID:        processInfo.PID,
				Name:       processInfo.Name,
				Command:    processInfo.Command,
				Status:     processInfo.Status,
				StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
				WorkingDir: processInfo.WorkingDir,
			}
			fmt.Println(response.Name)
			if processInfo.CompletedAt != nil {
				response.CompletedAt = processInfo.CompletedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT")
				response.ExitCode = processInfo.ExitCode
			}

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register getProcessByName tool: %w", err)
	}

	// Get process logs
	if err := s.mcpServer.RegisterTool("processGetLogs", "Get logs for a specific process",
		func(args ProcessIDArgs) (*mcp_golang.ToolResponse, error) {
			pm := process.GetProcessManager()
			stdout, stderr, err := pm.GetProcessOutput(args.PID)

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
			pm := process.GetProcessManager()
			err := pm.StopProcess(args.PID)

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
			pm := process.GetProcessManager()
			err := pm.KillProcess(args.PID)

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
