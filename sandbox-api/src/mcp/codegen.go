package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/lib"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sirupsen/logrus"
)

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

// Output types
type CodegenOutput struct {
	Success bool        `json:"success"`
	Message string      `json:"message,omitempty"`
	Data    interface{} `json:"data,omitempty"`
}

// registerCodegenTools registers all codegen-related tools
func (s *Server) registerCodegenTools() error {
	// Edit file tool - the most critical tool for coding agents
	if os.Getenv("MORPH_API_KEY") != "" {
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
func (s *Server) handleEditFile(ctx context.Context, req *mcp.CallToolRequest, args EditFileInput) (*mcp.CallToolResult, CodegenOutput, error) {
	morphAPIKey := os.Getenv("MORPH_API_KEY")
	if morphAPIKey == "" {
		return nil, CodegenOutput{}, fmt.Errorf("MORPH_API_KEY not set")
	}

	// Check if file exists
	fileExists, err := s.handlers.FileSystem.FileExists(args.TargetFile)
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to check if file exists: %w", err)
	}

	var originalContent string
	if fileExists {
		file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
		if err != nil {
			return nil, CodegenOutput{}, fmt.Errorf("failed to read file: %w", err)
		}
		originalContent = string(file.Content)
	}

	model := os.Getenv("MORPH_MODEL")
	if model == "" {
		model = "morph-v2"
	}

	logrus.Infof("Using MorphAPI to apply code using model: %s", model)
	morphClient := lib.NewMorphClient(model, morphAPIKey)
	updatedContent, err := morphClient.ApplyCodeEdit(originalContent, args.CodeEdit)
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to apply edit: %w", err)
	}

	err = s.handlers.FileSystem.WriteFile(args.TargetFile, []byte(updatedContent), 0644)
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to write file: %w", err)
	}

	return nil, CodegenOutput{
		Success: true,
		Message: fmt.Sprintf("Successfully applied edit to %s: %s", args.TargetFile, args.Instructions),
		Data: map[string]interface{}{
			"file_path":       args.TargetFile,
			"changes_applied": args.CodeEdit,
		},
	}, nil
}

// handleFileSearch implements fuzzy file search functionality
func (s *Server) handleFileSearch(ctx context.Context, req *mcp.CallToolRequest, args FileSearchInput) (*mcp.CallToolResult, CodegenOutput, error) {
	var matches []string
	query := strings.ToLower(args.Query)

	// Get the working directory from the filesystem handler
	workingDir, err := s.handlers.FileSystem.GetWorkingDirectory()
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to get working directory: %w", err)
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
			return nil, CodegenOutput{}, fmt.Errorf("directory must be within workspace")
		}

		// Check if the directory exists
		dirExists, err := s.handlers.FileSystem.DirectoryExists(*args.Directory)
		if err != nil {
			return nil, CodegenOutput{}, fmt.Errorf("failed to check directory: %w", err)
		}
		if !dirExists {
			return nil, CodegenOutput{}, fmt.Errorf("directory not found: %s", *args.Directory)
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
		return nil, CodegenOutput{}, fmt.Errorf("failed to search files: %w", err)
	}

	return nil, CodegenOutput{
		Success: true,
		Data:    map[string]interface{}{"matches": matches, "query": args.Query},
	}, nil
}

// handleCodebaseSearch implements semantic search across the codebase
func (s *Server) handleCodebaseSearch(ctx context.Context, req *mcp.CallToolRequest, args CodebaseSearchInput) (*mcp.CallToolResult, CodegenOutput, error) {
	// Simplified implementation - in production, use proper semantic search
	results := []string{}
	return nil, CodegenOutput{
		Success: true,
		Data:    map[string]interface{}{"results": results, "query": args.Query},
	}, nil
}

// handleGrepSearch implements regex search functionality
func (s *Server) handleGrepSearch(ctx context.Context, req *mcp.CallToolRequest, args GrepSearchInput) (*mcp.CallToolResult, CodegenOutput, error) {
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
		return nil, CodegenOutput{}, fmt.Errorf("grep search failed: %w", err)
	}

	return nil, CodegenOutput{
		Success: true,
		Data:    map[string]interface{}{"results": string(output)},
	}, nil
}

// handleReadFileRange reads specific lines from a file
func (s *Server) handleReadFileRange(ctx context.Context, req *mcp.CallToolRequest, args ReadFileRangeInput) (*mcp.CallToolResult, CodegenOutput, error) {
	file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(file.Content), "\n")
	if args.StartLineOneIndexed < 1 || args.EndLineOneIndexedInclusive > len(lines) {
		return nil, CodegenOutput{}, fmt.Errorf("invalid line range")
	}

	selectedLines := lines[args.StartLineOneIndexed-1 : args.EndLineOneIndexedInclusive]
	content := strings.Join(selectedLines, "\n")

	return nil, CodegenOutput{
		Success: true,
		Data:    map[string]interface{}{"content": content, "file": args.TargetFile},
	}, nil
}

// handleReapply reapplies the last edit
func (s *Server) handleReapply(ctx context.Context, req *mcp.CallToolRequest, args ReapplyInput) (*mcp.CallToolResult, CodegenOutput, error) {
	return nil, CodegenOutput{
		Success: false,
		Message: "Reapply functionality not yet implemented",
	}, nil
}

// handleListDir lists directory contents
func (s *Server) handleListDir(ctx context.Context, req *mcp.CallToolRequest, args ListDirInput) (*mcp.CallToolResult, CodegenOutput, error) {
	dir, err := s.handlers.FileSystem.ListDirectory(args.RelativeWorkspacePath)
	if err != nil {
		return nil, CodegenOutput{}, fmt.Errorf("failed to list directory: %w", err)
	}

	return nil, CodegenOutput{
		Success: true,
		Data:    dir,
	}, nil
}

// handleParallelApply handles parallel edits
func (s *Server) handleParallelApply(ctx context.Context, req *mcp.CallToolRequest, args ParallelApplyInput) (*mcp.CallToolResult, CodegenOutput, error) {
	return nil, CodegenOutput{
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
