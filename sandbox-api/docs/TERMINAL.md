# Terminal WebSocket lifecycle

`GET /terminal/ws` attaches to a persistent shell session. The `sessionId`
query parameter selects the session and defaults to `default`. Reconnecting
with the same ID reuses a live shell and replays its buffered output.

The `term` query parameter sets the shell's `TERM` when a session is created.
When it is absent or empty, the shell runs with `TERM=xterm-256color`. It has
no effect on reconnect: an existing session keeps the `TERM` it was created
with.

When the shell exits, the server sends remaining queued output and closes the
WebSocket with code `1000` and reason `Shell exited`. This applies to `exit`,
EOF at the shell prompt, and nonzero shell exit statuses. The close code
indicates that the terminal session ended; it does not report the shell's exit
status. There is no separate JSON exit event.

Clients should stop automatic reconnection after this normal close. An explicit
new connection with the same ID creates a new shell once the old session is
dead. After an unexpected transport failure, clients may reconnect with the
same ID to recover the existing live shell. A client disconnect alone does not
terminate that shell; sessions without clients expire after ten minutes idle.

Output draining is bounded: after the shell exits, the server allows 250 ms for
the PTY reader to finish before closing a PTY still held by a background child.
The final WebSocket output queue and close frame share a five-second write
deadline. A stalled client may therefore lose pending output and observe an
abnormal transport closure instead of the normal close frame.

# Terminal over HTTP streaming

For clients that cannot open a WebSocket (for example behind a proxy that only
forwards plain HTTP requests), the same sessions are available over two HTTP
endpoints. Both take the same `sessionId` query parameter, and a session can be
used from both transports at once.

`GET /terminal/stream` opens (or reattaches to) the session, with the same
`sessionId`, `cols`, `rows`, `shell` and `workingDir` query parameters as
`/terminal/ws`. The response is `text/event-stream` (Server-Sent Events). Each
`data:` line is a JSON message, with the same shape as the WebSocket messages:

- `{"type":"output","data":"..."}`: terminal output. The buffered output is
  replayed first, then live output follows.
- `{"type":"exit","data":"Shell exited"}`: the shell ended. The server then
  closes the stream.

Comment lines (`: keepalive`) are sent every 15 seconds so that idle proxies
keep the stream open. A client that stops reading for five seconds is
disconnected. Closing the stream does not end the shell, just like a WebSocket
disconnect.

`POST /terminal/input` sends one JSON message to a live session:

- `{"type":"input","data":"ls\r"}` writes keystrokes to the PTY.
- `{"type":"resize","cols":120,"rows":40}` resizes it.

It returns `204` on success, `404` if no live session has this `sessionId`
(open the stream first), `400` for an invalid message, `413` for a body over
1 MiB, and `409` if the shell can no longer take input.
