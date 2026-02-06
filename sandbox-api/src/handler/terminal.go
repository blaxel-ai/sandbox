package handler

import (
	"net/http"
	"strconv"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"

	"github.com/blaxel-ai/sandbox-api/src/handler/terminal"
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

	// Upgrade HTTP connection to WebSocket
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logrus.Errorf("Failed to upgrade WebSocket: %v", err)
		return
	}
	defer conn.Close()

	// Get or reconnect to a persistent terminal session
	manager := terminal.GetSessionManager()
	ms, isNew, err := manager.GetOrCreate(sessionId, shell, workingDir, nil, cols, rows)
	if err != nil {
		logrus.Errorf("Failed to create terminal session: %v", err)
		_ = conn.WriteJSON(TerminalMessage{
			Type: "error",
			Data: err.Error(),
		})
		return
	}

	// Subscribe to terminal output
	sub := ms.Subscribe()
	defer ms.Unsubscribe(sub)

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

	// Read from subscriber channel and send to WebSocket
	go func() {
		for {
			select {
			case data, ok := <-sub.Ch:
				if !ok {
					closeDone()
					return
				}
				msg := TerminalMessage{
					Type: "output",
					Data: string(data),
				}
				if err := conn.WriteJSON(msg); err != nil {
					closeDone()
					return
				}
			case <-done:
				return
			case <-ms.Done():
				closeDone()
				return
			}
		}
	}()

	// Read from WebSocket and write to PTY
	for {
		select {
		case <-done:
			return
		default:
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			closeDone()
			return
		}

		var msg TerminalMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			logrus.Warnf("Invalid terminal message: %v", err)
			continue
		}

		switch msg.Type {
		case "input":
			if _, err := ms.Write([]byte(msg.Data)); err != nil {
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
