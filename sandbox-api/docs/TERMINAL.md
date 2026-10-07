# Terminal WebSocket lifecycle

`GET /terminal/ws` attaches to a persistent shell session. The `sessionId`
query parameter selects the session and defaults to `default`. Reconnecting
with the same ID reuses a live shell and replays its buffered output.

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
