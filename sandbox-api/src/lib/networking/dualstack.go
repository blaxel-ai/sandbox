package networking

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// Dual-stack auto-forwarder.
//
// Preview traffic reaches the sandbox over IPv6: cluster-gateway dials the
// pod's IPv6 address directly, bypassing sandbox-api. Any process listening
// on an IPv4-only address (0.0.0.0, 127.0.0.1, ...) is therefore unreachable
// from outside. Sandboxes run unreviewed agent-written code, so we cannot
// rely on servers binding '::'. This scanner watches /proc/net/tcp{,6} and,
// for every IPv4-only listener, opens an IPv6-only [::]:port listener that
// pipes traffic to the IPv4 socket.
//
// ponytail: TCP only; preview traffic is HTTP/WS. Add UDP if a real use case shows up.

const dualStackScanInterval = 500 * time.Millisecond

var (
	procTCP4Path = "/proc/net/tcp"
	procTCP6Path = "/proc/net/tcp6"
)

type dualStackForwarder struct {
	listeners map[int]net.Listener // port -> our [::]:port listener
}

// StartDualStackForwarder starts the background scan loop. No-op on systems
// without /proc/net/tcp (non-Linux) or when explicitly disabled.
func StartDualStackForwarder(ctx context.Context) {
	if os.Getenv("BL_DISABLE_IPV6_FORWARDER") == "true" {
		logrus.Info("Dual-stack IPv6 forwarder disabled via BL_DISABLE_IPV6_FORWARDER")
		return
	}
	if _, err := os.Stat(procTCP4Path); err != nil {
		logrus.Debug("Dual-stack IPv6 forwarder: /proc/net/tcp not available, skipping")
		return
	}
	f := &dualStackForwarder{listeners: make(map[int]net.Listener)}
	go f.run(ctx)
	logrus.Info("Dual-stack IPv6 forwarder started")
}

func (f *dualStackForwarder) run(ctx context.Context) {
	ticker := time.NewTicker(dualStackScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			for port, l := range f.listeners {
				l.Close()
				delete(f.listeners, port)
			}
			return
		case <-ticker.C:
			owned := make(map[int]bool, len(f.listeners))
			for port := range f.listeners {
				owned[port] = true
			}
			want, err := scanIPv4OnlyListeners(owned)
			if err != nil {
				logrus.WithError(err).Debug("Dual-stack forwarder: scan failed")
				continue
			}
			f.reconcile(want)
		}
	}
}

// scanIPv4OnlyListeners returns port -> dial target IP for every TCP port
// that has an IPv4 listener but no IPv6 listener. Ports in ownedPorts are
// forwarder listeners we created ourselves: they must not count as IPv6
// coverage, otherwise each scan would see its own listener and close it.
func scanIPv4OnlyListeners(ownedPorts map[int]bool) (map[int]string, error) {
	v4File, err := os.Open(procTCP4Path)
	if err != nil {
		return nil, err
	}
	defer v4File.Close()
	v4, err := parseTCPListeners(v4File)
	if err != nil {
		return nil, err
	}

	v6File, err := os.Open(procTCP6Path)
	if err == nil {
		defer v6File.Close()
		v6, err := parseTCPListeners(v6File)
		if err != nil {
			return nil, err
		}
		for port := range v6 {
			// A foreign v6 listener exists (wildcard '::' is dual-stack by
			// default, and a specific v6 bind would make our wildcard bind
			// fail). Our own listeners can't coexist with foreign ones on the
			// same port, so ownedPorts membership is unambiguous.
			if !ownedPorts[port] {
				delete(v4, port)
			}
		}
	}

	return v4, nil
}

// parseTCPListeners parses /proc/net/tcp{,6} content and returns
// port -> local IP (dial target) for sockets in LISTEN state.
func parseTCPListeners(r io.Reader) (map[int]string, error) {
	listeners := make(map[int]string)
	content, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	for _, line := range strings.Split(string(content), "\n")[1:] { // skip header
		fields := strings.Fields(line)
		// sl local_address rem_address st ...
		if len(fields) < 4 || fields[3] != "0A" { // 0A = TCP_LISTEN
			continue
		}
		addrPort := strings.Split(fields[1], ":")
		if len(addrPort) != 2 {
			continue
		}
		port64, err := strconv.ParseInt(addrPort[1], 16, 32)
		if err != nil || port64 == 0 {
			continue
		}
		listeners[int(port64)] = hexToDialIP(addrPort[0])
	}

	return listeners, nil
}

// hexToDialIP converts the kernel's hex-encoded local address to the IP we
// should dial. Wildcard (0.0.0.0) and loopback binds are dialed via 127.0.0.1;
// interface-specific binds are dialed on their own address. IPv6 entries
// (32 hex chars) are only used for port-set membership, so any value works.
func hexToDialIP(h string) string {
	if len(h) != 8 {
		return "127.0.0.1"
	}
	b, err := hex.DecodeString(h)
	if err != nil {
		return "127.0.0.1"
	}
	// /proc/net/tcp stores IPv4 addresses in little-endian order
	ip := net.IPv4(b[3], b[2], b[1], b[0])
	if ip.IsUnspecified() || ip.IsLoopback() {
		return "127.0.0.1"
	}
	return ip.String()
}

// reconcile closes forwarders whose IPv4 listener went away and opens
// forwarders for newly detected IPv4-only listeners. Runs on a single
// goroutine, so no locking is needed on the map.
func (f *dualStackForwarder) reconcile(want map[int]string) {
	for port, l := range f.listeners {
		if _, ok := want[port]; !ok {
			l.Close()
			delete(f.listeners, port)
			logrus.Debugf("Dual-stack forwarder: closed [::]:%d", port)
		}
	}

	for port, targetIP := range want {
		if _, ok := f.listeners[port]; ok {
			continue
		}
		// "tcp6" makes Go set IPV6_V6ONLY=1, so this never conflicts with
		// the existing IPv4 socket on the same port.
		l, err := net.Listen("tcp6", fmt.Sprintf("[::]:%d", port))
		if err != nil {
			logrus.WithError(err).Debugf("Dual-stack forwarder: cannot bind [::]:%d", port)
			continue
		}
		f.listeners[port] = l
		target := net.JoinHostPort(targetIP, strconv.Itoa(port))
		go serveForwarder(l, target)
		logrus.Infof("Dual-stack forwarder: [::]:%d -> %s", port, target)
	}
}

func serveForwarder(l net.Listener, target string) {
	for {
		conn, err := l.Accept()
		if err != nil {
			return // listener closed by reconcile/shutdown
		}
		go forwardConn(conn, target)
	}
}

func forwardConn(src net.Conn, target string) {
	dst, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		src.Close()
		return
	}

	var wg sync.WaitGroup
	wg.Add(2)
	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite() // propagate half-close so streaming servers behave
		}
	}
	go cp(dst, src)
	go cp(src, dst)
	wg.Wait()
	src.Close()
	dst.Close()
}
