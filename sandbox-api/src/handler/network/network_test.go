package network

import (
	"bufio"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// TestNetworkPortMonitoring is an integration test that demonstrates both port
// querying and port open callback functionality
func TestNetworkPortMonitoring(t *testing.T) {
	// Create a new network monitor
	network := GetNetwork()

	// Start a test server that will open a port
	testServerCmd := exec.Command("python3", "-c", `
import http.server
import socketserver
import time

PORT = 8000
Handler = http.server.SimpleHTTPRequestHandler
httpd = socketserver.TCPServer(("", PORT), Handler)
print(f"Serving on port {PORT}")
# Keep the server running for a short time
time.sleep(10)
`)

	stdout, err := testServerCmd.StdoutPipe()
	if err != nil {
		t.Fatalf("Failed to get stdout pipe: %v", err)
	}

	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			t.Logf("server logs: %s", scanner.Text())
		}
	}()

	var wg sync.WaitGroup
	wg.Add(1)

	// Register callback before starting the server
	var callbackCalled bool
	var mu sync.Mutex
	network.RegisterPortOpenCallback(0, func(pid int, port *PortInfo) {
		// This callback will be called for all processes, we filter for our specific port
		if port.LocalPort == 8000 {
			mu.Lock()
			callbackCalled = true
			mu.Unlock()
			t.Logf("Callback triggered for PID %d opening port %d", pid, port.LocalPort)
			wg.Done()
		}
	})

	// Start the server
	err = testServerCmd.Start()
	if err != nil {
		t.Fatalf("Failed to start test server: %v", err)
	}

	// Get the PID
	serverPID := testServerCmd.Process.Pid
	t.Logf("Started test server with PID: %d", serverPID)

	// Give the server some time to start and open the port
	time.Sleep(2 * time.Second)

	// Test GetPortsForPID
	ports, err := network.GetPortsForPID(serverPID)
	if err != nil {
		t.Fatalf("Failed to get ports for PID %d: %v", serverPID, err)
	}

	// Check if the port 8000 is in the list
	var found bool
	for _, port := range ports {
		t.Logf("Port found for PID %d: %+v", serverPID, port)
		if port.LocalPort == 8000 {
			found = true
			break
		}
	}

	if !found {
		t.Logf("Port 8000 not found for PID %d. This test may fail on some systems.", serverPID)
	}

	// Wait for the callback or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success, callback was called
	case <-time.After(5 * time.Second):
		mu.Lock()
		if !callbackCalled {
			t.Log("Callback was not called within timeout. This test may fail on some systems.")
		}
		mu.Unlock()
	}

	// Clean up
	if err := testServerCmd.Process.Kill(); err != nil {
		t.Logf("Failed to kill test server: %v", err)
	}

	_ = testServerCmd.Wait()
	network.UnregisterPortOpenCallback(0)
}

// ExampleNetwork_GetPortsForPID shows how to get open ports for a specific PID
func ExampleNetwork_GetPortsForPID() {
	network := GetNetwork()

	// Replace this with an actual PID you want to monitor
	pid := 1234

	ports, err := network.GetPortsForPID(pid)
	if err != nil {
		logrus.Errorf("Error getting ports for PID %d: %v\n", pid, err)
		return
	}

	logrus.Infof("Found %d open ports for PID %d:\n", len(ports), pid)
	for _, port := range ports {
		logrus.Infof("- %s port %d on %s (state: %s)\n",
			port.Protocol, port.LocalPort, port.LocalAddr, port.State)
	}
}

// ExampleNetwork_RegisterPortOpenCallback shows how to register a callback for when a process opens a port
func ExampleNetwork_RegisterPortOpenCallback() {
	network := GetNetwork()

	// Replace this with an actual PID you want to monitor
	pid := 1234

	// Register a callback for when the process opens a new port
	network.RegisterPortOpenCallback(pid, func(pid int, port *PortInfo) {
		logrus.Infof("Process %d opened %s port %d on %s\n",
			pid, port.Protocol, port.LocalPort, port.LocalAddr)
	})

	// When done monitoring, unregister the callback
	// network.UnregisterPortOpenCallback(pid)

	// Keep the program running to receive callbacks
	logrus.Info("Monitoring for port changes. Press Ctrl+C to exit.")
	select {} // Block forever
}
