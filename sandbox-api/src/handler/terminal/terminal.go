package terminal

import (
	"io"
	"os"
	"os/exec"
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

	// Start a goroutine to wait for the process and release resources
	go func() {
		_ = cmd.Wait()
		// Release process resources immediately after Wait() to close pidfd
		if cmd.Process != nil {
			_ = cmd.Process.Release()
		}
	}()

	return &TerminalSession{
		ptmx:       ptmx,
		processPid: pid,
		closeCh:    make(chan struct{}),
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

	// Kill the process and its session using stored PID
	// Since pty.Start uses Setsid, the shell is the session leader
	// Killing the session leader will send SIGHUP to all processes in the session
	if t.processPid > 0 {
		// Try to kill the session (negative PID with SIGKILL)
		// This works because Setsid makes the process a session leader with pgid == pid
		_ = syscall.Kill(-t.processPid, syscall.SIGKILL)
		// The Wait() and Release() are handled by the goroutine started in NewTerminalSession
	}

	return nil
}

// Done returns a channel that is closed when the session ends
func (t *TerminalSession) Done() <-chan struct{} {
	return t.closeCh
}

// IsClosed returns true if the session is closed
func (t *TerminalSession) IsClosed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.closed
}
