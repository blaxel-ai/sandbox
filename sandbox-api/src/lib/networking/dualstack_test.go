package networking

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const sampleProcTCP = `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12345 1 0000000000000000 100 0 0 10 0
   1: 0100007F:0BB8 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12346 1 0000000000000000 100 0 0 10 0
   2: 0100007F:0BB9 0100007F:1F90 01 00000000:00000000 00:00000000 00000000  1000        0 12347 1 0000000000000000 100 0 0 10 0
   3: 0A00020F:0FA0 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 12348 1 0000000000000000 100 0 0 10 0
`

func TestParseTCPListeners(t *testing.T) {
	listeners, err := parseTCPListeners(strings.NewReader(sampleProcTCP))
	if err != nil {
		t.Fatal(err)
	}

	// 0.0.0.0:8080 -> dial loopback
	if got := listeners[8080]; got != "127.0.0.1" {
		t.Errorf("port 8080: got %q, want 127.0.0.1", got)
	}
	// 127.0.0.1:3000 -> dial loopback
	if got := listeners[3000]; got != "127.0.0.1" {
		t.Errorf("port 3000: got %q, want 127.0.0.1", got)
	}
	// 15.2.0.10:4000 (interface-specific, little-endian 0A00020F) -> dial that IP
	if got := listeners[4000]; got != "15.2.0.10" {
		t.Errorf("port 4000: got %q, want 15.2.0.10", got)
	}
	// ESTABLISHED entry must be ignored
	if _, ok := listeners[3001]; ok {
		t.Error("ESTABLISHED socket on port 3001 should not be listed")
	}
	if len(listeners) != 3 {
		t.Errorf("expected 3 listeners, got %d: %v", len(listeners), listeners)
	}
}

func TestForwarderPipesIPv6ToIPv4(t *testing.T) {
	// IPv4-only echo server on a random port
	echo, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				c.Write(buf[:n])
			}(c)
		}
	}()
	port := echo.Addr().(*net.TCPAddr).Port

	f := &dualStackForwarder{listeners: make(map[int]net.Listener)}
	f.reconcile(map[int]string{port: "127.0.0.1"})
	if len(f.listeners) != 1 {
		t.Fatalf("expected 1 forwarder, got %d", len(f.listeners))
	}

	// Reach the IPv4 server over IPv6
	conn, err := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", port), 2*time.Second)
	if err != nil {
		t.Fatalf("dial over IPv6 failed: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if string(buf[:n]) != "ping" {
		t.Errorf("got %q, want %q", buf[:n], "ping")
	}

	// Listener disappears -> forwarder is closed
	f.reconcile(map[int]string{})
	if len(f.listeners) != 0 {
		t.Errorf("expected 0 forwarders after reconcile, got %d", len(f.listeners))
	}
	if _, err := net.DialTimeout("tcp6", fmt.Sprintf("[::1]:%d", port), 500*time.Millisecond); err == nil {
		t.Error("forwarder port should be closed after reconcile")
	}
}

func TestScanIPv4OnlyListenersLive(t *testing.T) {
	if _, err := os.Stat(procTCP4Path); err != nil {
		t.Skip("/proc/net/tcp not available on this platform")
	}

	// IPv4-only listener must be detected
	v4only, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer v4only.Close()
	v4Port := v4only.Addr().(*net.TCPAddr).Port

	// Dual-stack listener must be excluded
	dual, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer dual.Close()
	dualPort := dual.Addr().(*net.TCPAddr).Port

	want, err := scanIPv4OnlyListeners(nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := want[v4Port]; got != "127.0.0.1" {
		t.Errorf("IPv4-only port %d: got %q, want 127.0.0.1", v4Port, got)
	}
	if _, ok := want[dualPort]; ok {
		t.Errorf("dual-stack port %d should be excluded", dualPort)
	}
}
