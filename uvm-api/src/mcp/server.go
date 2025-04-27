package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	mcp_golang "github.com/metoro-io/mcp-golang"

	"github.com/beamlit/uvm-api/src/api"
	"github.com/beamlit/uvm-api/src/filesystem"
	"github.com/beamlit/uvm-api/src/network"
	"github.com/beamlit/uvm-api/src/process"
)

// Server represents the MCP server
type Server struct {
	mcpServer *mcp_golang.Server
}

// ProcessArgs represents arguments for process-related tools
type ProcessArgs struct {
	Command           string `json:"command" jsonschema:"required,description=The command to execute"`
	WorkingDir        string `json:"workingDir" jsonschema:"description=The working directory for the command"`
	WaitForCompletion bool   `json:"waitForCompletion" jsonschema:"description=Whether to wait for the command to complete before returning"`
	Timeout           int    `json:"timeout" jsonschema:"description=Timeout in seconds for the command"`
	WaitForPorts      []int  `json:"waitForPorts" jsonschema:"description=List of ports to wait for before returning"`
}

// ProcessIDArgs represents arguments for process ID-related tools
type ProcessIDArgs struct {
	PID int `json:"pid" jsonschema:"required,description=Process ID"`
}

// FileSystemArgs represents arguments for filesystem-related tools
type FileSystemArgs struct {
	Path        string `json:"path" jsonschema:"required,description=Path to the file or directory"`
	Content     string `json:"content" jsonschema:"description=Content to write to the file"`
	IsDirectory bool   `json:"isDirectory" jsonschema:"description=Whether the path refers to a directory"`
	Permissions string `json:"permissions" jsonschema:"description=Permissions for the file or directory (octal string)"`
	Recursive   bool   `json:"recursive" jsonschema:"description=Whether to perform the operation recursively"`
}

// NetworkArgs represents arguments for network-related tools
type NetworkArgs struct {
	PID      int    `json:"pid" jsonschema:"required,description=Process ID"`
	Callback string `json:"callback" jsonschema:"description=Callback URL for port monitoring notifications"`
}

// NewServer creates a new MCP server
func NewServer(gin *gin.Engine) (*Server, error) {
	fmt.Println("Creating MCP server")
	transport := NewWebSocketTransport(gin)
	mcpServer := mcp_golang.NewServer(transport, mcp_golang.WithName("Sandbox API Server"))

	server := &Server{
		mcpServer: mcpServer,
	}

	fmt.Println("Registering tools")
	// Register all tools
	err := server.registerTools()
	if err != nil {
		return nil, fmt.Errorf("failed to register tools: %w", err)
	}

	fmt.Println("Tools registered")

	return server, nil
}

// Serve starts the MCP server
func (s *Server) Serve() error {
	return s.mcpServer.Serve()
}

// registerTools registers all the tools with the MCP server
func (s *Server) registerTools() error {
	// Process tools
	if err := s.registerProcessTools(); err != nil {
		return err
	}

	// Filesystem tools
	if err := s.registerFileSystemTools(); err != nil {
		return err
	}

	// Network tools
	if err := s.registerNetworkTools(); err != nil {
		return err
	}

	return nil
}

// registerProcessTools registers process-related tools
func (s *Server) registerProcessTools() error {
	// List processes
	type ListProcessesArgs struct{}
	if err := s.mcpServer.RegisterTool("listProcesses", "List all running processes",
		func(args ListProcessesArgs) (*mcp_golang.ToolResponse, error) {
			pm := process.GetProcessManager()
			processes := pm.ListProcesses()

			response := make([]api.ProcessResponse, 0, len(processes))
			for _, p := range processes {
				resp := api.ProcessResponse{
					PID:        p.PID,
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register listProcesses tool: %w", err)
	}

	// Execute command
	if err := s.mcpServer.RegisterTool("executeCommand", "Execute a command",
		func(args ProcessArgs) (*mcp_golang.ToolResponse, error) {
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

			pid, err := pm.StartProcess(args.Command, args.WorkingDir, callback)
			if err != nil {
				return nil, fmt.Errorf("failed to start process: %w", err)
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
				Command:    processInfo.Command,
				Status:     processInfo.Status,
				StartedAt:  processInfo.StartedAt.Format("Mon, 02 Jan 2006 15:04:05 GMT"),
				WorkingDir: processInfo.WorkingDir,
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register executeCommand tool: %w", err)
	}

	// Get process logs
	if err := s.mcpServer.RegisterTool("getProcessLogs", "Get logs for a specific process",
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register getProcessLogs tool: %w", err)
	}

	// Stop process
	if err := s.mcpServer.RegisterTool("stopProcess", "Stop a specific process",
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register stopProcess tool: %w", err)
	}

	// Kill process
	if err := s.mcpServer.RegisterTool("killProcess", "Kill a specific process",
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register killProcess tool: %w", err)
	}

	return nil
}

// registerFileSystemTools registers filesystem-related tools
func (s *Server) registerFileSystemTools() error {
	type GetWorkingDirectoryArgs struct{}
	fs := filesystem.NewFilesystem("/")
	// Get working directory
	if err := s.mcpServer.RegisterTool("getWorkingDirectory", "Get the current working directory",
		func(args GetWorkingDirectoryArgs) (*mcp_golang.ToolResponse, error) {
			workingDir, err := fs.GetAbsolutePath("/")
			if err != nil {
				return nil, fmt.Errorf("failed to get working directory: %w", err)
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(workingDir)), nil
		}); err != nil {
		return fmt.Errorf("failed to register getWorkingDirectory tool: %w", err)
	}

	// List directory
	if err := s.mcpServer.RegisterTool("listDirectory", "List contents of a directory",
		func(args FileSystemArgs) (*mcp_golang.ToolResponse, error) {
			dir, err := fs.ListDirectory(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to list directory: %w", err)
			}

			files := make([]map[string]interface{}, 0, len(dir.Files))
			for _, file := range dir.Files {
				files = append(files, map[string]interface{}{
					"path":         file.Path,
					"permissions":  file.Permissions.String(),
					"size":         file.Size,
					"lastModified": file.LastModified,
					"owner":        file.Owner,
					"group":        file.Group,
				})
			}

			subdirs := make([]map[string]interface{}, 0, len(dir.Subdirectories))
			for _, subdir := range dir.Subdirectories {
				subdirs = append(subdirs, map[string]interface{}{
					"path": subdir.Path,
				})
			}

			response := map[string]interface{}{
				"path":           dir.Path,
				"files":          files,
				"subdirectories": subdirs,
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register listDirectory tool: %w", err)
	}

	// Read file
	if err := s.mcpServer.RegisterTool("readFile", "Read contents of a file",
		func(args FileSystemArgs) (*mcp_golang.ToolResponse, error) {
			file, err := fs.ReadFile(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}

			response := map[string]interface{}{
				"path":         file.Path,
				"content":      string(file.Content),
				"permissions":  file.Permissions.String(),
				"size":         file.Size,
				"lastModified": file.LastModified,
				"owner":        file.Owner,
				"group":        file.Group,
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register readFile tool: %w", err)
	}

	// Write file
	if err := s.mcpServer.RegisterTool("writeFile", "Create or update a file",
		func(args FileSystemArgs) (*mcp_golang.ToolResponse, error) {
			// Parse permissions or use default
			var permissions os.FileMode = 0644
			if args.Permissions != "" {
				permInt, err := strconv.ParseUint(args.Permissions, 8, 32)
				if err == nil {
					permissions = os.FileMode(permInt)
				}
			}

			if args.IsDirectory {
				// Create directory
				err := fs.CreateDirectory(args.Path, permissions)
				if err != nil {
					return nil, fmt.Errorf("failed to create directory: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "Directory created successfully",
				}

				return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
			} else {
				// Create or update file
				err := fs.WriteFile(args.Path, []byte(args.Content), permissions)
				if err != nil {
					return nil, fmt.Errorf("failed to write file: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "File created/updated successfully",
				}

				return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
			}
		}); err != nil {
		return fmt.Errorf("failed to register writeFile tool: %w", err)
	}

	// Delete file or directory
	if err := s.mcpServer.RegisterTool("deleteFileOrDirectory", "Delete a file or directory",
		func(args FileSystemArgs) (*mcp_golang.ToolResponse, error) {

			// Check if it's a directory
			isDir, err := fs.DirectoryExists(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to check if path is a directory: %w", err)
			}

			if isDir {
				// Delete directory
				err := fs.DeleteDirectory(args.Path, args.Recursive)
				if err != nil {
					return nil, fmt.Errorf("failed to delete directory: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "Directory deleted successfully",
				}

				return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
			}

			// Check if it's a file
			isFile, err := fs.FileExists(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to check if path is a file: %w", err)
			}

			if isFile {
				// Delete file
				err := fs.DeleteFile(args.Path)
				if err != nil {
					return nil, fmt.Errorf("failed to delete file: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "File deleted successfully",
				}

				return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
			}

			return nil, fmt.Errorf("path %s does not exist", args.Path)
		}); err != nil {
		return fmt.Errorf("failed to register deleteFileOrDirectory tool: %w", err)
	}

	return nil
}

// registerNetworkTools registers network-related tools
func (s *Server) registerNetworkTools() error {
	// Get ports for process
	if err := s.mcpServer.RegisterTool("getProcessPorts", "Get ports for a specific process",
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register getProcessPorts tool: %w", err)
	}

	// Monitor ports for process
	if err := s.mcpServer.RegisterTool("monitorProcessPorts", "Start monitoring ports for a specific process",
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

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register monitorProcessPorts tool: %w", err)
	}

	// Stop monitoring ports for process
	if err := s.mcpServer.RegisterTool("stopMonitoringProcessPorts", "Stop monitoring ports for a specific process",
		func(args NetworkArgs) (*mcp_golang.ToolResponse, error) {
			net := network.GetNetwork()
			net.UnregisterPortOpenCallback(args.PID)

			response := map[string]interface{}{
				"pid":     args.PID,
				"message": "Port monitoring stopped",
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(fmt.Sprintf("%+v", response))), nil
		}); err != nil {
		return fmt.Errorf("failed to register stopMonitoringProcessPorts tool: %w", err)
	}

	return nil
}
