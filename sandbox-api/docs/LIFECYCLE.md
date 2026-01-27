# Process KeepAlive

This document describes the process `keepAlive` feature that controls the sandbox's auto-hibernation behavior.

## Overview

When launching a process with `keepAlive: true`, the sandbox will stay awake (auto-hibernation disabled) until the process completes or times out.

## How It Works

The sandbox uses a **counter-based scale-to-zero** system:

| Counter | Effect |
|---------|--------|
| > 0 | Auto-hibernation disabled (sandbox stays awake) |
| = 0 | Auto-hibernation enabled (sandbox can sleep) |

When a `keepAlive` process starts, the counter is incremented. When it ends, the counter is decremented.

## API Usage

**POST /process**

```json
{
  "command": "npm run dev",
  "workingDir": "/app",
  "keepAlive": true,
  "timeout": 600
}
```

## Parameters

| Parameter | Type | Default | Description |
|-----------|------|---------|-------------|
| `keepAlive` | boolean | `false` | When true, disables auto-hibernation while the process runs |
| `timeout` | integer | `600` | Timeout in seconds. Set to `0` for infinite (no timeout) |

## Timeout Behavior

- **timeout > 0**: Process will be automatically killed after the specified seconds
- **timeout = 0**: Process runs indefinitely (infinite timeout)
- **timeout not specified with keepAlive=true**: Defaults to 600 seconds (10 minutes)

## Logging

All keepAlive events are logged:

```
[KeepAlive] Started process 12345 (name: my-app, command: npm run dev) with timeout 600s
[KeepAlive] Stopped process 12345 (name: my-app, status: completed, exit_code: 0)
```

Scale operations:

```
[Scale] Disabled scale-to-zero (wrote '+', counter now: 1) - sandbox staying AWAKE
[Scale] Enabled scale-to-zero (wrote '-', counter now: 0) - sandbox can AUTO-HIBERNATE
```

## Crash Recovery

On startup, the sandbox-api resets the scale-to-zero counter to 0, ensuring the sandbox doesn't get stuck awake if the API crashed while keepAlive processes were running.

## Examples

### Run a dev server that keeps sandbox awake

```bash
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "npm run dev", "workingDir": "/app", "keepAlive": true}'
```

### Run a process with infinite timeout

```bash
curl -X POST http://localhost:8080/process \
  -H "Content-Type: application/json" \
  -d '{"command": "python server.py", "keepAlive": true, "timeout": 0}'
```
