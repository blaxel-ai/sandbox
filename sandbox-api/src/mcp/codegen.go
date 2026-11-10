package mcp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/blaxel-ai/sandbox-api/src/lib/codegen"
	jsoniter "github.com/json-iterator/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// Codegen tool input/output types

type EditFileInput struct {
	TargetFile   string `json:"targetFile" jsonschema:"The target file to modify. Always specify the target file as the first argument and use the relative path in the workspace of the file to edit."`
	Instructions string `json:"instructions" jsonschema:"A single sentence instruction describing what you are going to do for the sketched edit. This is used to assist the less intelligent model in applying the edit. Please use the first person to describe what you are going to do. Dont repeat what you have said previously in normal messages. And use it to disambiguate uncertainty in the edit."`
	CodeEdit     string `json:"codeEdit" jsonschema:"Specify ONLY the precise lines of code that you wish to edit. NEVER specify or write out unchanged code. Instead, represent all unchanged code using the comment of the language you're editing in - example: // ... existing code ..."`
}

type FileSearchInput struct {
	Query     string  `json:"query" jsonschema:"Fuzzy filename to search for"`
	Directory *string `json:"directory,omitempty" jsonschema:"Optional directory to search in (relative to workspace root). If not provided, searches from workspace root."`
}

type CodebaseSearchInput struct {
	Query             string   `json:"query" jsonschema:"The search query to find relevant code"`
	TargetDirectories []string `json:"targetDirectories,omitempty" jsonschema:"Glob patterns for directories to search over"`
}

type GrepSearchInput struct {
	Query          string  `json:"query" jsonschema:"The regex pattern to search for"`
	CaseSensitive  *bool   `json:"caseSensitive,omitempty" jsonschema:"Whether the search should be case sensitive"`
	IncludePattern *string `json:"includePattern,omitempty" jsonschema:"Glob pattern for files to include (e.g. '*.ts' for TypeScript files)"`
	ExcludePattern *string `json:"excludePattern,omitempty" jsonschema:"Glob pattern for files to exclude"`
}

type ReadFileRangeInput struct {
	TargetFile                 string `json:"targetFile" jsonschema:"The path of the file to read"`
	StartLineOneIndexed        int    `json:"startLineOneIndexed" jsonschema:"The one-indexed line number to start reading from (inclusive)"`
	EndLineOneIndexedInclusive int    `json:"endLineOneIndexedInclusive" jsonschema:"The one-indexed line number to end reading at (inclusive)"`
}

type ReapplyInput struct {
	TargetFile string `json:"targetFile" jsonschema:"The relative path to the file to reapply the last edit to"`
}

type ListDirInput struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath" jsonschema:"Path to list contents of, relative to the workspace root"`
}

type ParallelApplyInput struct {
	EditPlan    string       `json:"editPlan" jsonschema:"A detailed description of the parallel edits to be applied"`
	EditRegions []EditRegion `json:"editRegions" jsonschema:"List of files and regions to edit"`
}

type EditRegion struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath" jsonschema:"The path to the file to edit"`
	StartLine             *int   `json:"startLine,omitempty" jsonschema:"The start line of the region to edit. 1-indexed and inclusive"`
	EndLine               *int   `json:"endLine,omitempty" jsonschema:"The end line of the region to edit. 1-indexed and inclusive"`
}

// Output types - specific output types for each codegen tool

// EditFileOutput is returned by codegenEditFile
type EditFileOutput struct {
	Success        bool   `json:"success"`
	Message        string `json:"message,omitempty"`
	FilePath       string `json:"filePath,omitempty"`
	ChangesApplied string `json:"changesApplied,omitempty"`
}

// FileSearchOutput is returned by codegenFileSearch
type FileSearchOutput struct {
	Success bool     `json:"success"`
	Matches []string `json:"matches"`
	Query   string   `json:"query"`
}

// CodebaseSearchOutput is returned by codegenCodebaseSearch
type CodebaseSearchOutput struct {
	Success bool     `json:"success"`
	Results []string `json:"results"`
	Query   string   `json:"query"`
}

// GrepSearchOutput is returned by codegenGrepSearch
type GrepSearchOutput struct {
	Success bool   `json:"success"`
	Results string `json:"results"`
}

// ReadFileRangeOutput is returned by codegenReadFileRange
type ReadFileRangeOutput struct {
	Success bool   `json:"success"`
	Content string `json:"content"`
	File    string `json:"file"`
}

// ReapplyOutput is returned by codegenReapply
type ReapplyOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// ListDirOutput is returned by codegenListDir
type ListDirOutput struct {
	Success   bool                  `json:"success"`
	Directory *filesystem.Directory `json:"directory,omitempty"`
}

// ParallelApplyOutput is returned by codegenParallelApply
type ParallelApplyOutput struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
}

// registerCodegenTools registers all codegen-related tools
func (s *Server) registerCodegenTools() error {
	// Edit file tool - the most critical tool for coding agents
	// Register if any fastapply provider is enabled
	if codegen.IsEnabled() {
		mcp.AddTool(s.mcpServer, &mcp.Tool{
			Name:        "codegenEditFile",
			Description: "Use this tool to propose an edit to an existing file or create a new file. This will be read by a less intelligent model, which will quickly apply the edit. You should make it clear what the edit is, while also minimizing the unchanged code you write.",
		}, LogToolCall("codegenEditFile", s.handleEditFile))
	}

	// File search tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenFileSearch",
		Description: "Fast file search based on fuzzy matching against file path. Use if you know part of the file path but don't know where it's located exactly. Optionally specify a directory to narrow the search scope.",
	}, LogToolCall("codegenFileSearch", s.handleFileSearch))

	// Codebase search tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenCodebaseSearch",
		Description: "Find snippets of code from the codebase most relevant to the search query. This is a semantic search tool.",
	}, LogToolCall("codegenCodebaseSearch", s.handleCodebaseSearch))

	// Grep search tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenGrepSearch",
		Description: "Fast, exact regex searches over text files using the ripgrep engine. Best for finding exact text matches or regex patterns.",
	}, LogToolCall("codegenGrepSearch", s.handleGrepSearch))

	// Read file range tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenReadFileRange",
		Description: "Read the contents of a file within a specific line range. Can view at most 250 lines at a time.",
	}, LogToolCall("codegenReadFileRange", s.handleReadFileRange))

	// Reapply tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenReapply",
		Description: "Calls a smarter model to apply the last edit to the specified file. Use this tool immediately after a failed codegenEditFile attempt.",
	}, LogToolCall("codegenReapply", s.handleReapply))

	// List directory tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenListDir",
		Description: "List the contents of a directory. The quick tool to use for discovery, before using more targeted tools like semantic search or file reading.",
	}, LogToolCall("codegenListDir", s.handleListDir))

	// Parallel apply tool
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "codegenParallelApply",
		Description: "When there are multiple locations that can be edited in parallel, with a similar type of edit, use this tool to sketch out a plan for the edits.",
	}, LogToolCall("codegenParallelApply", s.handleParallelApply))

	return nil
}

// handleEditFile implements the edit_file tool functionality
func (s *Server) handleEditFile(ctx context.Context, req *mcp.CallToolRequest, args EditFileInput) (*mcp.CallToolResult, EditFileOutput, error) {
	// Create a FastApply client using the factory
	client, err := codegen.NewClient()
	if err != nil {
		return nil, EditFileOutput{}, fmt.Errorf("failed to create FastApply client: %w", err)
	}

	// Check if file exists
	fileExists, err := s.handlers.FileSystem.FileExists(args.TargetFile)
	if err != nil {
		return nil, EditFileOutput{}, fmt.Errorf("failed to check if file exists: %w", err)
	}

	var originalContent string
	if fileExists {
		file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
		if err != nil {
			return nil, EditFileOutput{}, fmt.Errorf("failed to read file: %w", err)
		}
		originalContent = string(file.Content)
	}

	// Use "auto" model by default
	model := "auto"
	logrus.Infof("Using %s API to apply code edit with model %s", client.ProviderName(), model)
	updatedContent, err := client.ApplyCodeEdit(originalContent, args.CodeEdit, model)
	if err != nil {
		return nil, EditFileOutput{}, fmt.Errorf("failed to apply edit: %w", err)
	}

	err = s.handlers.FileSystem.WriteFile(args.TargetFile, []byte(updatedContent), 0644)
	if err != nil {
		return nil, EditFileOutput{}, fmt.Errorf("failed to write file: %w", err)
	}

	return nil, EditFileOutput{
		Success:        true,
		Message:        fmt.Sprintf("Successfully applied edit to %s: %s", args.TargetFile, args.Instructions),
		FilePath:       args.TargetFile,
		ChangesApplied: args.CodeEdit,
	}, nil
}

// handleFileSearch implements fuzzy file search functionality
func (s *Server) handleFileSearch(ctx context.Context, req *mcp.CallToolRequest, args FileSearchInput) (*mcp.CallToolResult, FileSearchOutput, error) {
	matches := []string{}
	query := strings.ToLower(args.Query)

	// Get the working directory from the filesystem handler
	workingDir, err := s.handlers.FileSystem.GetWorkingDirectory()
	if err != nil {
		return nil, FileSearchOutput{}, fmt.Errorf("failed to get working directory: %w", err)
	}

	// Determine the search directory
	searchDir := workingDir
	if args.Directory != nil && *args.Directory != "" {
		// Resolve the directory relative to the working directory
		searchDir = filepath.Join(workingDir, *args.Directory)

		// Ensure the path is within the working directory (prevent path traversal)
		cleanSearchDir := filepath.Clean(searchDir)
		cleanWorkingDir := filepath.Clean(workingDir)
		if !strings.HasPrefix(cleanSearchDir, cleanWorkingDir) {
			return nil, FileSearchOutput{}, fmt.Errorf("directory must be within workspace")
		}

		// Check if the directory exists
		dirExists, err := s.handlers.FileSystem.DirectoryExists(*args.Directory)
		if err != nil {
			return nil, FileSearchOutput{}, fmt.Errorf("failed to check directory: %w", err)
		}
		if !dirExists {
			return nil, FileSearchOutput{}, fmt.Errorf("directory not found: %s", *args.Directory)
		}

		searchDir = cleanSearchDir
	}

	err = filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if !info.IsDir() {
			filename := strings.ToLower(info.Name())
			if s.fuzzyMatch(filename, query) {
				matches = append(matches, path)
				if len(matches) >= 10 {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, FileSearchOutput{}, fmt.Errorf("failed to search files: %w", err)
	}

	return nil, FileSearchOutput{
		Success: true,
		Matches: matches,
		Query:   args.Query,
	}, nil
}

// handleCodebaseSearch implements semantic search across the codebase
func (s *Server) handleCodebaseSearch(ctx context.Context, req *mcp.CallToolRequest, args CodebaseSearchInput) (*mcp.CallToolResult, CodebaseSearchOutput, error) {
	// Simplified implementation - in production, use proper semantic search
	results := []string{}
	return nil, CodebaseSearchOutput{
		Success: true,
		Results: results,
		Query:   args.Query,
	}, nil
}

// handleGrepSearch implements regex search functionality
func (s *Server) handleGrepSearch(ctx context.Context, req *mcp.CallToolRequest, args GrepSearchInput) (*mcp.CallToolResult, GrepSearchOutput, error) {
	cmd := exec.Command("rg", "--json")

	caseSensitive := false
	if args.CaseSensitive != nil {
		caseSensitive = *args.CaseSensitive
	}

	if !caseSensitive {
		cmd.Args = append(cmd.Args, "-i")
	}

	if args.IncludePattern != nil && *args.IncludePattern != "" {
		cmd.Args = append(cmd.Args, "-g", *args.IncludePattern)
	}

	if args.ExcludePattern != nil && *args.ExcludePattern != "" {
		cmd.Args = append(cmd.Args, "-g", "!"+*args.ExcludePattern)
	}

	cmd.Args = append(cmd.Args, args.Query)
	output, err := cmd.Output()

	if err != nil {
		return nil, GrepSearchOutput{}, fmt.Errorf("grep search failed: %w", err)
	}

	return nil, GrepSearchOutput{
		Success: true,
		Results: string(output),
	}, nil
}

// handleReadFileRange reads specific lines from a file
func (s *Server) handleReadFileRange(ctx context.Context, req *mcp.CallToolRequest, args ReadFileRangeInput) (*mcp.CallToolResult, ReadFileRangeOutput, error) {
	file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
	if err != nil {
		return nil, ReadFileRangeOutput{}, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(file.Content), "\n")
	if args.StartLineOneIndexed < 1 || args.EndLineOneIndexedInclusive > len(lines) {
		return nil, ReadFileRangeOutput{}, fmt.Errorf("invalid line range")
	}

	selectedLines := lines[args.StartLineOneIndexed-1 : args.EndLineOneIndexedInclusive]
	content := strings.Join(selectedLines, "\n")

	return nil, ReadFileRangeOutput{
		Success: true,
		Content: content,
		File:    args.TargetFile,
	}, nil
}

// handleReapply reapplies the last edit
func (s *Server) handleReapply(ctx context.Context, req *mcp.CallToolRequest, args ReapplyInput) (*mcp.CallToolResult, ReapplyOutput, error) {
	return nil, ReapplyOutput{
		Success: false,
		Message: "Reapply functionality not yet implemented",
	}, nil
}

// handleListDir lists directory contents
func (s *Server) handleListDir(ctx context.Context, req *mcp.CallToolRequest, args ListDirInput) (*mcp.CallToolResult, ListDirOutput, error) {
	dir, err := s.handlers.FileSystem.ListDirectory(args.RelativeWorkspacePath)
	if err != nil {
		return nil, ListDirOutput{}, fmt.Errorf("failed to list directory: %w", err)
	}

	return nil, ListDirOutput{
		Success:   true,
		Directory: dir,
	}, nil
}

// handleParallelApply handles parallel edits
func (s *Server) handleParallelApply(ctx context.Context, req *mcp.CallToolRequest, args ParallelApplyInput) (*mcp.CallToolResult, ParallelApplyOutput, error) {
	return nil, ParallelApplyOutput{
		Success: false,
		Message: "Parallel apply functionality not yet implemented",
	}, nil
}

// fuzzyMatch checks if query characters appear in order in the text
func (s *Server) fuzzyMatch(text, query string) bool {
	textIdx := 0
	for _, char := range query {
		found := false
		for textIdx < len(text) {
			if rune(text[textIdx]) == char {
				found = true
				textIdx++
				break
			}
			textIdx++
		}
		if !found {
			return false
		}
	}
	return true
}

// CreateJSONResponse is a helper to create JSON responses (kept for compatibility)
func CreateJSONResponse(data interface{}) (*mcp.CallToolResult, error) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal JSON: %w", err)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: string(jsonData)},
		},
	}, nil
}
