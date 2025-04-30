# MCP Integration Guide

This document explains how the Sandbox API integrates with the Model Context Protocol (MCP) to provide a standardized way for AI agents to interact with the sandbox environment.

## What is MCP?

Model Context Protocol (MCP) is a standardized protocol for AI agents to interact with external systems. It provides a schema-based approach to defining tools and their parameters, allowing agents to:

1. Discover available tools
2. Understand the required parameters for each tool
3. Execute tools and interpret their results

MCP uses WebSockets as the transport layer, enabling real-time bidirectional communication between agents and systems.

## MCP Server

The Sandbox API includes an MCP server implementation in `internal/mcp/server.go`. This server:

1. Registers all available tools from the Sandbox API
2. Provides schema information for each tool's parameters
3. Handles incoming tool calls from agents
4. Translates tool calls into internal API calls
5. Returns results in a standardized format

## Available Tools

The MCP server exposes the following tools:

### Process Tools

- **listProcesses**
  - Description: List all running processes
  - Parameters: None
  - Returns: List of process information

- **executeCommand**
  - Description: Execute a command
  - Parameters: 
    - `command` (string, required): The command to execute
    - `workingDir` (string): The working directory for the command
    - `waitForCompletion` (boolean): Whether to wait for the command to complete
    - `timeout` (integer): Timeout in seconds for the command
    - `waitForPorts` (array of integers): List of ports to wait for
  - Returns: Process information

- **getProcessLogs**
  - Description: Get logs for a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
  - Returns: Stdout and stderr logs

- **stopProcess**
  - Description: Stop a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
  - Returns: Process information

- **killProcess**
  - Description: Kill a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
  - Returns: Process information

### Filesystem Tools

- **getWorkingDirectory**
  - Description: Get the current working directory
  - Parameters: None
  - Returns: Current working directory path

- **listDirectory**
  - Description: List contents of a directory
  - Parameters:
    - `path` (string, required): Path to the directory
  - Returns: List of file and directory information

- **readFile**
  - Description: Read contents of a file
  - Parameters:
    - `path` (string, required): Path to the file
  - Returns: File contents and metadata

- **getFileSystemTree**
  - Description: Get a tree representation of the filesystem
  - Parameters:
    - `path` (string, required): Root path for the tree
    - `recursive` (boolean): Whether to get the tree recursively
  - Returns: Tree structure of the filesystem

- **createOrUpdateFile**
  - Description: Create or update a file
  - Parameters:
    - `path` (string, required): Path to the file
    - `content` (string): Content to write to the file
    - `permissions` (string): Permissions for the file (octal string)
  - Returns: File information

- **deleteFileOrDirectory**
  - Description: Delete a file or directory
  - Parameters:
    - `path` (string, required): Path to the file or directory
    - `recursive` (boolean): Whether to delete recursively (for directories)
  - Returns: Success status

### Network Tools

- **getProcessPorts**
  - Description: Get ports for a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
  - Returns: List of ports used by the process

- **monitorProcessPorts**
  - Description: Start monitoring ports for a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
    - `callback` (string): Callback URL for port monitoring notifications
  - Returns: Monitoring status

- **stopMonitoringProcessPorts**
  - Description: Stop monitoring ports for a specific process
  - Parameters:
    - `pid` (integer, required): Process ID
  - Returns: Success status

## Example MCP Tool Call

Here's an example of an MCP tool call to execute a command:

```json
{
  "id": "12345",
  "type": "tool_call",
  "data": {
    "name": "executeCommand",
    "arguments": {
      "command": "ls -la",
      "workingDir": "/home/user",
      "waitForCompletion": true
    }
  }
}
```

The response would look like:

```json
{
  "id": "12345",
  "type": "tool_response",
  "data": {
    "content": {
      "type": "text",
      "value": "{\"pid\":1234,\"command\":\"ls -la\",\"status\":\"completed\",\"startedAt\":\"Mon, 10 Apr 2023 15:30:00 GMT\",\"completedAt\":\"Mon, 10 Apr 2023 15:30:01 GMT\",\"workingDir\":\"/home/user\",\"exitCode\":0}"
    }
  }
}
```

## WebSocket Transport

The MCP server uses a WebSocket transport layer defined in `internal/mcp/transport.go`. This transport:

1. Sets up a WebSocket endpoint for MCP communication
2. Handles connection lifecycle (open, message, error, close)
3. Processes incoming messages and routes them to the appropriate handler
4. Sends responses back to the client

## Client Integration

The client directory contains TypeScript code for integrating with the MCP server:

- `client.ts`: Provides a client for connecting to the MCP server
- `server.ts`: Allows embedding the MCP client in another server

## Security Considerations

When integrating with MCP, consider the following security aspects:

1. Authentication: Implement authentication for MCP connections
2. Authorization: Limit which tools agents can execute
3. Rate limiting: Prevent abuse of the API
4. Resource limits: Set resource limits for processes executed via MCP

## Debugging MCP Integration

To debug MCP integration issues:

1. Check the WebSocket connection status
2. Look for errors in the server logs
3. Verify that the tool call parameters match the expected schema
4. Test tools directly via the REST API first

## References

- [MCP Specification](https://mcp.beamlit.dev/)
- [MCP Golang Library](https://github.com/metoro-io/mcp-golang) 