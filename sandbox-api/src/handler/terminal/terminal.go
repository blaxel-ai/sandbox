package terminal

import (
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

// TerminalSession represents an interactive terminal session with PTY
type TerminalSession struct {
	ptmx       *os.File
	processPid int // Store only PID to avoid FD leak (not *exec.Cmd)
	mu         sync.Mutex
	closed     bool
	closeCh    chan struct{}
	// shellDoneCh is closed when the shell process exits, even if child processes
	// still hold the PTY slave open. This allows detecting "exit" without waiting
	// for all children to close their inherited file descriptors.
	shellDoneCh chan struct{}
}

// findShell returns the best available shell
func findShell() string {
	// Check SHELL env first
	if shell := os.Getenv("SHELL"); shell != "" {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}

	// Try common shells in order of preference
	shells := []string{"/bin/zsh", "/bin/bash", "/bin/sh", "/bin/ash"}
	for _, shell := range shells {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}

	// Fallback
	return "/bin/sh"
}

// NewTerminalSession creates a new terminal session with the specified shell
func NewTerminalSession(shell string, workingDir string, env map[string]string, cols, rows uint16) (*TerminalSession, error) {
	if shell == "" {
		shell = findShell()
	}

	cmd := exec.Command(shell)

	if workingDir != "" {
		cmd.Dir = workingDir
	}

	// Build environment
	systemEnv := os.Environ()
	envOverrides := make(map[string]bool)
	for k := range env {
		envOverrides[k] = true
	}

	finalEnv := make([]string, 0, len(systemEnv)+len(env))
	for _, envVar := range systemEnv {
		idx := -1
		for i, c := range envVar {
			if c == '=' {
				idx = i
				break
			}
		}
		if idx > 0 {
			key := envVar[:idx]
			if !envOverrides[key] {
				finalEnv = append(finalEnv, envVar)
			}
		}
	}
	for k, v := range env {
		finalEnv = append(finalEnv, k+"="+v)
	}
	// Set TERM for proper terminal emulation only if not already set
	hasTerm := false
	for _, e := range finalEnv {
		if strings.HasPrefix(e, "TERM=") {
			hasTerm = true
			break
		}
	}
	if !hasTerm {
		finalEnv = append(finalEnv, "TERM=xterm-256color")
	}
	cmd.Env = finalEnv

	// NOTE: Do NOT set SysProcAttr here!
	// The pty.Start() function internally sets Setsid: true to create a new session,
	// which is required for proper PTY operation. Setting Setpgid would conflict with Setsid.

	// Start command with PTY
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
	if err != nil {
		return nil, err
	}

	// Store only the PID to avoid FD leak (workspace rule: never store *exec.Cmd in struct)
	pid := cmd.Process.Pid
	shellDoneCh := make(chan struct{})

	// Start a goroutine to wait for the process and release resources.
	// When the shell exits (e.g. user types "exit"), shellDoneCh is closed
	// to notify listeners even if child processes still hold the PTY slave open.
	go func() {
		_ = cmd.Wait()
		close(shellDoneCh)
		// Release process resources immediately after Wait() to close pidfd
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}()

	return &TerminalSession{
		ptmx:        ptmx,
		processPid:  pid,
		closeCh:     make(chan struct{}),
		shellDoneCh: shellDoneCh,
	}, nil
}

// Read reads from the PTY output
func (t *TerminalSession) Read(p []byte) (int, error) {
	return t.ptmx.Read(p)
}

// Write writes to the PTY input
func (t *TerminalSession) Write(p []byte) (int, error) {
	return t.ptmx.Write(p)
}

// Resize changes the terminal size
func (t *TerminalSession) Resize(cols, rows uint16) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return io.ErrClosedPipe
	}

	return pty.Setsize(t.ptmx, &pty.Winsize{
		Cols: cols,
		Rows: rows,
	})
}

// Close terminates the terminal session
func (t *TerminalSession) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true
	close(t.closeCh)

	// Close PTY first to signal EOF to readers
	if t.ptmx != nil {
		_ = t.ptmx.Close()
	}

	// Kill ALL processes in the terminal session.
	// Since pty.Start uses Setsid, the shell is the session leader (SID = processPid).
	// Background jobs started with & get their own process groups (different PGID),
	// so Kill(-pid, SIGKILL) only kills the shell's process group, not background jobs.
	// We must kill by session ID to catch everything.
	if t.processPid > 0 {
		killSessionProcesses(t.processPid)
	}

	return nil
}

// Done returns a channel that is closed when the session ends
func (t *TerminalSession) Done() <-chan struct{} {
	return t.closeCh
}

// ShellDone returns a channel that is closed when the shell process exits.
// This fires even if child processes still hold the PTY slave fd open.
func (t *TerminalSession) ShellDone() <-chan struct{} {
	return t.shellDoneCh
}

// IsClosed returns true if the session is closed
func (t *TerminalSession) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}

// killSessionProcesses kills all processes belonging to the given session ID.
// When pty.Start uses Setsid, the shell becomes the session leader (SID = its PID).
// Background jobs (started with &) may have different PGIDs but share the same SID.
// This walks /proc to find every process in the session and sends SIGKILL.
// Falls back to process-group kill on non-Linux systems where /proc is unavailable.
func killSessionProcesses(sessionID int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// Fallback for non-Linux: kill the process group only
		_ = syscall.Kill(-sessionID, syscall.SIGKILL)
		return
	}

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 {
			continue
		}

		// Read /proc/<pid>/stat: "pid (comm) state ppid pgrp session ..."
		data, err := os.ReadFile("/proc/" + entry.Name() + "/stat")
		if err != nil {
			continue
		}

		// The comm field (field 2) is in parens and can contain spaces/parens,
		// so find the last ')' and parse fields after it.
		s := string(data)
		closeIdx := strings.LastIndex(s, ")")
		if closeIdx < 0 {
			continue
		}
		// Fields after ")": state ppid pgrp session ...
		fields := strings.Fields(s[closeIdx+1:])
		// fields[0]=state, [1]=ppid, [2]=pgrp, [3]=session
		if len(fields) < 4 {
			continue
		}
		sid, err := strconv.Atoi(fields[3])
		if err != nil {
			continue
		}
		if sid == sessionID {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
