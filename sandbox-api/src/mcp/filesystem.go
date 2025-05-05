package mcp

import (
	"fmt"
	"os"
	"strconv"

	mcp_golang "github.com/metoro-io/mcp-golang"
)

// registerFileSystemTools registers filesystem-related tools
func (s *Server) registerFileSystemTools() error {
	type GetWorkingDirectoryArgs struct{}
	// Get working directory
	if err := s.mcpServer.RegisterTool("fsGetWorkingDirectory", "Get the current working directory",
		func(args GetWorkingDirectoryArgs) (*mcp_golang.ToolResponse, error) {
			workingDir, err := s.handlers.FileSystem.GetWorkingDirectory()
			if err != nil {
				return nil, fmt.Errorf("failed to get working directory: %w", err)
			}

			return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(workingDir)), nil
		}); err != nil {
		return fmt.Errorf("failed to register getWorkingDirectory tool: %w", err)
	}

	// List directory
	if err := s.mcpServer.RegisterTool("fsListDirectory", "List contents of a directory",
		func(args FsListDirectoryArgs) (*mcp_golang.ToolResponse, error) {
			dir, err := s.handlers.FileSystem.ListDirectory(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to list directory: %w", err)
			}
			return CreateJSONResponse(dir)
		}); err != nil {
		return fmt.Errorf("failed to register listDirectory tool: %w", err)
	}

	// Read file
	if err := s.mcpServer.RegisterTool("fsReadFile", "Read contents of a file",
		func(args FsReadFileArgs) (*mcp_golang.ToolResponse, error) {
			file, err := s.handlers.FileSystem.ReadFile(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to read file: %w", err)
			}
			return CreateJSONResponse(file)
		}); err != nil {
		return fmt.Errorf("failed to register readFile tool: %w", err)
	}

	// Write file
	if err := s.mcpServer.RegisterTool("fsWriteFile", "Create or update a file",
		func(args FsWriteArgs) (*mcp_golang.ToolResponse, error) {
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
				err := s.handlers.FileSystem.CreateDirectory(args.Path, permissions)
				if err != nil {
					return nil, fmt.Errorf("failed to create directory: %w", err)
				}
				response := map[string]interface{}{
					"path":    args.Path,
					"message": "Directory created successfully",
				}
				return CreateJSONResponse(response)
			} else {
				// Create or update file
				err := s.handlers.FileSystem.WriteFile(args.Path, []byte(args.Content), permissions)
				if err != nil {
					return nil, fmt.Errorf("failed to write file: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "File created/updated successfully",
				}

				return CreateJSONResponse(response)
			}
		}); err != nil {
		return fmt.Errorf("failed to register writeFile tool: %w", err)
	}

	// Delete file or directory
	if err := s.mcpServer.RegisterTool("fsDeleteFileOrDirectory", "Delete a file or directory",
		func(args FsDeleteArgs) (*mcp_golang.ToolResponse, error) {
			// Check if it's a directory
			isDir, err := s.handlers.FileSystem.DirectoryExists(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to check if path is a directory: %w", err)
			}

			if isDir {
				// Delete directory
				err := s.handlers.FileSystem.DeleteDirectory(args.Path, args.Recursive)
				if err != nil {
					return nil, fmt.Errorf("failed to delete directory: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "Directory deleted successfully",
				}

				return CreateJSONResponse(response)
			}

			// Check if it's a file
			isFile, err := s.handlers.FileSystem.FileExists(args.Path)
			if err != nil {
				return nil, fmt.Errorf("failed to check if path is a file: %w", err)
			}

			if isFile {
				// Delete file
				err := s.handlers.FileSystem.DeleteFile(args.Path)
				if err != nil {
					return nil, fmt.Errorf("failed to delete file: %w", err)
				}

				response := map[string]interface{}{
					"path":    args.Path,
					"message": "File deleted successfully",
				}

				return CreateJSONResponse(response)
			}

			return nil, fmt.Errorf("path %s does not exist", args.Path)
		}); err != nil {
		return fmt.Errorf("failed to register deleteFileOrDirectory tool: %w", err)
	}

	return nil
}
