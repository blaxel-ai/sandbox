package mcp

import (
	"fmt"
	"os"
	"strconv"

	mcp_golang "github.com/metoro-io/mcp-golang"

	"github.com/beamlit/uvm-api/src/filesystem"
)

// registerFileSystemTools registers filesystem-related tools
func (s *Server) registerFileSystemTools() error {
	type GetWorkingDirectoryArgs struct{}
	fs := filesystem.NewFilesystem("/")
	// Get working directory
	if err := s.mcpServer.RegisterTool("fsGetWorkingDirectory", "Get the current working directory",
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
	if err := s.mcpServer.RegisterTool("fsListDirectory", "List contents of a directory",
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

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register listDirectory tool: %w", err)
	}

	// Read file
	if err := s.mcpServer.RegisterTool("fsReadFile", "Read contents of a file",
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

			return CreateJSONResponse(response)
		}); err != nil {
		return fmt.Errorf("failed to register readFile tool: %w", err)
	}

	// Write file
	if err := s.mcpServer.RegisterTool("fsWriteFile", "Create or update a file",
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

				return CreateJSONResponse(response)
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

				return CreateJSONResponse(response)
			}
		}); err != nil {
		return fmt.Errorf("failed to register writeFile tool: %w", err)
	}

	// Delete file or directory
	if err := s.mcpServer.RegisterTool("fsDeleteFileOrDirectory", "Delete a file or directory",
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

				return CreateJSONResponse(response)
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

				return CreateJSONResponse(response)
			}

			return nil, fmt.Errorf("path %s does not exist", args.Path)
		}); err != nil {
		return fmt.Errorf("failed to register deleteFileOrDirectory tool: %w", err)
	}

	return nil
}
