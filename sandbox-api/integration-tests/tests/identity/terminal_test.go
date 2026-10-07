package tests

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

func connectTerminal(t *testing.T, session string) *websocket.Conn {
	t.Helper()
	u, err := url.Parse(common.BaseURL)
	require.NoError(t, err)
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	u.Path += "/terminal/ws"
	q := u.Query()
	q.Set("sessionId", session)
	q.Set("shell", "/bin/sh")
	q.Set("workingDir", "/tmp")
	u.RawQuery = q.Encode()
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(10*time.Second)))
	return conn
}

func sendTerminalInput(t *testing.T, conn *websocket.Conn, input string) {
	t.Helper()
	require.NoError(t, conn.WriteJSON(map[string]string{"type": "input", "data": input}))
}

func readTerminalUntil(t *testing.T, conn *websocket.Conn, marker string) string {
	t.Helper()
	var output strings.Builder
	for !strings.Contains(output.String(), marker) {
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		require.NoError(t, conn.ReadJSON(&msg))
		if msg.Type == "output" {
			output.WriteString(msg.Data)
		}
	}
	return output.String()
}

func expectTerminalExit(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	var output strings.Builder
	for {
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		err := conn.ReadJSON(&msg)
		if err != nil {
			var closeErr *websocket.CloseError
			require.True(t, errors.As(err, &closeErr), "expected close frame, got %v", err)
			require.Equal(t, websocket.CloseNormalClosure, closeErr.Code)
			require.Equal(t, "Shell exited", closeErr.Text)
			return output.String()
		}
		if msg.Type == "output" {
			output.WriteString(msg.Data)
		}
	}
}

func TestTerminalShellExit(t *testing.T) {
	for _, tc := range []struct{ name, command string }{
		{"exit", "exit\n"},
		{"nonzero", "exit 7\n"},
		{"eof", "\x04"},
		{"background-child", "sleep 30 &\nexit\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conn := connectTerminal(t, fmt.Sprintf("terminal-%s-%d", tc.name, time.Now().UnixNano()))
			// Disable echo so command text cannot satisfy the final-output assertion.
			sendTerminalInput(t, conn, "stty -echo; printf 'ready-%s\\n' marker\n")
			readTerminalUntil(t, conn, "ready-marker")
			sendTerminalInput(t, conn, "printf 'final-%s\\n' marker\n"+tc.command)
			require.Contains(t, expectTerminalExit(t, conn), "final-marker")
		})
	}
}

func TestTerminalReconnectPreservesShell(t *testing.T) {
	session := fmt.Sprintf("terminal-reconnect-%d", time.Now().UnixNano())
	conn := connectTerminal(t, session)
	sendTerminalInput(t, conn, "stty -echo; saved=still-here; printf 'ready-%s\\n' marker\n")
	readTerminalUntil(t, conn, "ready-marker")
	require.NoError(t, conn.Close())
	conn = connectTerminal(t, session)
	sendTerminalInput(t, conn, "printf 'saved:%s\\n' \"$saved\"\nexit\n")
	require.Contains(t, expectTerminalExit(t, conn), "saved:still-here")
}

func TestTerminalExitClosesEverySubscriber(t *testing.T) {
	session := fmt.Sprintf("terminal-subscribers-%d", time.Now().UnixNano())
	first := connectTerminal(t, session)
	sendTerminalInput(t, first, "stty -echo; printf 'ready-%s\\n' marker\n")
	readTerminalUntil(t, first, "ready-marker")
	second := connectTerminal(t, session)
	readTerminalUntil(t, second, "ready-marker")
	sendTerminalInput(t, first, "exit\n")
	expectTerminalExit(t, first)
	expectTerminalExit(t, second)
}
