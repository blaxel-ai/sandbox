package handler

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/terminal"
	"github.com/blaxel-ai/sandbox-api/src/lib/audit"
)

const (
	// terminalStreamKeepalive keeps idle proxies from closing a quiet stream.
	terminalStreamKeepalive = 15 * time.Second
	// terminalStreamWriteTimeout drops a client that stops reading, like the WebSocket.
	terminalStreamWriteTimeout = 5 * time.Second
	// terminalInputMaxBytes bounds one POST /terminal/input body.
	terminalInputMaxBytes = 1 << 20
)

// terminalInputState carries the audit command reconstruction of a session
// across POST /terminal/input calls, which each arrive on their own request.
type terminalInputState struct {
	mu     sync.Mutex
	cmdBuf terminalCommandBuffer
}

var terminalInputStates sync.Map // *terminal.ManagedSession -> *terminalInputState

func inputStateFor(ms *terminal.ManagedSession) *terminalInputState {
	state, loaded := terminalInputStates.LoadOrStore(ms, &terminalInputState{})
	if !loaded {
		go func() {
			<-ms.Done()
			terminalInputStates.Delete(ms)
		}()
	}
	return state.(*terminalInputState)
}

func terminalSize(c *gin.Context) (uint16, uint16) {
	cols, rows := uint16(80), uint16(24)
	if v, err := strconv.ParseUint(c.Query("cols"), 10, 16); err == nil {
		cols = uint16(v)
	}
	if v, err := strconv.ParseUint(c.Query("rows"), 10, 16); err == nil {
		rows = uint16(v)
	}
	return cols, rows
}

// HandleTerminalStream serves the terminal output as Server-Sent Events, for
// clients that cannot open a WebSocket (e.g. behind an HTTP-only proxy). Each
// event is a TerminalMessage in JSON, the same payload as on /terminal/ws:
// the buffered history first, then live "output", then "exit" when the shell
// ends. Input and resizes go to POST /terminal/input with the same sessionId.
func (h *TerminalHandler) HandleTerminalStream(c *gin.Context) {
	cols, rows := terminalSize(c)
	shell := c.Query("shell")
	workingDir := c.Query("workingDir")
	sessionId := c.DefaultQuery("sessionId", "default")
	id := audit.GetIdentity(c)

	audit.LogEvent(c, "terminal_connect", logrus.Fields{
		"session-id":  sessionId,
		"shell":       shell,
		"working-dir": workingDir,
		"transport":   "sse",
	})

	ms, isNew, err := terminal.GetSessionManager().GetOrCreate(sessionId, shell, workingDir, nil, cols, rows)
	if err != nil {
		logrus.Errorf("Failed to create terminal session: %v", err)
		h.SendError(c, http.StatusInternalServerError, err)
		return
	}

	sub, buffered := ms.SubscribeWithReplay()
	defer func() {
		ms.Unsubscribe(sub)
		audit.LogEventDirect(id, "terminal_disconnect", logrus.Fields{
			"session-id": sessionId,
			"transport":  "sse",
		})
	}()

	if !isNew && cols > 0 && rows > 0 {
		_ = ms.Resize(cols, rows)
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)

	rc := http.NewResponseController(c.Writer)
	write := func(frame string) bool {
		// Without a deadline a client that stops reading would pin this
		// handler and its subscription forever: refuse to stream instead.
		if err := rc.SetWriteDeadline(time.Now().Add(terminalStreamWriteTimeout)); err != nil {
			logrus.Errorf("Terminal stream: cannot set write deadline: %v", err)
			return false
		}
		if _, err := c.Writer.WriteString(frame); err != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	var send func(TerminalMessage) bool
	// PTY reads can split a UTF-8 character: hold the incomplete tail until
	// the next chunk so JSON encoding does not turn it into U+FFFD.
	var pending []byte
	sendOutput := func(data []byte) bool {
		data = append(pending, data...)
		data, pending = splitIncompleteUTF8(data)
		if len(data) == 0 {
			return true
		}
		return send(TerminalMessage{Type: "output", Data: string(data)})
	}
	send = func(msg TerminalMessage) bool {
		payload, err := json.Marshal(msg)
		if err != nil {
			return false
		}
		return write(fmt.Sprintf("data: %s\n\n", payload))
	}

	// The comment opens the stream at once, so proxies hand the headers over
	// before the shell has printed anything.
	if !write(": connected\n\n") {
		return
	}
	if len(buffered) > 0 && !sendOutput(buffered) {
		return
	}

	// drainAndSend writes the queued output, then the final message.
	drainAndSend := func(last TerminalMessage) {
		for {
			select {
			case data := <-sub.Ch:
				if !sendOutput(data) {
					return
				}
			default:
				if len(pending) > 0 && !send(TerminalMessage{Type: "output", Data: string(pending)}) {
					return
				}
				send(last)
				return
			}
		}
	}

	keepalive := time.NewTicker(terminalStreamKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case data := <-sub.Ch:
			if !sendOutput(data) {
				return
			}
		case <-keepalive.C:
			if !write(": keepalive\n\n") {
				return
			}
		case <-c.Request.Context().Done():
			return
		case <-ms.Done():
			// A subscriber is dropped before Done, never after: if both are
			// ready, the client must still learn that output is missing.
			select {
			case <-sub.Dropped():
				drainAndSend(TerminalMessage{Type: "error", Data: terminalOutputDroppedReason})
				return
			default:
			}
			// No more output is published after Done: drain what is queued.
			drainAndSend(TerminalMessage{Type: "exit", Data: "Shell exited"})
			return
		case <-sub.Dropped():
			// Output after what is queued will not reach this client: say so,
			// so that it reconnects and resyncs from the replay.
			drainAndSend(TerminalMessage{Type: "error", Data: terminalOutputDroppedReason})
			return
		}
	}
}

// HandleTerminalInput writes one TerminalMessage ("input" or "resize") to the
// session opened by GET /terminal/stream or /terminal/ws.
func (h *TerminalHandler) HandleTerminalInput(c *gin.Context) {
	sessionId := c.DefaultQuery("sessionId", "default")
	ms, ok := terminal.GetSessionManager().Get(sessionId)
	if !ok {
		h.SendError(c, http.StatusNotFound, fmt.Errorf("terminal session %q not found: open it with GET /terminal/stream first", sessionId))
		return
	}

	// Read the whole body so the size limit covers it all, not only the
	// first JSON value.
	body, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, terminalInputMaxBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.SendError(c, http.StatusRequestEntityTooLarge, fmt.Errorf("input is larger than %d bytes", terminalInputMaxBytes))
			return
		}
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("failed to read terminal message: %w", err))
		return
	}
	var msg TerminalMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("invalid terminal message: %w", err))
		return
	}

	switch msg.Type {
	case "input":
		data := []byte(msg.Data)
		state := inputStateFor(ms)
		state.mu.Lock()
		commands := state.cmdBuf.feed(data)
		_, err := ms.Write(data)
		state.mu.Unlock()
		for _, cmd := range commands {
			audit.LogEvent(c, "terminal_command", logrus.Fields{
				"session-id": sessionId,
				"command":    cmd,
			})
		}
		if err != nil {
			h.SendError(c, http.StatusConflict, fmt.Errorf("terminal session %q is not accepting input: %w", sessionId, err))
			return
		}
	case "resize":
		if msg.Cols == 0 || msg.Rows == 0 {
			h.SendError(c, http.StatusBadRequest, fmt.Errorf("resize needs cols and rows greater than 0"))
			return
		}
		if err := ms.Resize(msg.Cols, msg.Rows); err != nil {
			h.SendError(c, http.StatusConflict, fmt.Errorf("failed to resize terminal session %q: %w", sessionId, err))
			return
		}
	default:
		h.SendError(c, http.StatusBadRequest, fmt.Errorf("unknown message type %q: expected \"input\" or \"resize\"", msg.Type))
		return
	}
	c.Status(http.StatusNoContent)
}

// splitIncompleteUTF8 returns b without a trailing incomplete UTF-8 sequence,
// and that sequence.
func splitIncompleteUTF8(b []byte) ([]byte, []byte) {
	for i := 1; i <= utf8.UTFMax-1 && i <= len(b); i++ {
		if utf8.RuneStart(b[len(b)-i]) {
			if utf8.FullRune(b[len(b)-i:]) {
				return b, nil
			}
			return b[:len(b)-i], b[len(b)-i:]
		}
	}
	return b, nil
}
