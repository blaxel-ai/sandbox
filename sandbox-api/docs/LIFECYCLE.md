# Sandbox Lifecycle Management

This document describes the sandbox lifecycle management features, including process keepAlive and force stop functionality.

## Overview

The sandbox-api provides mechanisms to control the sandbox's auto-hibernation behavior:

1. **Process KeepAlive** - Prevents auto-hibernation while specific processes are running
2. **Force Stop** - Removes keepAlive from processes to enable auto-hibernation
3. **Status** - Reports the current sandbox state and active keepAlive processes

## How Auto-Hibernation Works

The sandbox uses a **counter-based scale-to-zero** system controlled via a special file:

| Operation | Effect |
|-----------|--------|
| `+` | Increment counter (disable auto-hibernation) |
| `-` | Decrement counter (enable auto-hibernation when counter reaches 0) |
| `=0` | Reset counter to 0 (used for crash recovery on startup) |

When the counter is **greater than 0**, auto-hibernation is disabled.
When the counter is **0**, auto-hibernation is enabled.

## Process KeepAlive

### Description

When launching a process with `keepAlive: true`, the sandbox will:
1. Increment the scale-to-zero counter (disabling auto-hibernation)
2. Keep the counter incremented until the process completes or is stopped
3. Decrement the counter when the process ends (enabling auto-hibernation if no other keepAlive processes exist)

### API Usage

**POST /process**

```json
{
  "command": "npm run dev",
  "workingDir": "/app",
  "keepAlive": true,
  "timeout": 600
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `keepAlive` | boolean | `false` | When true, disables auto-hibernation while the process runs |
| `timeout` | integer | `600` | Timeout in seconds. If keepAlive is true and timeout is not specified, defaults to 600s. Set to `0` for infinite. |

### Timeout Behavior

- **timeout > 0**: Process will be automatically killed after the specified seconds
- **timeout = 0**: Process runs indefinitely (infinite timeout)
- **timeout not specified with keepAlive=true**: Defaults to 600 seconds (10 minutes)

### Logging

All keepAlive events are logged with the `[KeepAlive]` prefix:

```
[KeepAlive] Started process 12345 (name: my-app, command: npm run dev) with timeout 600s
[KeepAlive] Stopped process 12345 (name: my-app, status: completed, exit_code: 0)
```

## Force Stop

### Description

The force stop feature allows you to remove keepAlive from all current keepAlive processes, enabling auto-hibernation. This is useful when you want to allow the sandbox to hibernate regardless of running processes.

### Behavior

1. **Immediate Stop** (default): Removes keepAlive from all processes immediately
2. **Scheduled Stop**: Removes keepAlive after a specified timeout

### Important Notes

- Force stop does **NOT** kill processes - it only removes the keepAlive flag
- Processes continue running but no longer prevent auto-hibernation
- Each force stop call cancels any previously scheduled stop
- Only processes that exist at the time of the stop call are affected

### API Usage

**POST /stop**

Immediate stop:
```json
{}
```
or
```json
{
  "timeout": 0
}
```

Scheduled stop (in 5 minutes):
```json
{
  "timeout": 300
}
```

### Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `timeout` | integer | `0` | Timeout in seconds before stop takes effect. `0` = immediate. |

### Scheduling Rules

- Latest call always wins
- Previous scheduled stops are cancelled when a new stop is called
- Example: `stop(300s)` then `stop(200s)` → stop in 200s
- Example: `stop(200s)` then `stop(300s)` → stop in 300s

### Response

```json
{
  "state": "auto",
  "scheduledStopAt": null,
  "keepAliveProcesses": []
}
```

With scheduled stop:
```json
{
  "state": "awake",
  "scheduledStopAt": "2024-01-01T12:10:00Z",
  "keepAliveProcesses": [
    {
      "pid": "12345",
      "name": "my-app",
      "command": "npm run dev",
      "startedAt": "2024-01-01T12:00:00Z"
    }
  ]
}
```

## Status

### Description

Returns the current sandbox lifecycle status, including:
- Current state (awake/auto)
- Scheduled stop time (if any)
- List of processes with keepAlive enabled

### API Usage

**GET /status**

### Response

```json
{
  "state": "awake",
  "scheduledStopAt": "2024-01-01T12:10:00Z",
  "keepAliveProcesses": [
    {
      "pid": "12345",
      "name": "my-app",
      "command": "npm run dev",
      "startedAt": "2024-01-01T12:00:00Z",
      "timeout": 0
    }
  ]
}
```

### State Values

| State | Description |
|-------|-------------|
| `awake` | At least one keepAlive process exists - auto-hibernation disabled |
| `auto` | No keepAlive processes - auto-hibernation enabled |

## MCP Tools

The lifecycle features are also available as MCP (Model Context Protocol) tools:

### stop

Force stop the sandbox by removing keepAlive from processes.

**Input:**
```json
{
  "timeout": 0
}
```

### status

Get the current sandbox status.

**Output:**
```json
{
  "state": "awake",
  "scheduledStopAt": null,
  "keepAliveProcesses": []
}
```

## Crash Recovery

On startup, the sandbox-api resets the scale-to-zero counter to 0:

```go
blaxel.ScaleReset() // Writes "=0" to the scale file
```

This ensures that if the sandbox-api crashed while keepAlive processes were running, the counter is reset to a known state, preventing the sandbox from being stuck in a "never hibernate" state.

## Architecture

```
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  HTTP Request   │────▶│ LifecycleHandler │────▶│ ProcessHandler  │
│  POST /stop     │     │                  │     │                 │
└─────────────────┘     │  - scheduledStop │     │ RemoveKeepAlive │
                        │  - stopTimer     │     │       │         │
┌─────────────────┐     │  - stopPIDs      │     └───────┼─────────┘
│  HTTP Request   │────▶│                  │             │
│  GET /status    │     └──────────────────┘             ▼
└─────────────────┘                              ┌─────────────────┐
                                                 │ ProcessManager  │
┌─────────────────┐     ┌──────────────────┐     │                 │
│  Process Start  │────▶│ StartProcess()   │────▶│ KeepAlive=false │
│  keepAlive=true │     │                  │     │                 │
└─────────────────┘     │ ScaleDisable()   │     └───────┼─────────┘
                        └──────────────────┘             │
                                                         ▼
┌─────────────────┐     ┌──────────────────┐     ┌─────────────────┐
│  Process End    │────▶│ Process callback │────▶│   blaxel.go     │
│                 │     │                  │     │                 │
└─────────────────┘     │ ScaleEnable()    │     │ ScaleEnable()   │
                        └──────────────────┘     │ ScaleDisable()  │
                                                 │ ScaleReset()    │
                                                 └─────────────────┘
                                                         │
                                                         ▼
                                                 ┌─────────────────┐
                                                 │  Scale File     │
                                                 │ (internal)      │
                                                 └─────────────────┘
```

## File Locations

| File | Description |
|------|-------------|
| `src/handler/lifecycle.go` | HTTP handlers for /stop and /status |
| `src/mcp/lifecycle.go` | MCP tool implementations |
| `src/handler/process/process.go` | Process management with keepAlive support |
| `src/lib/blaxel/blaxel.go` | Scale-to-zero file operations |

## Examples

### Example 1: Run a dev server that keeps sandbox awake

```bash
# Start a process with keepAlive (10 minute default timeout)
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "npm run dev", "workingDir": "/app", "keepAlive": true}'

# Check status
curl http://localhost:8080/status
# {"state":"awake","keepAliveProcesses":[{"pid":"12345",...}]}
```

### Example 2: Run a process with infinite timeout

```bash
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "python server.py", "keepAlive": true, "timeout": 0}'
```

### Example 3: Force stop all keepAlive processes immediately

```bash
curl -X POST http://localhost:8080/stop
# {"state":"auto","keepAliveProcesses":[]}
```

### Example 4: Schedule force stop in 5 minutes

```bash
curl -X POST http://localhost:8080/stop \
  -H "Content-Type: application/json" \
  -d '{"timeout": 300}'
# {"state":"awake","scheduledStopAt":"2024-01-01T12:05:00Z","keepAliveProcesses":[...]}
```

### Example 5: Cancel scheduled stop by scheduling a new immediate stop

```bash
# First, schedule a stop in 10 minutes
curl -X POST http://localhost:8080/stop -d '{"timeout": 600}'

# Then, cancel it by doing an immediate stop
curl -X POST http://localhost:8080/stop
```
