package process

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"
)

var (
	// ErrStdinNotEnabled is returned when writing to a process started without stdin: true.
	ErrStdinNotEnabled = errors.New("process was started without stdin; start it with \"stdin\": true")
	// ErrStdinClosed is returned once stdin has been closed, the process has
	// exited, or after a sandbox-api restart: the pipe lived in the previous
	// sandbox-api process and died with it.
	ErrStdinClosed = errors.New("stdin is closed; restart the process to get a new one")
	// ErrStdinStalled is returned when the child stops reading and the pipe
	// buffer stays full for stdinWriteTimeout. Part of the body may have been
	// written; the caller decides whether the stream is still usable.
	ErrStdinStalled = errors.New("process is not reading its stdin")
)

// stdinWriteTimeout bounds one write, and with it the time the pipe lock is
// held: a child that stops reading must not pin an HTTP goroutine and every
// later writer for good. A var so tests can shorten it.
var stdinWriteTimeout = 10 * time.Second

// stdinPipe is the parent's write end of a process's stdin. A plain pipe, not a
// FIFO on disk, so it does not survive a sandbox-api restart: the child sees EOF,
// which is the documented shutdown path for stdio protocols such as MCP.
// ponytail: swap for a mkfifo next to the log files if stdin must outlive sandbox-api.
type stdinPipe struct {
	mu sync.Mutex
	w  io.WriteCloser
}

// attachStdin gives cmd a stdin pipe when the process asked for one. Called on
// every run, so a restart-on-failure gets a fresh pipe; exec.Cmd.Wait closes the
// previous one when the old run exits.
func attachStdin(cmd *exec.Cmd, p *ProcessInfo) error {
	if !p.Stdin {
		return nil
	}
	w, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	p.stdin.mu.Lock()
	p.stdin.w = w
	p.stdin.mu.Unlock()
	return nil
}

// WriteStdin writes data verbatim to the process's stdin. The whole body goes
// out under one lock, so concurrent writers never interleave inside a message.
func (pm *ProcessManager) WriteStdin(identifier string, data []byte) error {
	p, err := pm.stdinTarget(identifier)
	if err != nil {
		return err
	}
	p.stdin.mu.Lock()
	defer p.stdin.mu.Unlock()
	if p.stdin.w == nil {
		return ErrStdinClosed
	}
	if f, ok := p.stdin.w.(*os.File); ok {
		_ = f.SetWriteDeadline(time.Now().Add(stdinWriteTimeout))
		defer f.SetWriteDeadline(time.Time{})
	}
	_, err = p.stdin.w.Write(data)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, os.ErrClosed):
		// exec.Cmd.Wait closed the pipe when the child exited.
		p.stdin.w = nil
		return ErrStdinClosed
	case errors.Is(err, os.ErrDeadlineExceeded):
		return ErrStdinStalled
	default:
		return fmt.Errorf("failed to write to stdin: %w", err)
	}
}

// CloseStdin sends EOF to the process. Idempotent.
func (pm *ProcessManager) CloseStdin(identifier string) error {
	p, err := pm.stdinTarget(identifier)
	if err != nil {
		return err
	}
	p.stdin.mu.Lock()
	defer p.stdin.mu.Unlock()
	if p.stdin.w == nil {
		return nil
	}
	err = p.stdin.w.Close()
	p.stdin.w = nil
	if errors.Is(err, os.ErrClosed) {
		// Already closed by exec.Cmd.Wait when the child exited: still EOF.
		return nil
	}
	return err
}

func (pm *ProcessManager) stdinTarget(identifier string) (*ProcessInfo, error) {
	p, exists := pm.GetProcessByIdentifier(identifier)
	if !exists {
		return nil, fmt.Errorf("process with identifier %s not found", identifier)
	}
	if !p.Stdin {
		return nil, ErrStdinNotEnabled
	}
	return p, nil
}
