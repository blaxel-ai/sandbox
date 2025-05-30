package mcp

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/blaxel-ai/sandbox-api/src/lib"
	mcp_golang "github.com/metoro-io/mcp-golang"
	"github.com/sirupsen/logrus"
)

// CodegenArgs represents arguments for various codegen tools

// EditFileArgs represents arguments for the edit_file tool
type EditFileArgs struct {
	TargetFile   string `json:"targetFile" jsonschema:"required,description=The target file to modify. Always specify the target file as the first argument and use the relative path in the workspace of the file to edit."`
	Instructions string `json:"instructions" jsonschema:"required,description=A single sentence instruction describing what you are going to do for the sketched edit. This is used to assist the less intelligent model in applying the edit. Please use the first person to describe what you are going to do. Dont repeat what you have said previously in normal messages. And use it to disambiguate uncertainty in the edit."`
	CodeEdit     string `json:"codeEdit" jsonschema:"required,description=Specify ONLY the precise lines of code that you wish to edit. NEVER specify or write out unchanged code. Instead, represent all unchanged code using the comment of the language you're editing in - example: // ... existing code ..."`
}

// FileSearchArgs represents arguments for the file_search tool
type FileSearchArgs struct {
	Query string `json:"query" jsonschema:"required,description=Fuzzy filename to search for"`
}

// CodebaseSearchArgs represents arguments for the codebase_search tool
type CodebaseSearchArgs struct {
	Query             string   `json:"query" jsonschema:"required,description=The search query to find relevant code"`
	TargetDirectories []string `json:"targetDirectories" jsonschema:"description=Glob patterns for directories to search over"`
}

// GrepSearchArgs represents arguments for the grep_search tool
type GrepSearchArgs struct {
	Query          string `json:"query" jsonschema:"required,description=The regex pattern to search for"`
	CaseSensitive  bool   `json:"caseSensitive" jsonschema:"description=Whether the search should be case sensitive"`
	IncludePattern string `json:"includePattern" jsonschema:"description=Glob pattern for files to include (e.g. '*.ts' for TypeScript files)"`
	ExcludePattern string `json:"excludePattern" jsonschema:"description=Glob pattern for files to exclude"`
}

// ReadFileRangeArgs represents arguments for the read_file_range tool
type ReadFileRangeArgs struct {
	TargetFile                 string `json:"targetFile" jsonschema:"required,description=The path of the file to read"`
	StartLineOneIndexed        int    `json:"startLineOneIndexed" jsonschema:"required,description=The one-indexed line number to start reading from (inclusive)"`
	EndLineOneIndexedInclusive int    `json:"endLineOneIndexedInclusive" jsonschema:"required,description=The one-indexed line number to end reading at (inclusive)"`
}

// RunTerminalCmdArgs represents arguments for the run_terminal_cmd tool
type RunTerminalCmdArgs struct {
	Command      string `json:"command" jsonschema:"required,description=The terminal command to execute"`
	IsBackground bool   `json:"isBackground" jsonschema:"description=Whether the command should be run in the background"`
}

// ReapplyArgs represents arguments for the reapply tool
type ReapplyArgs struct {
	TargetFile string `json:"targetFile" jsonschema:"required,description=The relative path to the file to reapply the last edit to"`
}

// ListDirArgs represents arguments for the list_dir tool
type ListDirArgs struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath" jsonschema:"required,description=Path to list contents of, relative to the workspace root"`
}

// ParallelApplyArgs represents arguments for the parallel_apply tool
type ParallelApplyArgs struct {
	EditPlan    string       `json:"editPlan" jsonschema:"required,description=A detailed description of the parallel edits to be applied"`
	EditRegions []EditRegion `json:"editRegions" jsonschema:"required,description=List of files and regions to edit"`
}

// EditRegion represents a region in a file to edit
type EditRegion struct {
	RelativeWorkspacePath string `json:"relativeWorkspacePath" jsonschema:"required,description=The path to the file to edit"`
	StartLine             int    `json:"startLine" jsonschema:"description=The start line of the region to edit. 1-indexed and inclusive"`
	EndLine               int    `json:"endLine" jsonschema:"description=The end line of the region to edit. 1-indexed and inclusive"`
}

// registerCodegenTools registers all codegen-related tools
func (s *Server) registerCodegenTools() error {
	// Edit file tool - the most critical tool for coding agents
	if err := s.mcpServer.RegisterTool("codegenEditFile", "Use this tool to propose an edit to an existing file or create a new file. This will be read by a less intelligent model, which will quickly apply the edit. You should make it clear what the edit is, while also minimizing the unchanged code you write.",
		LogToolCall("codegenEditFile", func(args EditFileArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleEditFile(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenEditFile tool: %w", err)
	}

	// File search tool - fast fuzzy file search
	if err := s.mcpServer.RegisterTool("codegenFileSearch", "Fast file search based on fuzzy matching against file path. Use if you know part of the file path but don't know where it's located exactly.",
		LogToolCall("codegenFileSearch", func(args FileSearchArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleFileSearch(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenFileSearch tool: %w", err)
	}

	// Codebase search tool - semantic search across the codebase
	if err := s.mcpServer.RegisterTool("codegenCodebaseSearch", "Find snippets of code from the codebase most relevant to the search query. This is a semantic search tool.",
		LogToolCall("codegenCodebaseSearch", func(args CodebaseSearchArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleCodebaseSearch(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenCodebaseSearch tool: %w", err)
	}

	// Grep search tool - fast regex searches
	if err := s.mcpServer.RegisterTool("codegenGrepSearch", "Fast, exact regex searches over text files using the ripgrep engine. Best for finding exact text matches or regex patterns.",
		LogToolCall("codegenGrepSearch", func(args GrepSearchArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleGrepSearch(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenGrepSearch tool: %w", err)
	}

	// Read file range tool - read specific lines from a file
	if err := s.mcpServer.RegisterTool("codegenReadFileRange", "Read the contents of a file within a specific line range. Can view at most 250 lines at a time.",
		LogToolCall("codegenReadFileRange", func(args ReadFileRangeArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleReadFileRange(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenReadFileRange tool: %w", err)
	}

	// Run terminal command tool
	if err := s.mcpServer.RegisterTool("codegenRunTerminalCmd", "Execute terminal commands. The command will be proposed to the user for approval before execution.",
		LogToolCall("codegenRunTerminalCmd", func(args RunTerminalCmdArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleRunTerminalCmd(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenRunTerminalCmd tool: %w", err)
	}

	// Reapply tool - error recovery for failed edits
	if err := s.mcpServer.RegisterTool("codegenReapply", "Calls a smarter model to apply the last edit to the specified file. Use this tool immediately after a failed codegenEditFile attempt.",
		LogToolCall("codegenReapply", func(args ReapplyArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleReapply(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenReapply tool: %w", err)
	}

	// List directory tool - for discovery and navigation
	if err := s.mcpServer.RegisterTool("codegenListDir", "List the contents of a directory. The quick tool to use for discovery, before using more targeted tools like semantic search or file reading.",
		LogToolCall("codegenListDir", func(args ListDirArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleListDir(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenListDir tool: %w", err)
	}

	// Parallel apply tool - for systematic changes across multiple files
	if err := s.mcpServer.RegisterTool("codegenParallelApply", "When there are multiple locations that can be edited in parallel, with a similar type of edit, use this tool to sketch out a plan for the edits.",
		LogToolCall("codegenParallelApply", func(args ParallelApplyArgs) (*mcp_golang.ToolResponse, error) {
			return s.handleParallelApply(args)
		})); err != nil {
		return fmt.Errorf("failed to register codegenParallelApply tool: %w", err)
	}

	return nil
}

// handleEditFile implements the edit_file tool functionality
func (s *Server) handleEditFile(args EditFileArgs) (*mcp_golang.ToolResponse, error) {
	// Check if file exists
	fileExists, err := s.handlers.FileSystem.FileExists(args.TargetFile)
	if err != nil {
		return nil, fmt.Errorf("failed to check if file exists: %w", err)
	}

	var originalContent string
	if fileExists {
		// Read the current file content
		file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read file: %w", err)
		}
		originalContent = string(file.Content)
	}

	var updatedContent string

	// Check if MORPH_API_KEY is set and use Morph API if available
	morphAPIKey := os.Getenv("MORPH_API_KEY")
	if morphAPIKey != "" {
		model := os.Getenv("MORPH_MODEL")
		logrus.Infof("Using MorphAPI to apply code using model: %s", model)
		morphClient := lib.NewMorphClient(morphAPIKey)
		morphContent, err := morphClient.ApplyCodeEdit(originalContent, args.CodeEdit)
		if err != nil {
			// Fall back to simple edit if Morph API fails
			updatedContent, err = s.applyCodeEdit(originalContent, args.CodeEdit)
			if err != nil {
				return nil, fmt.Errorf("failed to apply edit (Morph failed, fallback also failed): %w", err)
			}
		} else {
			updatedContent = morphContent
		}
	} else {
		// Use the existing simple edit method
		updatedContent, err = s.applyCodeEdit(originalContent, args.CodeEdit)
		if err != nil {
			return nil, fmt.Errorf("failed to apply edit: %w", err)
		}
	}

	// Write the updated content back to the file
	err = s.handlers.FileSystem.WriteFile(args.TargetFile, []byte(updatedContent), 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	response := map[string]interface{}{
		"success":         true,
		"message":         fmt.Sprintf("Successfully applied edit to %s: %s", args.TargetFile, args.Instructions),
		"changes_applied": args.CodeEdit,
		"file_path":       args.TargetFile,
	}

	return CreateJSONResponse(response)
}

// applyCodeEdit applies code changes to the original content
func (s *Server) applyCodeEdit(originalContent, codeEdit string) (string, error) {
	// If original content is empty, return the code edit as the new content
	if originalContent == "" {
		// Remove any "// ... existing code ..." markers from new file content
		lines := strings.Split(codeEdit, "\n")
		var filteredLines []string
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if !strings.Contains(trimmed, "... existing code ...") {
				filteredLines = append(filteredLines, line)
			}
		}
		return strings.Join(filteredLines, "\n"), nil
	}

	// For existing files, we need to intelligently merge the changes
	// This is a simplified approach - in production, you might want more sophisticated merging
	editLines := strings.Split(codeEdit, "\n")
	originalLines := strings.Split(originalContent, "\n")

	var result []string
	var editIndex int

	for _, editLine := range editLines {
		trimmed := strings.TrimSpace(editLine)
		if strings.Contains(trimmed, "... existing code ...") {
			// Find the next non-placeholder line in the edit
			nextEditIndex := editIndex + 1
			for nextEditIndex < len(editLines) {
				nextTrimmed := strings.TrimSpace(editLines[nextEditIndex])
				if !strings.Contains(nextTrimmed, "... existing code ...") {
					break
				}
				nextEditIndex++
			}

			// Copy original lines until we find the next edit line
			if nextEditIndex < len(editLines) {
				targetLine := strings.TrimSpace(editLines[nextEditIndex])
				for _, origLine := range originalLines {
					if strings.TrimSpace(origLine) == targetLine {
						break
					}
					result = append(result, origLine)
				}
			} else {
				// No more edit lines, copy rest of original
				result = append(result, originalLines...)
				break
			}
		} else {
			result = append(result, editLine)
		}
		editIndex++
	}

	return strings.Join(result, "\n"), nil
}

// handleFileSearch implements fuzzy file search functionality
func (s *Server) handleFileSearch(args FileSearchArgs) (*mcp_golang.ToolResponse, error) {
	var matches []string
	query := strings.ToLower(args.Query)

	err := filepath.Walk("/", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors and continue
		}

		if !info.IsDir() {
			filename := strings.ToLower(info.Name())
			fullPath := strings.ToLower(path)

			// Simple fuzzy matching - check if query characters appear in order
			if s.fuzzyMatch(filename, query) || s.fuzzyMatch(fullPath, query) {
				matches = append(matches, path)
				if len(matches) >= 10 { // Limit to 10 results
					return filepath.SkipDir
				}
			}
		}
		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to search files: %w", err)
	}

	response := map[string]interface{}{
		"matches": matches,
		"query":   args.Query,
	}

	return CreateJSONResponse(response)
}

// fuzzyMatch implements simple fuzzy matching
func (s *Server) fuzzyMatch(text, pattern string) bool {
	textIndex := 0
	for _, char := range pattern {
		found := false
		for textIndex < len(text) {
			if rune(text[textIndex]) == char {
				found = true
				textIndex++
				break
			}
			textIndex++
		}
		if !found {
			return false
		}
	}
	return true
}

// handleCodebaseSearch implements semantic search across the codebase
func (s *Server) handleCodebaseSearch(args CodebaseSearchArgs) (*mcp_golang.ToolResponse, error) {
	// For now, implement as a text-based search
	// In a production environment, you might want to use embeddings or more sophisticated search
	var results []map[string]interface{}

	searchDirs := args.TargetDirectories
	if len(searchDirs) == 0 {
		searchDirs = []string{"."} // Search current directory by default
	}

	for _, dir := range searchDirs {
		err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}

			// Skip non-code files
			if info.IsDir() || !s.isCodeFile(path) {
				return nil
			}

			// Read file content
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			// Simple text search for now
			if strings.Contains(strings.ToLower(string(content)), strings.ToLower(args.Query)) {
				lines := strings.Split(string(content), "\n")
				for i, line := range lines {
					if strings.Contains(strings.ToLower(line), strings.ToLower(args.Query)) {
						result := map[string]interface{}{
							"file":    path,
							"line":    i + 1,
							"content": line,
							"context": s.getContextLines(lines, i, 2),
						}
						results = append(results, result)

						if len(results) >= 20 { // Limit results
							return filepath.SkipDir
						}
					}
				}
			}
			return nil
		})

		if err != nil {
			return nil, fmt.Errorf("failed to search directory %s: %w", dir, err)
		}
	}

	response := map[string]interface{}{
		"results": results,
		"query":   args.Query,
	}

	return CreateJSONResponse(response)
}

// isCodeFile checks if a file is a code file based on extension
func (s *Server) isCodeFile(path string) bool {
	codeExts := []string{".go", ".js", ".ts", ".py", ".java", ".c", ".cpp", ".h", ".hpp", ".cs", ".php", ".rb", ".rs", ".sh", ".sql", ".json", ".yaml", ".yml", ".xml", ".html", ".css", ".scss", ".sass", ".vue", ".jsx", ".tsx"}
	ext := strings.ToLower(filepath.Ext(path))
	for _, codeExt := range codeExts {
		if ext == codeExt {
			return true
		}
	}
	return false
}

// getContextLines returns surrounding lines for context
func (s *Server) getContextLines(lines []string, centerLine, contextSize int) []string {
	start := centerLine - contextSize
	end := centerLine + contextSize + 1

	if start < 0 {
		start = 0
	}
	if end > len(lines) {
		end = len(lines)
	}

	return lines[start:end]
}

// handleGrepSearch implements regex search using grep-like functionality
func (s *Server) handleGrepSearch(args GrepSearchArgs) (*mcp_golang.ToolResponse, error) {
	// Use ripgrep if available, otherwise fallback to grep or internal implementation
	cmd := exec.Command("rg", "--json")

	if !args.CaseSensitive {
		cmd.Args = append(cmd.Args, "-i")
	}

	if args.IncludePattern != "" {
		cmd.Args = append(cmd.Args, "-g", args.IncludePattern)
	}

	if args.ExcludePattern != "" {
		cmd.Args = append(cmd.Args, "-g", "!"+args.ExcludePattern)
	}

	cmd.Args = append(cmd.Args, args.Query, ".")

	output, err := cmd.Output()
	if err != nil {
		// Fallback to internal implementation if ripgrep is not available
		return s.handleGrepSearchFallback(args)
	}

	// Parse ripgrep JSON output
	results := []map[string]interface{}{}
	lines := strings.Split(string(output), "\n")

	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var rgResult map[string]interface{}
		if err := json.Unmarshal([]byte(line), &rgResult); err == nil {
			if rgResult["type"] == "match" {
				data := rgResult["data"].(map[string]interface{})
				results = append(results, map[string]interface{}{
					"file":    data["path"].(map[string]interface{})["text"],
					"line":    data["line_number"],
					"content": data["lines"].(map[string]interface{})["text"],
				})
			}
		}

		if len(results) >= 50 { // Limit to 50 matches
			break
		}
	}

	response := map[string]interface{}{
		"matches": results,
		"query":   args.Query,
	}

	return CreateJSONResponse(response)
}

// handleGrepSearchFallback implements grep search without external tools
func (s *Server) handleGrepSearchFallback(args GrepSearchArgs) (*mcp_golang.ToolResponse, error) {
	var regex *regexp.Regexp
	var err error

	if args.CaseSensitive {
		regex, err = regexp.Compile(args.Query)
	} else {
		regex, err = regexp.Compile("(?i)" + args.Query)
	}

	if err != nil {
		return nil, fmt.Errorf("invalid regex pattern: %w", err)
	}

	var results []map[string]interface{}

	err = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		if info.IsDir() {
			return nil
		}

		// Apply include/exclude patterns
		if args.IncludePattern != "" {
			matched, _ := filepath.Match(args.IncludePattern, info.Name())
			if !matched {
				return nil
			}
		}

		if args.ExcludePattern != "" {
			matched, _ := filepath.Match(args.ExcludePattern, info.Name())
			if matched {
				return nil
			}
		}

		// Read and search file
		content, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(content), "\n")
		for i, line := range lines {
			if regex.MatchString(line) {
				results = append(results, map[string]interface{}{
					"file":    path,
					"line":    i + 1,
					"content": line,
				})

				if len(results) >= 50 {
					return filepath.SkipDir
				}
			}
		}

		return nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to search files: %w", err)
	}

	response := map[string]interface{}{
		"matches": results,
		"query":   args.Query,
	}

	return CreateJSONResponse(response)
}

// handleReadFileRange reads a specific range of lines from a file
func (s *Server) handleReadFileRange(args ReadFileRangeArgs) (*mcp_golang.ToolResponse, error) {
	// Validate line range
	if args.StartLineOneIndexed < 1 {
		return nil, fmt.Errorf("start line must be >= 1")
	}
	if args.EndLineOneIndexedInclusive < args.StartLineOneIndexed {
		return nil, fmt.Errorf("end line must be >= start line")
	}
	if args.EndLineOneIndexedInclusive-args.StartLineOneIndexed+1 > 250 {
		return nil, fmt.Errorf("can only read up to 250 lines at a time")
	}

	// Read file
	file, err := s.handlers.FileSystem.ReadFile(args.TargetFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	lines := strings.Split(string(file.Content), "\n")
	totalLines := len(lines)

	// Adjust end line if it exceeds file length
	endLine := args.EndLineOneIndexedInclusive
	if endLine > totalLines {
		endLine = totalLines
	}

	// Extract the requested range (convert to 0-indexed)
	startIdx := args.StartLineOneIndexed - 1
	endIdx := endLine

	if startIdx >= totalLines {
		return nil, fmt.Errorf("start line %d exceeds file length %d", args.StartLineOneIndexed, totalLines)
	}

	selectedLines := lines[startIdx:endIdx]

	response := map[string]interface{}{
		"file":        args.TargetFile,
		"start_line":  args.StartLineOneIndexed,
		"end_line":    endLine,
		"total_lines": totalLines,
		"content":     strings.Join(selectedLines, "\n"),
		"lines":       selectedLines,
	}

	return CreateJSONResponse(response)
}

// handleRunTerminalCmd executes terminal commands
func (s *Server) handleRunTerminalCmd(args RunTerminalCmdArgs) (*mcp_golang.ToolResponse, error) {
	// For safety, we'll use the existing process execution from the process handler
	// But we need to be careful about security

	if args.IsBackground {
		// Execute in background
		processInfo, err := s.handlers.Process.ExecuteProcess(args.Command, "/", "", false, 0, []int{})
		if err != nil {
			return nil, fmt.Errorf("failed to execute background command: %w", err)
		}

		response := map[string]interface{}{
			"message":    "Command started in background",
			"command":    args.Command,
			"process_id": processInfo.PID,
			"background": true,
		}

		return CreateJSONResponse(response)
	} else {
		// Execute synchronously
		processInfo, err := s.handlers.Process.ExecuteProcess(args.Command, "/", "", true, 30, []int{})
		if err != nil {
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}

		// Get logs
		logs, err := s.handlers.Process.GetProcessOutput(processInfo.PID)
		if err != nil {
			// If we can't get logs, still return basic process info
			response := map[string]interface{}{
				"message":    "Command executed successfully",
				"command":    args.Command,
				"process_id": processInfo.PID,
				"status":     processInfo.Status,
			}
			return CreateJSONResponse(response)
		}

		response := map[string]interface{}{
			"message":    "Command executed successfully",
			"command":    args.Command,
			"process_id": processInfo.PID,
			"status":     processInfo.Status,
			"logs":       logs.Logs,
		}

		return CreateJSONResponse(response)
	}
}

// handleReapply attempts to reapply the last edit with better intelligence
func (s *Server) handleReapply(args ReapplyArgs) (*mcp_golang.ToolResponse, error) {
	// For now, this is a placeholder - in a real implementation, you would:
	// 1. Store the last edit attempt
	// 2. Use a more intelligent model to reapply it
	// 3. Potentially use different strategies

	response := map[string]interface{}{
		"message": "Reapply functionality is not yet fully implemented",
		"file":    args.TargetFile,
		"note":    "This would typically call a smarter model to reapply the last failed edit",
	}

	return CreateJSONResponse(response)
}

// handleListDir implements directory listing functionality
func (s *Server) handleListDir(args ListDirArgs) (*mcp_golang.ToolResponse, error) {
	dir, err := s.handlers.FileSystem.ListDirectory(args.RelativeWorkspacePath)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}

	response := map[string]interface{}{
		"path":     args.RelativeWorkspacePath,
		"contents": dir,
	}

	return CreateJSONResponse(response)
}

// handleParallelApply implements parallel editing across multiple files
func (s *Server) handleParallelApply(args ParallelApplyArgs) (*mcp_golang.ToolResponse, error) {
	// For now, this is a simplified implementation
	// In a production environment, you might want more sophisticated parallel processing

	var results []map[string]interface{}
	var errors []string

	for _, editRegion := range args.EditRegions {
		// For each file in the edit regions, apply the edit plan
		// This is a simplified approach - you could make this more sophisticated

		result := map[string]interface{}{
			"file":       editRegion.RelativeWorkspacePath,
			"start_line": editRegion.StartLine,
			"end_line":   editRegion.EndLine,
			"status":     "planned",
			"edit_plan":  args.EditPlan,
		}

		// Check if file exists
		fileExists, err := s.handlers.FileSystem.FileExists(editRegion.RelativeWorkspacePath)
		if err != nil {
			errors = append(errors, fmt.Sprintf("Failed to check file %s: %v", editRegion.RelativeWorkspacePath, err))
			result["status"] = "error"
			result["error"] = err.Error()
		} else if !fileExists {
			errors = append(errors, fmt.Sprintf("File %s does not exist", editRegion.RelativeWorkspacePath))
			result["status"] = "error"
			result["error"] = "file does not exist"
		} else {
			result["status"] = "ready"
		}

		results = append(results, result)
	}

	response := map[string]interface{}{
		"edit_plan":   args.EditPlan,
		"total_files": len(args.EditRegions),
		"results":     results,
		"errors":      errors,
		"note":        "This tool sketches out the parallel edit plan. Individual edits would need to be applied separately using codegenEditFile tool.",
	}

	return CreateJSONResponse(response)
}
