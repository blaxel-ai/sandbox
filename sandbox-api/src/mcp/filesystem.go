package mcp

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
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
	Directory *filesystem.Directory `json:"directory"`
}

type ReadFileInput struct {
	Path string `json:"path" jsonschema:"Path to the file"`
}

type ReadFileOutput struct {
	Path         string `json:"path"`
	Name         string `json:"name"`
	Permissions  string `json:"permissions"`
	Size         int64  `json:"size"`
	LastModified string `json:"lastModified"`
	Owner        string `json:"owner"`
	Group        string `json:"group"`
	Content      string `json:"content"`
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

type FsEditFileInput struct {
	Path       string `json:"path" jsonschema:"Path to the file to edit"`
	OldString  string `json:"oldString" jsonschema:"Exact text to replace. Must be non-empty. Must appear exactly once in the file unless replaceAll is true."`
	NewString  string `json:"newString" jsonschema:"Replacement text. Must differ from oldString. Use an empty string to delete the matched text."`
	ReplaceAll *bool  `json:"replaceAll,omitempty" jsonschema:"Replace every occurrence of oldString (default: false)"`
}

type FsEditFileOutput struct {
	Path                string `json:"path"`
	OccurrencesReplaced int    `json:"occurrencesReplaced"`
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
		return nil, ListDirectoryOutput{Directory: dir}, nil
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
		return nil, ReadFileOutput{
			Path:         file.Path,
			Name:         file.Path, // Name will be derived from path
			Permissions:  fmt.Sprintf("%o", file.Permissions),
			Size:         file.Size,
			LastModified: file.LastModified.Format("2006-01-02T15:04:05Z07:00"),
			Owner:        file.Owner,
			Group:        file.Group,
			Content:      string(file.Content),
		}, nil
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

	// Edit file (deterministic search/replace)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "fsEditFile",
		Description: "Make a targeted string-replace edit to an existing file. " +
			"Prefer this over fsWriteFile for any change to a file that already exists. " +
			"oldString must appear exactly once unless replaceAll is true; include enough " +
			"surrounding context (whitespace, neighbouring lines) to make the match unambiguous.",
	}, LogToolCall("fsEditFile", func(ctx context.Context, req *mcp.CallToolRequest, input FsEditFileInput) (*mcp.CallToolResult, FsEditFileOutput, error) {
		if input.OldString == "" {
			return nil, FsEditFileOutput{}, fmt.Errorf("oldString must be non-empty")
		}
		if input.OldString == input.NewString {
			return nil, FsEditFileOutput{}, fmt.Errorf("oldString and newString are identical")
		}
		replaceAll := input.ReplaceAll != nil && *input.ReplaceAll

		file, err := s.handlers.FileSystem.ReadFile(input.Path)
		if err != nil {
			return nil, FsEditFileOutput{}, fmt.Errorf("failed to read %s: %w", input.Path, err)
		}

		content := string(file.Content)
		occurrences := strings.Count(content, input.OldString)
		if occurrences == 0 {
			return nil, FsEditFileOutput{}, fmt.Errorf(
				"oldString not found in %s. Re-read the file and retry — its contents may have changed since you last read it",
				input.Path)
		}
		if occurrences > 1 && !replaceAll {
			return nil, FsEditFileOutput{}, fmt.Errorf(
				"oldString matches %d locations in %s; add more surrounding context to make the match unique, or set replaceAll=true",
				occurrences, input.Path)
		}

		var updated string
		if replaceAll {
			updated = strings.ReplaceAll(content, input.OldString, input.NewString)
		} else {
			updated = strings.Replace(content, input.OldString, input.NewString, 1)
		}

		if err := s.handlers.FileSystem.WriteFile(input.Path, []byte(updated), file.Permissions); err != nil {
			return nil, FsEditFileOutput{}, fmt.Errorf("failed to write %s: %w", input.Path, err)
		}

		return nil, FsEditFileOutput{
			Path:                file.Path,
			OccurrencesReplaced: occurrences,
		}, nil
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
