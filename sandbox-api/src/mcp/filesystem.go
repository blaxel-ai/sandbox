package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Filesystem tool input/output types
type GetWorkingDirectoryInput struct{}

type GetWorkingDirectoryOutput struct {
	Path string `json:"path"`
}

type ListDirectoryInput struct {
	Path string `json:"path" jsonschema:"Path to the file or directory"`
}

type ListDirectoryOutput struct {
	Entries []interface{} `json:"entries"`
}

type ReadFileInput struct {
	Path string `json:"path" jsonschema:"Path to the file"`
}

type ReadFileOutput struct {
	Content interface{} `json:"content"`
}

type WriteFileInput struct {
	Path        string  `json:"path" jsonschema:"Path to the file or directory"`
	Content     *string `json:"content,omitempty" jsonschema:"Content to write to the file"`
	Permissions *string `json:"permissions,omitempty" jsonschema:"Permissions for the file or directory (octal string)"`
	IsDirectory *bool   `json:"isDirectory,omitempty" jsonschema:"Whether the path refers to a directory"`
}

type WriteFileOutput struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type DeleteFileInput struct {
	Path        string `json:"path" jsonschema:"Path to the file or directory"`
	IsDirectory *bool  `json:"isDirectory,omitempty" jsonschema:"Whether the path refers to a directory"`
	Recursive   *bool  `json:"recursive,omitempty" jsonschema:"Whether to perform the operation recursively"`
}

type DeleteFileOutput struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// registerFileSystemTools registers filesystem-related tools
func (s *Server) registerFileSystemTools() error {
	// Get working directory
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "fsGetWorkingDirectory",
		Description: "Get the current working directory",
	}, LogToolCall("fsGetWorkingDirectory", func(ctx context.Context, req *mcp.CallToolRequest, input GetWorkingDirectoryInput) (*mcp.CallToolResult, GetWorkingDirectoryOutput, error) {
		workingDir, err := s.handlers.FileSystem.GetWorkingDirectory()
		if err != nil {
			return nil, GetWorkingDirectoryOutput{}, fmt.Errorf("failed to get working directory: %w", err)
		}
		return nil, GetWorkingDirectoryOutput{Path: workingDir}, nil
	}))

	// List directory
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "fsListDirectory",
		Description: "List contents of a directory",
	}, LogToolCall("fsListDirectory", func(ctx context.Context, req *mcp.CallToolRequest, input ListDirectoryInput) (*mcp.CallToolResult, ListDirectoryOutput, error) {
		dir, err := s.handlers.FileSystem.ListDirectory(input.Path)
		if err != nil {
			return nil, ListDirectoryOutput{}, fmt.Errorf("failed to list directory: %w", err)
		}
		// Convert to interface{} slice
		entries := make([]interface{}, 0)
		entries = append(entries, dir)
		return nil, ListDirectoryOutput{Entries: entries}, nil
	}))

	// Read file
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "fsReadFile",
		Description: "Read contents of a file",
	}, LogToolCall("fsReadFile", func(ctx context.Context, req *mcp.CallToolRequest, input ReadFileInput) (*mcp.CallToolResult, ReadFileOutput, error) {
		file, err := s.handlers.FileSystem.ReadFile(input.Path)
		if err != nil {
			return nil, ReadFileOutput{}, fmt.Errorf("failed to read file: %w", err)
		}
		return nil, ReadFileOutput{Content: file}, nil
	}))

	// Write file
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "fsWriteFile",
		Description: "Create or update a file",
	}, LogToolCall("fsWriteFile", func(ctx context.Context, req *mcp.CallToolRequest, input WriteFileInput) (*mcp.CallToolResult, WriteFileOutput, error) {
		// Parse permissions or use default
		var permissions os.FileMode = 0644
		if input.Permissions != nil && *input.Permissions != "" {
			permInt, err := strconv.ParseUint(*input.Permissions, 8, 32)
			if err == nil {
				permissions = os.FileMode(permInt)
			}
		}

		isDirectory := false
		if input.IsDirectory != nil {
			isDirectory = *input.IsDirectory
		}

		if isDirectory {
			// Create directory
			err := s.handlers.FileSystem.CreateDirectory(input.Path, permissions)
			if err != nil {
				return nil, WriteFileOutput{}, fmt.Errorf("failed to create directory: %w", err)
			}
			return nil, WriteFileOutput{
				Path:    input.Path,
				Message: "Directory created successfully",
			}, nil
		} else {
			// Create or update file
			content := ""
			if input.Content != nil {
				content = *input.Content
			}
			err := s.handlers.FileSystem.WriteFile(input.Path, []byte(content), permissions)
			if err != nil {
				return nil, WriteFileOutput{}, fmt.Errorf("failed to write file: %w", err)
			}
			return nil, WriteFileOutput{
				Path:    input.Path,
				Message: "File created/updated successfully",
			}, nil
		}
	}))

	// Delete file or directory
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "fsDeleteFileOrDirectory",
		Description: "Delete a file or directory",
	}, LogToolCall("fsDeleteFileOrDirectory", func(ctx context.Context, req *mcp.CallToolRequest, input DeleteFileInput) (*mcp.CallToolResult, DeleteFileOutput, error) {
		// Check if it's a directory (or use the hint from input)
		isDir := false
		if input.IsDirectory != nil {
			isDir = *input.IsDirectory
		} else {
			// Auto-detect if not specified
			var err error
			isDir, err = s.handlers.FileSystem.DirectoryExists(input.Path)
			if err != nil {
				return nil, DeleteFileOutput{}, fmt.Errorf("failed to check if path is a directory: %w", err)
			}
		}

		recursive := false
		if input.Recursive != nil {
			recursive = *input.Recursive
		}

		if isDir {
			// Delete directory
			err := s.handlers.FileSystem.DeleteDirectory(input.Path, recursive)
			if err != nil {
				return nil, DeleteFileOutput{}, fmt.Errorf("failed to delete directory: %w", err)
			}
			return nil, DeleteFileOutput{
				Path:    input.Path,
				Message: "Directory deleted successfully",
			}, nil
		} else {
			// Delete file
			err := s.handlers.FileSystem.DeleteFile(input.Path)
			if err != nil {
				return nil, DeleteFileOutput{}, fmt.Errorf("failed to delete file: %w", err)
			}
			return nil, DeleteFileOutput{
				Path:    input.Path,
				Message: "File deleted successfully",
			}, nil
		}
	}))

	return nil
}
