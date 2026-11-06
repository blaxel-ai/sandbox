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

Output draining is bounded: after the shell exits, the server closes a PTY
still held by a background child once no output has been read for 250 ms.
Output still being delivered to a slow client extends this, up to 30 seconds.
Every WebSocket write, including the remaining output and the close frame,
must complete within five seconds. A client that keeps reading gets all of it,
however far behind it is; a client that stops reading for five seconds is
disconnected, and observes an abnormal transport closure instead of the normal
close frame.

## Slow clients

Output is never dropped silently. While a client is attached, the server reads
the PTY no faster than that client consumes output, so a slow client throttles
the shell (and every other client of the same session) instead of losing data.

A client that reads nothing for 15 seconds is dropped. It first receives the
output queued for it, without gaps, then `{"type":"error","data":"output
dropped: client too slow, reconnect to resync"}` and a close with code `1013`
and the same reason. It should reconnect with the same `sessionId`. The replay
holds only the last 100 KiB of output: enough to restore the terminal screen,
not to recover everything the client missed.

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
- `{"type":"error","data":"output dropped: ..."}`: the client fell too far
  behind and output was dropped for it (see Slow clients). The server then
  closes the stream; reopen it to resync.

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
