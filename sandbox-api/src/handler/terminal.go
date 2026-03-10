package handler

import (
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/terminal"
	"github.com/blaxel-ai/sandbox-api/src/lib/audit"
)

var (
	terminalHandlerInstance *TerminalHandler
	terminalHandlerOnce     sync.Once
)

// GetTerminalHandler returns the singleton terminal handler instance
func GetTerminalHandler() *TerminalHandler {
	terminalHandlerOnce.Do(func() {
		terminalHandlerInstance = NewTerminalHandler()
	})
	return terminalHandlerInstance
}

// TerminalHandler handles terminal WebSocket connections
type TerminalHandler struct {
	*BaseHandler
	upgrader websocket.Upgrader
}

// NewTerminalHandler creates a new terminal handler
func NewTerminalHandler() *TerminalHandler {
	return &TerminalHandler{
		BaseHandler: NewBaseHandler(),
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for sandbox use
			},
		},
	}
}

// TerminalMessage represents a message to/from the terminal
type TerminalMessage struct {
	Type string `json:"type"` // "input", "output", "resize"
	Data string `json:"data,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

// terminalEscapeState tracks which ANSI/VT escape sequence the parser is inside.
type terminalEscapeState int

const (
	termEscNone   terminalEscapeState = iota // normal input
	termEscStart                             // saw ESC, type not yet known
	termEscCSI                               // inside CSI sequence (ESC [)
	termEscOSC                               // inside OSC sequence (ESC ])
	termEscOSCST                             // saw ESC inside OSC (possible ST = ESC \)
)

// terminalCommandBuffer reconstructs typed commands from raw PTY input bytes.
// It uses a state machine to correctly skip CSI (ESC [), OSC (ESC ]), and
// two-character escape sequences so that no payload bytes leak into the
// reconstructed command string.
type terminalCommandBuffer struct {
	buf   strings.Builder
	state terminalEscapeState
}

// feed processes raw PTY input and returns any completed commands (submitted via Enter).
func (cb *terminalCommandBuffer) feed(data []byte) []string {
	var commands []string
	for _, b := range data {
		switch cb.state {
		case termEscNone:
			switch b {
			case 0x1b: // ESC — start of escape sequence
				cb.state = termEscStart
			case 0x7f, 0x08: // DEL / Backspace
				s := cb.buf.String()
				if len(s) > 0 {
					cb.buf.Reset()
					cb.buf.WriteString(s[:len(s)-1])
				}
			case '\r', '\n': // Enter — flush current command
				if cmd := strings.TrimSpace(cb.buf.String()); cmd != "" {
					commands = append(commands, cmd)
				}
				cb.buf.Reset()
			default:
				if b >= 0x20 { // printable ASCII / UTF-8 continuation bytes
					cb.buf.WriteByte(b)
				}
			}
		case termEscStart:
			switch b {
			case '[':
				cb.state = termEscCSI // CSI sequence
			case ']':
				cb.state = termEscOSC // OSC sequence
			default:
				cb.state = termEscNone // two-char escape sequence — done
			}
		case termEscCSI:
			// CSI final byte is in range 0x40–0x7E (@–~)
			if b >= 0x40 && b <= 0x7e {
				cb.state = termEscNone
			}
			// intermediate/parameter bytes: keep consuming
		case termEscOSC:
			switch b {
			case 0x07: // BEL terminates OSC
				cb.state = termEscNone
			case 0x1b: // possible ST (ESC \)
				cb.state = termEscOSCST
			}
			// other bytes are OSC payload — consume without buffering
		case termEscOSCST:
			// ESC \ = String Terminator, ends the OSC sequence
			// Any other byte is a new escape sequence intro
			switch b {
			case '\\':
				cb.state = termEscNone
			case '[':
				cb.state = termEscCSI
			case ']':
				cb.state = termEscOSC
			default:
				cb.state = termEscNone
			}
		}
	}
	return commands
}

func (h *TerminalHandler) HandleTerminalPage(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, terminal.GetTerminalHTML())
}

func (h *TerminalHandler) HandleTerminalWS(c *gin.Context) {
	// Parse query parameters
	cols := uint16(80)
	rows := uint16(24)
	if colsStr := c.Query("cols"); colsStr != "" {
		if v, err := strconv.ParseUint(colsStr, 10, 16); err == nil {
			cols = uint16(v)
		}
	}
	if rowsStr := c.Query("rows"); rowsStr != "" {
		if v, err := strconv.ParseUint(rowsStr, 10, 16); err == nil {
			rows = uint16(v)
		}
	}
	shell := c.Query("shell")
	workingDir := c.Query("workingDir")
	sessionId := c.DefaultQuery("sessionId", "default")

	// Capture identity before WebSocket upgrade (headers available only on HTTP request)
	id := audit.GetIdentity(c)

	audit.LogEvent(c, "terminal_connect", logrus.Fields{
		"session-id":  sessionId,
		"shell":       shell,
		"working-dir": workingDir,
	})

	// Upgrade HTTP connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logrus.Errorf("Failed to upgrade WebSocket: %v", err)
		return
	}

	// Get or reconnect to a persistent terminal session
	manager := terminal.GetSessionManager()
	ms, isNew, err := manager.GetOrCreate(sessionId, shell, workingDir, nil, cols, rows)
	if err != nil {
		logrus.Errorf("Failed to create terminal session: %v", err)
		_ = conn.WriteJSON(TerminalMessage{
			Type: "error",
			Data: err.Error(),
		})
		conn.Close()
		return
	}

	// Subscribe to terminal output
	sub := ms.Subscribe()

	// Replay buffered output so the client sees the full terminal history.
	// For a new session this is typically empty or just the initial prompt.
	// For a reconnection this restores the terminal state.
	buffered := ms.GetBuffer()
	if len(buffered) > 0 {
		_ = conn.WriteJSON(TerminalMessage{
			Type: "output",
			Data: string(buffered),
		})
	}

	// On reconnection, resize to match the new client's dimensions
	if !isNew && cols > 0 && rows > 0 {
		_ = ms.Resize(cols, rows)
	}

	// Channel to signal when to stop, with sync.Once to prevent double-close panic
	done := make(chan struct{})
	var closeOnce sync.Once
	closeDone := func() {
		closeOnce.Do(func() {
			close(done)
		})
	}

	// WaitGroup ensures the output goroutine finishes writing before we
	// close the WebSocket connection. gorilla/websocket does not support
	// concurrent writes, so conn.Close() must not race with WriteJSON.
	var wg sync.WaitGroup
	wg.Add(1)

	// Read from subscriber channel and send to WebSocket
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				logrus.Errorf("Terminal WS output goroutine panic: %v", r)
			}
			closeDone()
		}()

		for {
			select {
			case data, ok := <-sub.Ch:
				if !ok {
					return
				}
				msg := TerminalMessage{
					Type: "output",
					Data: string(data),
				}
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			case <-done:
				return
			case <-ms.Done():
				return
			}
		}
	}()

	// Cleanup in correct order: stop goroutine -> wait for last write ->
	// close connection -> unsubscribe.  Using a single defer guarantees
	// this ordering regardless of which return path is taken.
	defer func() {
		audit.LogEventDirect(id, "terminal_disconnect", logrus.Fields{
			"session-id": sessionId,
		})
		closeDone()
		wg.Wait()
		conn.Close()
		ms.Unsubscribe(sub)
	}()

	// cmdBuf tracks the line currently being typed to reconstruct commands for audit logs.
	var cmdBuf terminalCommandBuffer

	// Read from WebSocket and write to PTY
	for {
		select {
		case <-done:
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return
		}

		var msg TerminalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			logrus.Warnf("Invalid terminal message: %v", err)
			continue
		}

		switch msg.Type {
		case "input":
			data := []byte(msg.Data)
			for _, cmd := range cmdBuf.feed(data) {
				audit.LogEventDirect(id, "terminal_command", logrus.Fields{
					"session-id": sessionId,
					"command":   cmd,
				})
			}
			if _, err := ms.Write(data); err != nil {
				logrus.Warnf("Failed to write to PTY: %v", err)
			}
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				if err := ms.Resize(msg.Cols, msg.Rows); err != nil {
					logrus.Warnf("Failed to resize PTY: %v", err)
				}
			}
		}
	}
}
