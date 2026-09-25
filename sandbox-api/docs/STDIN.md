# Process stdin

`POST /process` runs a command with stdout and stderr going to log files. By
default the process has no stdin (it reads EOF at once). Set `"stdin": true` to
give it a writable pipe instead, then drive it over HTTP. This is what a stdio
protocol such as MCP needs: raw bytes in, raw lines out, no PTY in between.

## API

| Call | Effect |
|---|---|
| `POST /process` with `{"command": "...", "name": "...", "stdin": true}` | Start the process with a stdin pipe. The response carries `"stdin": true`. |
| `POST /process/{name}/stdin` with a raw body | Write the body verbatim to stdin. Include the trailing newline your protocol expects. Max 8 MiB per call. |
| `GET /process/{name}/logs/stream` | Read stdout. Lines are tagged `stdout:` / `stderr:`; drop the tag and ignore `[keepalive]` lines. |
| `DELETE /process/{name}/stdin` | Close stdin (EOF). Idempotent. For MCP this is the clean shutdown. |

Errors:

- `404` unknown process.
- `409` the process was started without `stdin: true`, or its stdin is already
  closed: explicit `DELETE`, process exited, or sandbox-api restarted (see
  below). The message says which.
- `413` body over 8 MiB. `400` for a body that could not be read.
- `503` the process stopped reading its stdin: the pipe buffer stayed full for
  10 seconds. Part of the body may have gone through, so treat the stream as
  suspect and restart the process if it does not recover.

Sequential writes keep their order. Each body is written under one lock, so two
concurrent writers never interleave inside a message. A write holds that lock
for at most the 10 second deadline, so one stuck writer cannot pin the others.

Writes and closes are audited as `process_stdin_write` (byte count only, never
the body) and `process_stdin_close`; `process_exec` records whether stdin was
requested.

`DISABLE_TERMINAL` does not affect these routes. It removes the web PTY, while
`POST /process` already runs arbitrary commands with or without stdin; a pipe
adds no capability the process API did not have.

## Restart behaviour

- **Restart on failure** (`restartOnFailure: true`): each run gets a fresh pipe.
  Writes after the restart reach the new child. Anything written between the
  failure and the restart is lost with the old pipe.
- **sandbox-api restart** (OOM, crash, upgrade): the pipe lives in sandbox-api,
  so it dies with it. The child reads EOF, which for a stdio server means
  "shut down". The process is re-attached on the next start and still reports
  `"stdin": true`, but writes return `409 stdin is closed`. Restart the process
  and re-run your protocol handshake.
- **Sandbox scale-to-zero / resume**: the whole VM is snapshotted, pipe
  included. Nothing to do.
- **Relaunch from an archive**: a process restored at boot from the archived
  state is started with the same `stdin` flag it had, so it gets a fresh pipe.
  The protocol handshake has to be redone, as after any restart.

A pipe rather than a FIFO on disk is a deliberate trade: stdin survival across a
sandbox-api restart would buy little, since the client's log stream drops at the
same moment and any stdio session has to be re-initialised anyway.

## Example: an MCP server over stdio

```sh
BASE=http://localhost:8080

curl -s $BASE/process -H 'Content-Type: application/json' -d '{
  "name": "mcp-fs",
  "command": "npx -y @modelcontextprotocol/server-filesystem /tmp",
  "stdin": true
}'

# Follow stdout in another shell, keeping only stdout lines:
curl -sN $BASE/process/mcp-fs/logs/stream | sed -n 's/^stdout://p'

curl -s $BASE/process/mcp-fs/stdin --data-binary \
  '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"curl","version":"0"}}}
'
curl -s $BASE/process/mcp-fs/stdin --data-binary '{"jsonrpc":"2.0","method":"notifications/initialized"}
'
curl -s $BASE/process/mcp-fs/stdin --data-binary '{"jsonrpc":"2.0","id":2,"method":"tools/list"}
'

curl -s -X DELETE $BASE/process/mcp-fs/stdin
```

To use an MCP SDK's stdio transport unchanged, run a small local shim whose own
stdin/stdout are bridged to these two calls, and point the transport at the shim.
