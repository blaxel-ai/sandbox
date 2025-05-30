# Codegen MCP Tools

This document describes the comprehensive set of MCP (Model Context Protocol) tools implemented for codegen agents, based on the recommendations from [Morph Documentation](https://docs.morphllm.com/guides/tools#delete-file-file-removal).

## Overview

The codegen tools provide AI agents with the ability to:
- Navigate and discover code repositories
- Search for specific code patterns and functions
- Read and edit files intelligently
- Execute commands and manage processes
- Handle error recovery and reapplication

## Core Tools

### 1. `codegenEditFile` - The Most Critical Tool

**Purpose**: Propose edits to existing files or create new files with minimal token usage.

**Arguments**:
- `target_file` (required): The target file to modify (relative path)
- `instructions` (required): Single sentence describing the edit
- `code_edit` (required): Only the precise lines to edit, using `// ... existing code ...` markers

**Key Features**:
- Handles both new file creation and existing file modification
- Intelligent merging of changes with existing content
- Minimal token usage by only specifying changed lines
- Supports all file types

**Example Usage**:
```json
{
  "target_file": "src/main.go",
  "instructions": "I am adding error handling to the main function",
  "code_edit": "func main() {\n    // ... existing code ...\n    if err != nil {\n        log.Fatal(err)\n    }\n    // ... existing code ...\n}"
}
```

### 2. `codegenFileSearch` - Fast Fuzzy File Search

**Purpose**: Quick file discovery using fuzzy matching.

**Arguments**:
- `query` (required): Fuzzy filename to search for

**Features**:
- Searches both filenames and full paths
- Returns up to 10 matches
- Case-insensitive fuzzy matching

### 3. `codegenCodebaseSearch` - Semantic Code Search

**Purpose**: Find relevant code snippets across the codebase.

**Arguments**:
- `query` (required): Search query for relevant code
- `target_directories` (optional): Directories to search in

**Features**:
- Searches within code files only
- Provides context lines around matches
- Filters by common code file extensions
- Returns up to 20 results with line numbers

### 4. `codegenGrepSearch` - Fast Regex Search

**Purpose**: Exact pattern matching using regex.

**Arguments**:
- `query` (required): Regex pattern to search for
- `case_sensitive` (optional): Case sensitivity flag
- `include_pattern` (optional): File pattern to include (e.g., "*.go")
- `exclude_pattern` (optional): File pattern to exclude

**Features**:
- Uses ripgrep when available, falls back to internal implementation
- Supports file filtering with glob patterns
- Returns up to 50 matches
- Proper regex escaping and validation

### 5. `codegenReadFileRange` - Precise File Reading

**Purpose**: Read specific line ranges from files.

**Arguments**:
- `target_file` (required): Path to the file
- `start_line_one_indexed` (required): Starting line number (1-indexed)
- `end_line_one_indexed_inclusive` (required): Ending line number (1-indexed)

**Features**:
- Reads up to 250 lines at a time
- Provides line numbers and total file length
- Validates line ranges
- Returns both content string and line arrays

### 6. `codegenRunTerminalCmd` - Command Execution

**Purpose**: Execute terminal commands safely.

**Arguments**:
- `command` (required): The terminal command to execute
- `is_background` (optional): Whether to run in background

**Features**:
- Supports both synchronous and background execution
- Returns process information and logs
- Integrates with existing process management
- 30-second timeout for synchronous commands

### 7. `codegenListDir` - Directory Discovery

**Purpose**: List directory contents for navigation and discovery.

**Arguments**:
- `relative_workspace_path` (required): Path relative to workspace root

**Features**:
- Provides detailed file and directory information
- Integration with existing filesystem handler
- Supports recursive directory exploration

### 8. `codegenParallelApply` - Systematic Changes

**Purpose**: Plan parallel edits across multiple files.

**Arguments**:
- `edit_plan` (required): Description of the parallel edits
- `edit_regions` (required): Array of files and regions to edit

**Features**:
- Validates file existence before planning
- Provides status for each planned edit
- Supports up to 50 files per operation
- Returns detailed planning information

### 9. `codegenReapply` - Error Recovery

**Purpose**: Recover from failed edit operations.

**Arguments**:
- `target_file` (required): File to reapply edit to

**Features**:
- Placeholder for intelligent edit reapplication
- Framework for using smarter models
- Error recovery workflow support

## Tool Usage Workflow

The recommended workflow for codegen agents:

1. **Discovery**: Use `codegenListDir` to understand project structure
2. **Search**: Use `codegenCodebaseSearch` or `codegenGrepSearch` to find relevant code
3. **Context Building**: Use `codegenReadFileRange` to examine specific sections
4. **Modification**: Use `codegenEditFile` to make precise changes
5. **Verification**: Use `codegenReadFileRange` again to verify changes
6. **Command Execution**: Use `codegenRunTerminalCmd` for testing or building

## Implementation Details

### File Search Strategy
- Fuzzy matching algorithm for file discovery
- Code file filtering by extension
- Context extraction with surrounding lines
- Result limiting to prevent overwhelming output

### Edit Application Logic
- Intelligent merging for existing files
- Marker-based unchanged code sections
- New file creation with marker filtering
- Error handling and validation

### Security Considerations
- File path validation and sanitization
- Command execution through existing process handlers
- Working directory restrictions
- Timeout enforcement for commands

### Performance Optimizations
- Result limiting (10 files, 20 code results, 50 grep matches)
- Early termination for large repositories
- Efficient file walking and pattern matching
- JSON streaming for large outputs

## Integration

These tools are registered automatically when the MCP server starts:

```go
// In server.go
func (s *Server) registerTools() error {
    // ... existing tools ...

    // Codegen tools
    if err := s.registerCodegenTools(); err != nil {
        return err
    }
    logrus.Info("Codegen tools registered")
    return nil
}
```

## Error Handling

All tools follow consistent error handling patterns:
- Detailed error messages with context
- Graceful fallbacks where possible
- Validation of input parameters
- Logging of tool calls and performance metrics

## Future Enhancements

Potential improvements for production use:
- Vector embeddings for semantic search
- More sophisticated edit merging algorithms
- Integration with language servers for syntax awareness
- Caching for improved search performance
- Real-time file system monitoring
- Advanced parallel processing capabilities

This comprehensive tool suite enables AI agents to effectively navigate, understand, and modify codebases with high precision and efficiency.