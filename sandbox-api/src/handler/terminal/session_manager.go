package terminal

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// maxBufferSize is the maximum number of bytes to keep in the output ring buffer.
	// When a client reconnects, this buffered output is replayed to restore the terminal state.
	maxBufferSize = 100 * 1024 // 100KB

	// subscriberChanSize is the buffer size for each subscriber's output channel.
	subscriberChanSize = 64

	// sessionCleanupInterval is how often the cleanup loop runs.
	sessionCleanupInterval = 30 * time.Second

	// sessionIdleTimeout is how long a session with no connected clients stays alive.
	sessionIdleTimeout = 10 * time.Minute

	// ansiReset resets all terminal text attributes. Prepended to buffer replays
	// to avoid inheriting stale formatting from truncated escape sequences.
	ansiReset = "\x1b[0m"
)

// Subscriber represents a connected WebSocket client receiving terminal output.
type Subscriber struct {
	Ch   chan []byte
	done chan struct{}
}

// ManagedSession wraps a TerminalSession with output buffering and lifecycle management.
// It keeps the terminal session alive across WebSocket disconnects so that background
// processes (like dev servers) continue running.
type ManagedSession struct {
	ID      string
	Session *TerminalSession

	// Output ring buffer and dead flag (both protected by bufMu).
	// dead is guarded by bufMu so that appendBuffer and markDead are
	// mutually exclusive — the buffer is guaranteed to be in its final
	// state once dead == true.
	bufMu  sync.Mutex
	buffer []byte
	dead   bool

	// Subscribers (protected by subMu)
	subMu       sync.RWMutex
	subscribers map[*Subscriber]struct{}

	// Lifecycle
	doneCh    chan struct{}
	closeOnce sync.Once

	// Last activity timestamp (protected by activityMu).
	// Separate from bufMu to avoid nested locks.
	activityMu   sync.Mutex
	lastActivity time.Time
}

func newManagedSession(id string, session *TerminalSession) *ManagedSession {
	ms := &ManagedSession{
		ID:           id,
		Session:      session,
		buffer:       make([]byte, 0, 4096),
		subscribers:  make(map[*Subscriber]struct{}),
		doneCh:       make(chan struct{}),
		lastActivity: time.Now(),
	}
	go ms.readLoop()
	go ms.watchShellExit()
	return ms
}

// watchShellExit monitors the shell process and closes the session when it exits.
// This handles the case where the user types "exit" but background child processes
// still hold the PTY slave fd open, which would otherwise keep readLoop alive
// and leave the terminal in a stuck state.
func (ms *ManagedSession) watchShellExit() {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("watchShellExit panic in session %s: %v", ms.ID, r)
		}
	}()

	select {
	case <-ms.Session.ShellDone():
		logrus.Infof("Shell process exited for session %s, closing session", ms.ID)
		ms.Session.Close()
		ms.markDead()
	case <-ms.doneCh:
	}
}

// readLoop continuously reads from the PTY and distributes output to subscribers.
// It runs for the entire lifetime of the session. When the PTY returns an error
// (shell exited), it marks the session as dead.
func (ms *ManagedSession) readLoop() {
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("readLoop panic in session %s: %v", ms.ID, r)
		}
		// Always mark dead on exit, whether from PTY error or panic.
		ms.markDead()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := ms.Session.Read(buf)
		if err != nil {
			return
		}
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			ms.appendBuffer(data)
			ms.broadcast(data)
		}
	}
}

// markDead atomically sets the dead flag and closes doneCh.
// Safe to call from multiple goroutines — only the first call takes effect.
func (ms *ManagedSession) markDead() {
	ms.closeOnce.Do(func() {
		ms.bufMu.Lock()
		ms.dead = true
		ms.bufMu.Unlock()
		close(ms.doneCh)
	})
}

// appendBuffer adds data to the ring buffer. No-op if the session is already dead,
// guaranteeing the buffer is immutable once dead == true.
func (ms *ManagedSession) appendBuffer(data []byte) {
	ms.bufMu.Lock()
	if ms.dead {
		ms.bufMu.Unlock()
		return
	}
	ms.buffer = append(ms.buffer, data...)
	if len(ms.buffer) > maxBufferSize {
		excess := len(ms.buffer) - maxBufferSize
		// Find the nearest newline after the truncation point so we start
		// at a line boundary. This avoids cutting inside ANSI escape
		// sequences, which would corrupt terminal state on replay.
		cutPoint := excess
		limit := excess + 256
		if limit > len(ms.buffer) {
			limit = len(ms.buffer)
		}
		for i := excess; i < limit; i++ {
			if ms.buffer[i] == '\n' {
				cutPoint = i + 1
				break
			}
		}
		ms.buffer = ms.buffer[cutPoint:]
	}
	ms.bufMu.Unlock()

	// Update activity outside bufMu to avoid nested locks.
	ms.activityMu.Lock()
	ms.lastActivity = time.Now()
	ms.activityMu.Unlock()
}

// GetBuffer returns a copy of the current output buffer, prepended with an
// ANSI reset sequence. The reset ensures that any text-attribute state lost
// during ring-buffer truncation (e.g. a color escape that was pruned) does
// not leak into the replayed content.
func (ms *ManagedSession) GetBuffer() []byte {
	ms.bufMu.Lock()
	defer ms.bufMu.Unlock()
	if len(ms.buffer) == 0 {
		return nil
	}
	reset := []byte(ansiReset)
	result := make([]byte, len(reset)+len(ms.buffer))
	copy(result, reset)
	copy(result[len(reset):], ms.buffer)
	return result
}

func (ms *ManagedSession) broadcast(data []byte) {
	ms.subMu.RLock()
	defer ms.subMu.RUnlock()
	for sub := range ms.subscribers {
		select {
		case sub.Ch <- data:
		case <-sub.done:
			// Subscriber is closing, skip
		case <-ms.doneCh:
			// Session is dead, stop broadcasting
			return
		default:
			// Channel full, drop data for this slow subscriber
		}
	}
}

// Subscribe registers a new subscriber to receive terminal output.
func (ms *ManagedSession) Subscribe() *Subscriber {
	sub := &Subscriber{
		Ch:   make(chan []byte, subscriberChanSize),
		done: make(chan struct{}),
	}
	ms.subMu.Lock()
	ms.subscribers[sub] = struct{}{}
	ms.subMu.Unlock()
	return sub
}

// Unsubscribe removes a subscriber and signals it to stop.
func (ms *ManagedSession) Unsubscribe(sub *Subscriber) {
	ms.subMu.Lock()
	delete(ms.subscribers, sub)
	ms.subMu.Unlock()

	// Signal subscriber goroutine to stop (idempotent via select).
	select {
	case <-sub.done:
	default:
		close(sub.done)
	}

	ms.activityMu.Lock()
	ms.lastActivity = time.Now()
	ms.activityMu.Unlock()
}

// IsDead returns true if the underlying shell process has exited.
func (ms *ManagedSession) IsDead() bool {
	ms.bufMu.Lock()
	defer ms.bufMu.Unlock()
	return ms.dead
}

// ClientCount returns the number of connected subscribers.
func (ms *ManagedSession) ClientCount() int {
	ms.subMu.RLock()
	defer ms.subMu.RUnlock()
	return len(ms.subscribers)
}

// Write writes input to the underlying terminal.
func (ms *ManagedSession) Write(p []byte) (int, error) {
	return ms.Session.Write(p)
}

// Resize changes the terminal dimensions.
func (ms *ManagedSession) Resize(cols, rows uint16) error {
	return ms.Session.Resize(cols, rows)
}

// Done returns a channel that is closed when the session dies (shell exits).
func (ms *ManagedSession) Done() <-chan struct{} {
	return ms.doneCh
}

// Close terminates the managed session, killing the shell and all its children.
func (ms *ManagedSession) Close() {
	ms.Session.Close()
	ms.markDead()
}

// SessionManager manages persistent terminal sessions.
// Sessions survive WebSocket disconnects and can be reconnected to.
type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*ManagedSession
	stopCh   chan struct{}
}

var (
	globalManager     *SessionManager
	globalManagerOnce sync.Once
)

// GetSessionManager returns the singleton session manager.
func GetSessionManager() *SessionManager {
	globalManagerOnce.Do(func() {
		globalManager = &SessionManager{
			sessions: make(map[string]*ManagedSession),
			stopCh:   make(chan struct{}),
		}
		go globalManager.cleanupLoop()
	})
	return globalManager
}

// GetOrCreate returns an existing live session for the given ID, or creates a new one.
func (sm *SessionManager) GetOrCreate(id string, shell string, workingDir string, env map[string]string, cols, rows uint16) (*ManagedSession, bool, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if ms, ok := sm.sessions[id]; ok && !ms.IsDead() {
		logrus.Infof("Reattaching to existing terminal session: %s", id)
		return ms, false, nil
	}

	// Create a new underlying terminal session
	session, err := NewTerminalSession(shell, workingDir, env, cols, rows)
	if err != nil {
		return nil, false, err
	}

	ms := newManagedSession(id, session)
	sm.sessions[id] = ms
	logrus.Infof("Created new terminal session: %s", id)
	return ms, true, nil
}

// Remove explicitly closes and removes a session.
func (sm *SessionManager) Remove(id string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	if ms, ok := sm.sessions[id]; ok {
		ms.Close()
		delete(sm.sessions, id)
		logrus.Infof("Removed terminal session: %s", id)
	}
}

func (sm *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(sessionCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sm.cleanup()
		case <-sm.stopCh:
			return
		}
	}
}

func (sm *SessionManager) cleanup() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	for id, ms := range sm.sessions {
		clientCount := ms.ClientCount()

		// Clean up dead sessions with no clients
		if ms.IsDead() && clientCount == 0 {
			ms.Close()
			delete(sm.sessions, id)
			logrus.Infof("Cleaned up dead terminal session: %s", id)
			continue
		}

		// Clean up idle sessions (no clients for too long)
		if clientCount == 0 {
			ms.activityMu.Lock()
			idle := now.Sub(ms.lastActivity) > sessionIdleTimeout
			ms.activityMu.Unlock()
			if idle {
				ms.Close()
				delete(sm.sessions, id)
				logrus.Infof("Cleaned up idle terminal session: %s (idle > %v)", id, sessionIdleTimeout)
			}
		}
	}
}
