package tests

import (
	"context"
	"errors"
	"fmt"
	"net"
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
	return dialTerminal(t, websocket.DefaultDialer, session)
}

// connectSlowTerminal connects with a tiny socket receive buffer, so that the
// client behaves like one on a slow link as soon as it stops reading.
func connectSlowTerminal(t *testing.T, session string) *websocket.Conn {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.NetDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, addr)
		if err == nil {
			err = conn.(*net.TCPConn).SetReadBuffer(4096)
		}
		return conn, err
	}
	return dialTerminal(t, &dialer, session)
}

func dialTerminal(t *testing.T, dialer *websocket.Dialer, session string) *websocket.Conn {
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
	conn, _, err := dialer.Dial(u.String(), nil)
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

// seqOutput is what `seq 1 n` prints.
func seqOutput(n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%d\n", i)
	}
	return b.String()
}

// readTerminalSlowly reads until the shell exits after a stall long enough for
// the shell to outrun the client. The PTY may add carriage returns.
func readTerminalSlowly(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Minute)))
	time.Sleep(3 * time.Second)
	var output strings.Builder
	for {
		var msg struct {
			Type string `json:"type"`
			Data string `json:"data"`
		}
		err := conn.ReadJSON(&msg)
		if err != nil {
			var closeErr *websocket.CloseError
			require.True(t, errors.As(err, &closeErr), "expected close frame after %d bytes, got %v", output.Len(), err)
			require.Equal(t, websocket.CloseNormalClosure, closeErr.Code, "after %d bytes: %s", output.Len(), closeErr.Text)
			return strings.ReplaceAll(output.String(), "\r", "")
		}
		require.Equal(t, "output", msg.Type, msg.Data)
		output.WriteString(msg.Data)
	}
}

// A client that reads slower than the shell writes must get every byte, not
// lose blocks of output mid-stream (the customer's `seq` reproduction).
func TestTerminalSlowClientGetsAllOutput(t *testing.T) {
	const n = 1000000 // 6.9 MB: more than the socket buffers (up to 4 MB) and the 64-slot queue
	conn := connectSlowTerminal(t, fmt.Sprintf("terminal-slow-%d", time.Now().UnixNano()))
	sendTerminalInput(t, conn, "stty -echo; printf 'ready-%s\\n' marker\n")
	readTerminalUntil(t, conn, "ready-marker")
	sendTerminalInput(t, conn, fmt.Sprintf("seq 1 %d\nexit\n", n))

	got := readTerminalSlowly(t, conn)
	require.True(t, strings.Contains(got, seqOutput(n)), "output lost: got %d bytes", len(got))
}

// Two clients on one session: the slow one sets the pace, neither loses output.
func TestTerminalSlowAndFastClientsGetAllOutput(t *testing.T) {
	const n = 1000000
	session := fmt.Sprintf("terminal-slow-fast-%d", time.Now().UnixNano())
	fast := connectTerminal(t, session)
	sendTerminalInput(t, fast, "stty -echo; printf 'ready-%s\\n' marker\n")
	readTerminalUntil(t, fast, "ready-marker")
	slow := connectSlowTerminal(t, session)
	readTerminalUntil(t, slow, "ready-marker")
	sendTerminalInput(t, fast, fmt.Sprintf("seq 1 %d\nexit\n", n))

	// Read the fast client concurrently, without require: it cannot stop the
	// test from another goroutine.
	fastOut := make(chan string, 1)
	go func() {
		var output strings.Builder
		_ = fast.SetReadDeadline(time.Now().Add(2 * time.Minute))
		for {
			var msg struct {
				Type string `json:"type"`
				Data string `json:"data"`
			}
			if err := fast.ReadJSON(&msg); err != nil {
				fastOut <- strings.ReplaceAll(output.String(), "\r", "")
				return
			}
			output.WriteString(msg.Data)
		}
	}()
	slowOut := readTerminalSlowly(t, slow)
	want := seqOutput(n)
	require.True(t, strings.Contains(slowOut, want), "slow client lost output: got %d bytes", len(slowOut))
	got := <-fastOut
	require.True(t, strings.Contains(got, want), "fast client lost output: got %d bytes", len(got))
}
