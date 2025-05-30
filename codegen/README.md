# MCP Codegen Tools Test Suite

A comprehensive test suite for the MCP (Model Context Protocol) codegen tools developed for the sandbox-api project.

## Overview

This test suite validates all 9 codegen MCP tools:

1. **`codegenEditFile`** - File editing with intelligent merging
2. **`codegenFileSearch`** - Fuzzy file search
3. **`codegenCodebaseSearch`** - Semantic code search
4. **`codegenGrepSearch`** - Regex pattern search
5. **`codegenReadFileRange`** - Read specific line ranges
6. **`codegenRunTerminalCmd`** - Command execution
7. **`codegenListDir`** - Directory listing
8. **`codegenParallelApply`** - Parallel edit planning
9. **`codegenReapply`** - Edit error recovery

## Prerequisites

1. **Node.js** >= 18.0.0
2. **MCP Server** running with codegen tools registered
3. **TypeScript** for development

## Installation

```bash
# Install dependencies
npm install

# Build the project
npm run build
```

## Usage

### Run All Tests

```bash
# Run complete test suite
npm test

# Run with verbose output
npm run test:verbose
```

### Filter Tests

```bash
# Filter tests by name, tool, or description
npm run test -- --filter "edit"
npm run test -- --filter "search"
npm run test -- --filter "terminal"
```

### List Available Tools

```bash
# List all codegen tools from the server
npm run list
```

### Custom Server URL

```bash
# Connect to a different MCP server
npm run test -- --server ws://localhost:9000/mcp
```

### Development Mode

```bash
# Run without building (using ts-node)
npm run dev
```

## Command Line Options

| Option | Short | Description | Default |
|--------|-------|-------------|---------|
| `--help` | `-h` | Show help message | - |
| `--list` | `-l` | List available tools | - |
| `--verbose` | `-v` | Verbose output | false |
| `--filter <pattern>` | `-f` | Filter tests | none |
| `--server <url>` | `-s` | Server URL | ws://localhost:8080/mcp |
| `--timeout <ms>` | `-t` | Timeout in ms | 30000 |

## Test Cases

### Directory Operations
- ✅ List root directory
- ❌ List non-existent directory

### File Search
- ✅ Search for Go files
- ✅ Search for package.json
- ✅ Search for non-existent patterns (empty results)

### Code Search
- ✅ Search for function definitions
- ✅ Search for import statements

### Regex Search
- ✅ Search for TODO comments
- ✅ Regex pattern matching
- ❌ Invalid regex patterns

### File Reading
- ✅ Read first 10 lines of README
- ❌ Read lines beyond file length
- ❌ Read non-existent file
- ❌ Invalid line ranges

### File Editing
- ✅ Create new test file
- ✅ Edit existing file
- ❌ Edit with invalid path

### Command Execution
- ✅ Execute simple command
- ✅ Execute background command
- ❌ Execute invalid command

### Parallel Operations
- ✅ Plan parallel edits
- ✅ Handle non-existent files

### Error Recovery
- ✅ Test reapply functionality

## Example Output

```
🚀 MCP Codegen Tools Test Suite
================================
Server URL: ws://localhost:8080/mcp
Verbose: false
Filter: none
Timeout: 30000ms

🔌 Connecting to MCP server...
✅ Connected to MCP server

🛠️  Available codegen tools:
   • codegenEditFile: Use this tool to propose an edit to an existing file...
   • codegenFileSearch: Fast file search based on fuzzy matching...
   • codegenCodebaseSearch: Find snippets of code from the codebase...
   ...

🧪 Running 20 test cases...

[1/20] Running: List root directory
   Tool: codegenListDir
   Description: Lists contents of the current directory
   ✅ PASSED (Success)

[2/20] Running: Search for Go files
   Tool: codegenFileSearch
   Description: Searches for Go source files
   ✅ PASSED (Success)

...

📊 Test Results Summary:
   ✅ Passed: 18
   ❌ Failed: 2
   📝 Total: 20
```

## Testing Strategy

### Test Categories

1. **Happy Path Tests**: Normal usage scenarios that should succeed
2. **Error Handling Tests**: Invalid inputs that should fail gracefully
3. **Edge Cases**: Boundary conditions and unusual inputs
4. **Integration Tests**: Multi-tool workflows

### Test Structure

Each test case includes:
- **Name**: Descriptive test name
- **Tool**: Target MCP tool
- **Arguments**: Input parameters
- **Expected Result**: Success or failure expectation
- **Description**: Test purpose
- **Setup/Cleanup**: Optional preparation and cleanup

### Filtering

Tests can be filtered by:
- Test name (e.g., "edit", "search")
- Tool name (e.g., "codegenEditFile")
- Description keywords

## Architecture

### Components

- **`CodegenMCPClient`**: WebSocket-based MCP client with tool filtering
- **`CodegenTestSuite`**: Test case definitions and execution
- **`TestRunner`**: CLI interface and test orchestration

### MCP Integration

- Connects via WebSocket to sandbox-api MCP server
- Filters tools to only include those starting with "codegen"
- Handles connection lifecycle and error recovery
- Provides detailed logging and timing information

## Troubleshooting

### Common Issues

1. **Connection Failed**: Ensure sandbox-api server is running on correct port
2. **No Tools Found**: Verify codegen tools are registered in server
3. **Test Failures**: Check server logs for detailed error information
4. **TypeScript Errors**: Run `npm run build` to compile latest changes

### Debug Mode

```bash
# Run with verbose output for debugging
npm run test -- --verbose

# Test a specific tool manually
npm run dev
# Then modify test-runner.ts to test specific scenarios
```

### Server Setup

Ensure the sandbox-api server is running:

```bash
cd ../sandbox-api
go run . -p 8080
```

## Contributing

1. **Add Test Cases**: Extend `src/test-cases.ts` with new scenarios
2. **Improve Client**: Enhance `src/mcp-client.ts` for better error handling
3. **CLI Features**: Add new commands to `src/test-runner.ts`

### Test Case Format

```typescript
{
  name: 'Descriptive test name',
  tool: 'codegenToolName',
  args: { /* tool arguments */ },
  expectSuccess: true, // or false
  description: 'What this test validates',
  setup: async () => { /* optional setup */ },
  cleanup: async () => { /* optional cleanup */ }
}
```

## License

MIT License - see LICENSE file for details.

## Related

- [MCP Specification](https://modelcontextprotocol.org/)
- [Sandbox API Documentation](../sandbox-api/README.md)
- [Codegen Tools Documentation](../sandbox-api/docs/codegen-tools.md)