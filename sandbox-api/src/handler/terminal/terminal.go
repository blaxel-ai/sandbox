package terminal

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/blaxel-ai/sandbox-api/src/lib/identity"
	"github.com/blaxel-ai/sandbox-api/src/lib/oom"
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

// terminalEnv builds the shell's environment: sandbox-api's own, then the
// caller's overrides, then what an xterm.js terminal needs.
//
// TERM always describes the PTY, i.e. the web terminal (xterm.js), unless the
// caller sets it. sandbox-api's own TERM describes wherever it was started
// (often TERM=linux), and passing it on gives vim, less and htop the Linux
// console's 8 colours, no mouse and Home/End codes that don't match.
//
// Without a locale the shell runs in POSIX/C and treats every byte as a
// character, so accented input and UTF-8 output break. LANG=C.UTF-8 is set
// only when no LC_ALL / LC_CTYPE / LANG is set and the image can honour it.
func terminalEnv(systemEnv []string, overrides map[string]string, utf8Locale bool) []string {
	finalEnv := make([]string, 0, len(systemEnv)+len(overrides)+3)
	set := make(map[string]bool, len(systemEnv)+len(overrides))
	for _, envVar := range systemEnv {
		key, _, ok := strings.Cut(envVar, "=")
		if !ok || key == "" || key == "TERM" {
			continue
		}
		if _, overridden := overrides[key]; overridden {
			continue
		}
		finalEnv = append(finalEnv, envVar)
		set[key] = true
	}
	for k, v := range overrides {
		finalEnv = append(finalEnv, k+"="+v)
		set[k] = true
	}
	if !set["TERM"] {
		finalEnv = append(finalEnv, "TERM=xterm-256color")
	}
	if !set["COLORTERM"] {
		finalEnv = append(finalEnv, "COLORTERM=truecolor")
	}
	if utf8Locale && !set["LC_ALL"] && !set["LC_CTYPE"] && !set["LANG"] {
		finalEnv = append(finalEnv, "LANG=C.UTF-8")
	}
	return finalEnv
}

// utf8LocaleAvailable reports whether LANG=C.UTF-8 works in this image: musl
// (Alpine) is always UTF-8, and glibc images ship it as /usr/lib/locale/C.utf8
// (Debian, Ubuntu, Fedora). Elsewhere setting it would only make the shell
// print a setlocale warning, so it is left unset.
func utf8LocaleAvailable() bool {
	for _, p := range []string{"/usr/lib/locale/C.utf8", "/usr/lib/locale/C.UTF-8"} {
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	musl, _ := filepath.Glob("/lib/ld-musl-*")
	return len(musl) > 0
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

	finalEnv := terminalEnv(os.Environ(), env, utf8LocaleAvailable())
	cmd.Env = identity.Get().DecorateEnv(finalEnv)

	// The PTY pair is opened here rather than through pty.StartWithSize so the
	// slave can be handed over to the workload user (as login(1) does) before
	// the shell starts. Setsid/Setctty are required for PTY operation and
	// conflict with Setpgid, so the credential travels in the same attributes.
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, err
	}
	defer tty.Close()

	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: cols, Rows: rows}); err != nil {
		ptmx.Close()
		return nil, err
	}

	if id := identity.Get(); id != nil {
		if err := tty.Chown(id.Uid, id.Gid); err != nil {
			ptmx.Close()
			return nil, err
		}
	}

	cmd.Stdin = tty
	cmd.Stdout = tty
	cmd.Stderr = tty
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:     true,
		Setctty:    true,
		Credential: identity.Get().Credential(),
	}

	if err := cmd.Start(); err != nil {
		ptmx.Close()
		return nil, err
	}

	// Store only the PID to avoid FD leak (workspace rule: never store *exec.Cmd in struct)
	pid := cmd.Process.Pid
	oom.PreferAsVictim(pid)
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
//
// Note: there is a theoretical TOCTOU window between reading /proc/<pid>/stat and
// sending the kill signal during which a PID could be reused. In practice the window
// is microseconds and the default Linux PID space (32768+) makes collisions
// negligible. We mitigate further by skipping our own process and kernel threads.
func killSessionProcesses(sessionID int) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		// Fallback for non-Linux: kill the process group only
		_ = syscall.Kill(-sessionID, syscall.SIGKILL)
		return
	}

	ownPid := os.Getpid()

	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 1 || pid == ownPid {
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

		// Skip kernel threads (parented by PID 0 or kthreadd PID 2)
		ppid, _ := strconv.Atoi(fields[1])
		if ppid == 0 || ppid == 2 {
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
