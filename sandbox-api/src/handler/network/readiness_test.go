package network

import (
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"testing"
	"time"
)

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort int
		wantOK   bool
	}{
		{"0.0.0.0:3000", "0.0.0.0", 3000, true},
		{"127.0.0.1:8080", "127.0.0.1", 8080, true},
		{"*:3000", "*", 3000, true},
		{"[::]:3000", "::", 3000, true},
		{":::3000", "::", 3000, true},
		{"[::1]:3000", "::1", 3000, true},
		{"[2001:db8::1]:443", "2001:db8::1", 443, true},
		{"0.0.0.0:*", "", 0, false}, // wildcard peer port is not numeric
		{"noport", "", 0, false},
	}
	for _, c := range cases {
		host, port, ok := splitHostPort(c.in)
		if ok != c.wantOK || host != c.wantHost || port != c.wantPort {
			t.Errorf("splitHostPort(%q) = (%q, %d, %v), want (%q, %d, %v)",
				c.in, host, port, ok, c.wantHost, c.wantPort, c.wantOK)
		}
	}
}

func TestIsRoutableListener(t *testing.T) {
	cases := []struct {
		name string
		p    *PortInfo
		want bool
	}{
		{"nil", nil, false},
		{"ipv4 wildcard listen", &PortInfo{LocalAddr: "0.0.0.0", State: "LISTEN"}, true},
		{"ipv6 wildcard listen", &PortInfo{LocalAddr: "::", State: "LISTEN"}, true},
		{"star wildcard listen", &PortInfo{LocalAddr: "*", State: "LISTEN"}, true},
		{"concrete routable ip", &PortInfo{LocalAddr: "10.0.0.5", State: "LISTEN"}, true},
		{"empty state treated as listen", &PortInfo{LocalAddr: "0.0.0.0", State: ""}, true},
		{"ipv4 loopback", &PortInfo{LocalAddr: "127.0.0.1", State: "LISTEN"}, false},
		{"ipv6 loopback", &PortInfo{LocalAddr: "::1", State: "LISTEN"}, false},
		{"established not listen", &PortInfo{LocalAddr: "0.0.0.0", State: "ESTAB"}, false},
	}
	for _, c := range cases {
		if got := IsRoutableListener(c.p); got != c.want {
			t.Errorf("%s: IsRoutableListener = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestIsPortReadySubtreeAttribution verifies the core ENG-4284 fix: when a
// command is wrapped in a shell (`sh -c "... && server"`), the listening socket
// is owned by a child PID, yet IsPortReady(shellPID, port) must still detect it.
func TestIsPortReadySubtreeAttribution(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("subtree attribution via ss/pgrep is exercised on linux")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	port := 38731
	// The listener is a child of the shell: the shell PID owns no socket.
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("sleep 0.3 && exec python3 -m http.server %d --bind 0.0.0.0", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	shellPID := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if IsPortReady(shellPID, port) {
			return // success: readiness detected via the child's socket
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("IsPortReady(%d, %d) never became true for shell-wrapped child listener", shellPID, port)
}

// TestIsPortReadyRejectsLoopbackOnly verifies that a workload bound only to
// loopback is NOT reported ready, since the edge gateway cannot reach it — even
// though a bare 127.0.0.1 connect would succeed.
func TestIsPortReadyRejectsLoopbackOnly(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("relies on ss listener enumeration on linux")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}

	port := 38732
	cmd := exec.Command("python3", "-m", "http.server", strconv.Itoa(port), "--bind", "127.0.0.1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server: %v", err)
	}
	pid := cmd.Process.Pid
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	// Wait until the loopback socket is actually connectable.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) && !isPortOpenByConnect(port) {
		time.Sleep(100 * time.Millisecond)
	}
	if !isPortOpenByConnect(port) {
		t.Skip("loopback server never came up; skipping")
	}

	if IsPortReady(pid, port) {
		t.Fatalf("IsPortReady returned true for a loopback-only bind; gateway could not reach it")
	}
}
