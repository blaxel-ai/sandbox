package networking

import (
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/sirupsen/logrus"
)

// resolvConfPath is the resolver configuration the tunnel's nameservers are
// injected into. A variable so tests do not touch the host's own file.
var resolvConfPath = "/etc/resolv.conf"

// maxNameservers is glibc's MAXNS: nameservers past the third are ignored, so
// injecting more would silently push the existing ones out of use.
const maxNameservers = 3

// resolvGuard remembers the resolver configuration as it was before the tunnel
// injected its own nameservers, so teardown can put it back verbatim.
type resolvGuard struct {
	path     string
	original []byte
	mode     os.FileMode
}

// applyTunnelDNS puts the tunnel's nameservers ahead of the existing ones.
// A sandbox reachable only over IPv6 is given a DNS64 resolver by the host,
// which answers AAAA queries and nothing else; once the tunnel carries a family
// natively, resolution for that family needs a resolver that answers for it.
//
// The existing nameservers are kept as fallbacks after the injected ones, up to
// glibc's three-nameserver limit. Nothing is written when the file already
// starts with the requested servers, so a restart is a no-op.
func applyTunnelDNS(path string, servers []string) (*resolvGuard, error) {
	if len(servers) == 0 {
		return nil, nil
	}

	original, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}

	mode := os.FileMode(0o444)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	updated := renderResolvConf(original, servers)
	if string(updated) == string(original) {
		return &resolvGuard{path: path, original: original, mode: mode}, nil
	}

	if err := writePreservingMode(path, updated, mode); err != nil {
		return nil, err
	}

	logrus.WithFields(logrus.Fields{
		"path":        path,
		"nameservers": servers,
	}).Info("Injected the tunnel's nameservers into the resolver configuration")

	return &resolvGuard{path: path, original: original, mode: mode}, nil
}

// restore puts back the resolver configuration from before the injection.
func (g *resolvGuard) restore() {
	if g == nil {
		return
	}
	if err := writePreservingMode(g.path, g.original, g.mode); err != nil {
		logrus.WithError(err).WithField("path", g.path).Warn("Failed to restore the resolver configuration")
		return
	}
	logrus.WithField("path", g.path).Info("Restored the resolver configuration")
}

// writePreservingMode rewrites a file the host deliberately left read-only (a
// tenant must not be able to point the sandbox at its own resolver), making it
// writable only for the length of the write.
func writePreservingMode(path string, content []byte, mode os.FileMode) error {
	if err := os.Chmod(path, 0o644); err != nil {
		return fmt.Errorf("failed to make %s writable: %w", path, err)
	}
	defer func() {
		if err := os.Chmod(path, mode); err != nil {
			logrus.WithError(err).WithField("path", path).Warn("Failed to restore resolver configuration permissions")
		}
	}()

	if err := os.WriteFile(path, content, mode); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// renderResolvConf returns the resolver configuration with servers as its first
// nameservers, the pre-existing ones kept in order behind them and every other
// directive (options, search, comments) left untouched.
func renderResolvConf(existing []byte, servers []string) []byte {
	var out strings.Builder
	written := make(map[string]bool, len(servers))

	for _, server := range servers {
		if written[server] {
			continue
		}
		out.WriteString(fmt.Sprintf("nameserver %s\n", server))
		written[server] = true
	}

	for _, line := range strings.Split(string(existing), "\n") {
		server, isNameserver := nameserverOf(line)
		switch {
		case !isNameserver:
			// Preserve the trailing newline of the original rather than
			// appending an empty line for it.
			if line != "" {
				out.WriteString(line + "\n")
			}
		case written[server] || len(written) >= maxNameservers:
			// A duplicate, or a nameserver glibc would never consult anyway.
		default:
			out.WriteString(line + "\n")
			written[server] = true
		}
	}

	return []byte(out.String())
}

// nameserverOf reports the address of a resolv.conf nameserver line.
func nameserverOf(line string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "nameserver" {
		return "", false
	}
	return fields[1], true
}

// tunnelledDNS returns the configured nameservers whose address family the
// tunnel actually carries. An IPv4 resolver is only reachable once IPv4 is
// routed into the tunnel; injecting it any earlier would break resolution
// instead of fixing it.
func tunnelledDNS(servers []string, tunnelled []*net.IPNet) []string {
	usable := make([]string, 0, len(servers))
	for _, server := range servers {
		ip := net.ParseIP(server)
		if ip == nil {
			logrus.WithField("nameserver", server).Warn("Ignoring unparsable nameserver")
			continue
		}
		if !routedThroughTunnel(ip, tunnelled) {
			logrus.WithField("nameserver", server).
				Info("Nameserver is not routed through the tunnel, not injecting it")
			continue
		}
		usable = append(usable, server)
	}
	return usable
}

// routedThroughTunnel reports whether an address is covered by one of the
// prefixes routed into the tunnel.
func routedThroughTunnel(ip net.IP, tunnelled []*net.IPNet) bool {
	for _, dst := range tunnelled {
		if dst.Contains(ip) {
			return true
		}
	}
	return false
}
