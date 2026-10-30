package handler

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// TestTerminalWS_SlowClientGetsAllOutput floods the terminal while the client
// is not reading: output must be held back, not dropped.
func TestTerminalWS_SlowClientGetsAllOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/terminal/ws", NewTerminalHandler().HandleTerminalWS)
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/terminal/ws?sessionId=ws-slow-" + t.Name()
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Enough output to fill the socket buffers and the subscriber channel.
	const last = 200000
	cmd := fmt.Sprintf("stty -echo; seq 1 %d; exit\r", last)
	if err := conn.WriteJSON(TerminalMessage{Type: "input", Data: cmd}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)

	var out strings.Builder
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	for {
		var msg TerminalMessage
		if err := conn.ReadJSON(&msg); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
				t.Fatalf("read: %v", err)
			}
			break
		}
		if msg.Type != "output" {
			t.Fatalf("unexpected %q message: %s", msg.Type, msg.Data)
		}
		out.WriteString(msg.Data)
	}

	var want strings.Builder
	for i := 1; i <= last; i++ {
		fmt.Fprintf(&want, "%d\n", i)
	}
	// The PTY may add carriage returns of its own: compare lines only.
	if got := strings.ReplaceAll(out.String(), "\r", ""); !strings.Contains(got, want.String()) {
		t.Fatalf("output lost: got %d bytes, want the %d bytes of seq", out.Len(), want.Len())
	}
}
