package network

import (
	"bufio"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// PortInfo represents information about an open port
type PortInfo struct {
	PID         int    `json:"pid"`
	Protocol    string `json:"protocol"` // tcp or udp
	LocalAddr   string `json:"localAddr"`
	LocalPort   int    `json:"localPort"`
	RemoteAddr  string `json:"remoteAddr"`
	RemotePort  int    `json:"remotePort"`
	State       string `json:"state"`
	ProcessName string `json:"processName"`
}

// PortOpenCallback is a function that gets called when a process opens a new port
type PortOpenCallback func(pid int, port *PortInfo)

// Network provides functionality for monitoring network connections
type Network struct {
	portsByPID     map[int]map[int]*PortInfo  // PID -> Port -> PortInfo
	callbacks      map[int][]PortOpenCallback // PID -> list of callbacks
	monitoredPIDs  map[int]bool
	stopMonitoring chan bool
	isMonitoring   bool
	mutex          sync.RWMutex
}

// Global process manager instance
var (
	network     *Network
	networkOnce sync.Once
)

func GetNetwork() *Network {
	networkOnce.Do(func() {
		network = &Network{
			portsByPID:     make(map[int]map[int]*PortInfo),
			callbacks:      make(map[int][]PortOpenCallback),
			monitoredPIDs:  make(map[int]bool),
			stopMonitoring: make(chan bool),
			isMonitoring:   false,
		}
	})

	return network
}

// GetPortsForPID returns all open ports for a specific PID
func (n *Network) GetPortsForPID(pid int) ([]*PortInfo, error) {
	n.mutex.RLock()
	defer n.mutex.RUnlock()

	// Update ports information before returning
	if err := n.updatePortsForPID(pid); err != nil {
		return nil, err
	}

	portMap, exists := n.portsByPID[pid]
	if !exists {
		return []*PortInfo{}, nil
	}

	ports := make([]*PortInfo, 0, len(portMap))
	for _, port := range portMap {
		ports = append(ports, port)
	}

	return ports, nil
}

// RegisterPortOpenCallback registers a callback function that will be called when the specified PID opens a new port
func (n *Network) RegisterPortOpenCallback(pid int, callback PortOpenCallback) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	// Initialize callbacks slice if it doesn't exist
	if _, exists := n.callbacks[pid]; !exists {
		n.callbacks[pid] = make([]PortOpenCallback, 0)
	}

	n.callbacks[pid] = append(n.callbacks[pid], callback)
	n.monitoredPIDs[pid] = true

	// Start monitoring if not already doing so
	if !n.isMonitoring {
		go n.startMonitoring()
	}
}

// UnregisterPortOpenCallback removes all callbacks for a specific PID
func (n *Network) UnregisterPortOpenCallback(pid int) {
	n.mutex.Lock()
	defer n.mutex.Unlock()

	delete(n.callbacks, pid)
	delete(n.monitoredPIDs, pid)

	// If no more PIDs to monitor, stop monitoring
	if len(n.monitoredPIDs) == 0 && n.isMonitoring {
		n.stopMonitoring <- true
	}
}

// startMonitoring starts a goroutine that periodically checks for new open ports
func (n *Network) startMonitoring() {
	n.mutex.Lock()
	if n.isMonitoring {
		n.mutex.Unlock()
		return
	}

	n.isMonitoring = true
	n.mutex.Unlock()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			func() {
				n.mutex.Lock()
				defer n.mutex.Unlock()

				for pid := range n.monitoredPIDs {
					oldPorts := n.portsByPID[pid]
					if err := n.updatePortsForPID(pid); err != nil {
						logrus.Errorf("Error updating ports for PID %d: %v\n", pid, err)
						continue
					}

					// Check for new ports
					newPorts := n.portsByPID[pid]
					for portNum, portInfo := range newPorts {
						if _, exists := oldPorts[portNum]; !exists {
							// New port detected, trigger callbacks
							for _, callback := range n.callbacks[pid] {
								go callback(pid, portInfo)
							}
						}
					}
				}
			}()
		case <-n.stopMonitoring:
			n.mutex.Lock()
			n.isMonitoring = false
			n.mutex.Unlock()
			return
		}
	}
}

// updatePortsForPID updates the internal cache of ports for a specific PID
func (n *Network) updatePortsForPID(pid int) error {
	ports, err := getOpenPortsForPID(pid)
	if err != nil {
		return err
	}

	// Initialize or clear the port map for this PID
	if _, exists := n.portsByPID[pid]; !exists {
		n.portsByPID[pid] = make(map[int]*PortInfo)
	}

	// Update with new port information
	newPortMap := make(map[int]*PortInfo)
	for _, port := range ports {
		newPortMap[port.LocalPort] = port
	}
	n.portsByPID[pid] = newPortMap

	return nil
}

// getOpenPortsForPID uses platform-specific commands to get open ports for a specific PID
func getOpenPortsForPID(pid int) ([]*PortInfo, error) {
	// Use different commands based on the operating system
	if runtime.GOOS == "darwin" {
		return getPortsUsingLsof(pid)
	}

	// For Linux systems, try ss command first (newer and more efficient)
	portsInfo, err := getPortsUsingSS(pid)
	if err == nil {
		return portsInfo, nil
	}

	// Fall back to netstat if ss fails
	return getPortsUsingNetstat(pid)
}

// getPortsUsingSS uses the 'ss' command to get port information for a specific PID
func getPortsUsingSS(pid int) ([]*PortInfo, error) {
	// Run ss command: ss -tunap | grep <pid>
	cmd := exec.Command("ss", "-tunap")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Parse the output line by line
	scanner := bufio.NewScanner(stdout)
	portsInfo := make([]*PortInfo, 0)
	pidStr := fmt.Sprintf("pid=%d", pid)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, pidStr) {
			continue
		}

		// Parse the line to extract port information
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}

		protocol := strings.ToLower(fields[0])
		state := fields[1]

		// Parse local address
		localAddrParts := strings.Split(fields[4], ":")
		if len(localAddrParts) < 2 {
			continue
		}

		localAddr := localAddrParts[0]
		localPort, err := strconv.Atoi(localAddrParts[1])
		if err != nil {
			continue
		}

		// Parse remote address if available
		var remoteAddr string
		var remotePort int
		if len(fields) > 5 {
			remoteAddrParts := strings.Split(fields[5], ":")
			if len(remoteAddrParts) >= 2 {
				remoteAddr = remoteAddrParts[0]
				remotePort, _ = strconv.Atoi(remoteAddrParts[1])
			}
		}

		// Extract process name if available
		processName := ""
		for _, field := range fields {
			if strings.Contains(field, "\"") {
				parts := strings.Split(field, "\"")
				if len(parts) >= 2 {
					processName = parts[1]
				}
				break
			}
		}

		portInfo := &PortInfo{
			PID:         pid,
			Protocol:    protocol,
			LocalAddr:   localAddr,
			LocalPort:   localPort,
			RemoteAddr:  remoteAddr,
			RemotePort:  remotePort,
			State:       state,
			ProcessName: processName,
		}

		portsInfo = append(portsInfo, portInfo)
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return portsInfo, nil
}

// getPortsUsingLsof uses the 'lsof' command to get port information for a specific PID (macOS)
func getPortsUsingLsof(pid int) ([]*PortInfo, error) {
	// Run lsof command: lsof -iTCP -iUDP -n -P -a -p <pid>
	cmd := exec.Command("lsof", "-iTCP", "-iUDP", "-n", "-P", "-a", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		// lsof returns exit code 1 if no files are found, which is not an error for our use case
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return []*PortInfo{}, nil
		}
		return nil, err
	}

	// Parse the output line by line
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	portsInfo := make([]*PortInfo, 0)

	i := 0
	for scanner.Scan() {
		if i == 0 {
			// Skip the header
			continue
		}
		line := scanner.Text()
		fields := strings.Fields(line)

		// lsof output format:
		// COMMAND   PID   USER   FD   TYPE     DEVICE SIZE/OFF NODE NAME
		// Example: node    12345 user   23u  IPv4 0x1234     0t0  TCP *:8000 (LISTEN)
		if len(fields) < 9 {
			continue
		}

		// Get the protocol from TYPE field (fields[4])
		protocol := ""
		if strings.Contains(fields[4], "IPv4") || strings.Contains(fields[4], "IPv6") {
			// Get protocol from NAME field (fields[8])
			if strings.Contains(fields[8], "TCP") {
				protocol = "tcp"
			} else if strings.Contains(fields[8], "UDP") {
				protocol = "udp"
			}
		}

		if protocol == "" {
			continue
		}

		// Parse NAME field for address and port
		// Format examples:
		// *:8000 (LISTEN)
		// 127.0.0.1:8000->127.0.0.1:54321 (ESTABLISHED)
		nameField := fields[8]

		// Extract state if present
		state := ""
		if strings.Contains(line, "(LISTEN)") {
			state = "LISTEN"
		} else if strings.Contains(line, "(ESTABLISHED)") {
			state = "ESTABLISHED"
		} else if strings.Contains(line, "(CLOSE_WAIT)") {
			state = "CLOSE_WAIT"
		}

		// Parse local and remote addresses
		var localAddr, remoteAddr string
		var localPort, remotePort int

		// Check if it's a connection (contains ->)
		if strings.Contains(nameField, "->") {
			parts := strings.Split(nameField, "->")
			if len(parts) == 2 {
				// Parse local address
				localParts := strings.Split(parts[0], ":")
				if len(localParts) >= 2 {
					localAddr = strings.Join(localParts[:len(localParts)-1], ":")
					localPort, _ = strconv.Atoi(localParts[len(localParts)-1])
				}

				// Parse remote address
				remoteParts := strings.Split(parts[1], ":")
				if len(remoteParts) >= 2 {
					remoteAddr = strings.Join(remoteParts[:len(remoteParts)-1], ":")
					remotePort, _ = strconv.Atoi(remoteParts[len(remoteParts)-1])
				}
			}
		} else {
			// It's a listening port
			localParts := strings.Split(nameField, ":")
			if len(localParts) >= 2 {
				localAddr = strings.Join(localParts[:len(localParts)-1], ":")
				localPort, _ = strconv.Atoi(localParts[len(localParts)-1])

				// Convert * to 0.0.0.0 for consistency
				if localAddr == "*" {
					localAddr = "0.0.0.0"
				}
			}
		}

		if localPort == 0 {
			continue
		}

		// Get process name from COMMAND field
		processName := fields[0]

		portInfo := &PortInfo{
			PID:         pid,
			Protocol:    protocol,
			LocalAddr:   localAddr,
			LocalPort:   localPort,
			RemoteAddr:  remoteAddr,
			RemotePort:  remotePort,
			State:       state,
			ProcessName: processName,
		}

		portsInfo = append(portsInfo, portInfo)
		i++
	}

	return portsInfo, nil
}

// getPortsUsingNetstat uses the 'netstat' command to get port information for a specific PID
func getPortsUsingNetstat(pid int) ([]*PortInfo, error) {
	// Run netstat command: netstat -tunap | grep <pid>
	cmd := exec.Command("netstat", "-tunap")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	// Parse the output line by line
	scanner := bufio.NewScanner(stdout)
	portsInfo := make([]*PortInfo, 0)
	pidStr := fmt.Sprintf("%d/", pid)

	// Skip header lines
	for i := 0; i < 2 && scanner.Scan(); i++ {
		// Skip headers
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, pidStr) {
			continue
		}

		// Parse the line to extract port information
		fields := strings.Fields(line)
		if len(fields) < 7 {
			continue
		}

		protocol := strings.ToLower(fields[0])

		// Parse local address
		localAddrParts := strings.Split(fields[3], ":")
		if len(localAddrParts) < 2 {
			continue
		}

		localAddr := localAddrParts[0]
		localPort, err := strconv.Atoi(localAddrParts[len(localAddrParts)-1])
		if err != nil {
			continue
		}

		// Parse remote address
		remoteAddrParts := strings.Split(fields[4], ":")
		remoteAddr := remoteAddrParts[0]
		remotePort := 0
		if len(remoteAddrParts) >= 2 {
			remotePort, _ = strconv.Atoi(remoteAddrParts[len(remoteAddrParts)-1])
		}

		// Parse state
		state := fields[5]

		// Parse process name
		processNameParts := strings.Split(fields[6], "/")
		processName := ""
		if len(processNameParts) >= 2 {
			processName = processNameParts[1]
		}

		portInfo := &PortInfo{
			PID:         pid,
			Protocol:    protocol,
			LocalAddr:   localAddr,
			LocalPort:   localPort,
			RemoteAddr:  remoteAddr,
			RemotePort:  remotePort,
			State:       state,
			ProcessName: processName,
		}

		portsInfo = append(portsInfo, portInfo)
	}

	if err := cmd.Wait(); err != nil {
		return nil, err
	}

	return portsInfo, nil
}
