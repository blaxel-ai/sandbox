# Sandbox API Client

This client library provides a convenient way to interact with the OS-as-a-Service Sandbox API through MCP (Machine Control Protocol).

## Features

- Connect to the Sandbox API using WebSocket-based MCP
- Execute commands in the sandbox environment
- Manipulate the filesystem
- Monitor network activity

## Installation

```bash
# Using npm
npm install

# Using pnpm
pnpm install
```

## Usage

### Client Usage

The client allows you to connect to the Sandbox API and execute tools:

```typescript
import { connectToMCP } from './client';

async function main() {
  const client = await connectToMCP('https://your-mcp-endpoint.com');
  
  // Execute a command
  const result = await client.callTool('executeCommand', {
    command: 'ls -la',
    workingDir: '/home/user',
    waitForCompletion: true
  });
  
  console.log('Command result:', result);
  
  // Read a file
  const fileContents = await client.callTool('readFile', {
    path: '/path/to/file.txt'
  });
  
  console.log('File contents:', fileContents);
}

main().catch(console.error);
```

### Server Integration

You can also integrate the Sandbox API with your own server:

```typescript
import { createMcpServer } from './server';

async function main() {
  const server = await createMcpServer({
    port: 3000,
    mcpUrl: 'https://your-mcp-endpoint.com'
  });
  
  console.log('MCP server running on port 3000');
}

main().catch(console.error);
```

## Available Tools

The client can access all tools available in the Sandbox API:

### Process Tools
- `listProcesses`: List all running processes
- `executeCommand`: Execute a command
- `getProcessLogs`: Get logs for a specific process
- `stopProcess`: Stop a running process
- `killProcess`: Forcefully terminate a running process

### Filesystem Tools
- `getWorkingDirectory`: Get the current working directory
- `readFile`: Read the contents of a file
- `listDirectory`: List the contents of a directory
- `getFileSystemTree`: Get a tree representation of the filesystem
- `createOrUpdateFile`: Create or update a file
- `deleteFileOrDirectory`: Delete a file or directory

### Network Tools
- `getProcessPorts`: Get ports used by a specific process
- `monitorProcessPorts`: Monitor ports for a specific process
- `stopMonitoringProcessPorts`: Stop monitoring ports for a specific process

## Environment Configuration

The client uses the following environment variables:

- `BLAXEL_MCP_URL`: The URL of the MCP server to connect to

## License

This project is licensed under the MIT License. 