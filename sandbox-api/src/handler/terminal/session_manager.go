package terminal

import (
	"sync"
	"sync/atomic"
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

	// After the shell exits, the session ends once no output has been read for
	// shellDrainTimeout, so that a child keeping the PTY slave open does not
	// keep it alive. Output still being delivered to slow clients extends the
	// drain, up to shellDrainMaxTimeout.
	shellDrainTimeout    = 250 * time.Millisecond
	shellDrainMaxTimeout = 30 * time.Second

	// ansiReset resets all terminal text attributes. Prepended to buffer replays
	// to avoid inheriting stale formatting from truncated escape sequences.
	ansiReset = "\x1b[0m"
)

// subscriberLagTimeout is how long the PTY reader waits on a subscriber whose
// channel is full before dropping it. Until then the reader blocks, so the
// shell is throttled to the pace of its clients instead of losing output.
// Longer than the handlers' write timeouts, so it only catches a client
// that is stuck, not one on a slow link. A variable for tests.
var subscriberLagTimeout = 15 * time.Second

// Subscriber represents a connected WebSocket client receiving terminal output.
type Subscriber struct {
	Ch       chan []byte
	done     chan struct{}
	doneOnce sync.Once

	// dropped is closed when the session stopped delivering to this
	// subscriber because it did not keep up. Ch then holds the last output it
	// will get, without gaps; the client must reconnect to resync.
	dropped     chan struct{}
	droppedOnce sync.Once
}

// Dropped returns a channel that is closed when output stopped being
// delivered to this subscriber because it fell too far behind.
func (sub *Subscriber) Dropped() <-chan struct{} {
	return sub.dropped
}

func (sub *Subscriber) close() {
	sub.doneOnce.Do(func() { close(sub.done) })
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

	// Progress of readLoop, for draining after the shell exits.
	delivering atomic.Bool
	published  atomic.Uint64
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
	defer ms.Session.Close()
	defer func() {
		if r := recover(); r != nil {
			logrus.Errorf("watchShellExit panic in session %s: %v", ms.ID, r)
		}
	}()

	select {
	case <-ms.Session.ShellDone():
		logrus.Infof("Shell process exited for session %s, closing session", ms.ID)
		// Let readLoop publish the shell's final output before ending the session.
		// A background child may keep the PTY open indefinitely.
		ticker := time.NewTicker(shellDrainTimeout)
		defer ticker.Stop()
		deadline := time.Now().Add(shellDrainMaxTimeout)
		published := ms.published.Load()
		for {
			select {
			case <-ticker.C:
			case <-ms.doneCh:
				return
			}
			n := ms.published.Load()
			if (n == published && !ms.delivering.Load()) || time.Now().After(deadline) {
				return
			}
			published = n
		}
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
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			ms.publish(data)
		}
		if err != nil {
			return
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

// publish appends data to the ring buffer and takes the list of subscribers
// to deliver it to while holding bufMu, so SubscribeWithReplay sees each chunk
// either in its snapshot or on its channel, never both. Delivery happens after
// the lock is released, because it waits for slow subscribers.
// No-op if the session is already dead: the buffer is immutable once dead == true.
func (ms *ManagedSession) publish(data []byte) {
	ms.delivering.Store(true)
	defer func() {
		ms.published.Add(1)
		ms.delivering.Store(false)
	}()

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
	subs := ms.subscriberList()
	ms.bufMu.Unlock()

	ms.broadcast(subs, data)

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
	return ms.bufferLocked()
}

func (ms *ManagedSession) bufferLocked() []byte {
	if len(ms.buffer) == 0 {
		return nil
	}
	reset := []byte(ansiReset)
	result := make([]byte, len(reset)+len(ms.buffer))
	copy(result, reset)
	copy(result[len(reset):], ms.buffer)
	return result
}

func (ms *ManagedSession) subscriberList() []*Subscriber {
	ms.subMu.RLock()
	defer ms.subMu.RUnlock()
	subs := make([]*Subscriber, 0, len(ms.subscribers))
	for sub := range ms.subscribers {
		subs = append(subs, sub)
	}
	return subs
}

// broadcast delivers data to every subscriber, waiting while a subscriber's
// channel is full. The PTY is not read meanwhile, so the kernel buffer fills
// and the shell blocks on write: output is throttled, never lost. A
// subscriber that stays full for subscriberLagTimeout is dropped and told so.
func (ms *ManagedSession) broadcast(subs []*Subscriber, data []byte) {
	for _, sub := range subs {
		select {
		case sub.Ch <- data:
			continue
		case <-sub.done:
			continue
		case <-sub.dropped:
			continue
		case <-ms.doneCh:
			return
		default:
		}

		timer := time.NewTimer(subscriberLagTimeout)
		select {
		case sub.Ch <- data:
		case <-sub.done:
		case <-sub.dropped:
		case <-ms.doneCh:
			timer.Stop()
			return
		case <-timer.C:
			logrus.Warnf("Terminal session %s: subscriber did not read output for %v, dropping it", ms.ID, subscriberLagTimeout)
			ms.drop(sub)
		}
		timer.Stop()
	}
}

// drop stops delivering output to sub and signals it through Dropped.
func (ms *ManagedSession) drop(sub *Subscriber) {
	ms.subMu.Lock()
	delete(ms.subscribers, sub)
	ms.subMu.Unlock()
	sub.droppedOnce.Do(func() { close(sub.dropped) })
}

// Subscribe registers a new subscriber to receive terminal output.
// While a subscriber is attached, the session reads the PTY no faster than
// the subscriber consumes Ch.
func (ms *ManagedSession) Subscribe() *Subscriber {
	sub := &Subscriber{
		Ch:      make(chan []byte, subscriberChanSize),
		done:    make(chan struct{}),
		dropped: make(chan struct{}),
	}
	ms.subMu.Lock()
	ms.subscribers[sub] = struct{}{}
	ms.subMu.Unlock()
	return sub
}

// SubscribeWithReplay registers a subscriber and returns the buffered output
// up to that point: the output is replayed exactly once, without gaps.
func (ms *ManagedSession) SubscribeWithReplay() (*Subscriber, []byte) {
	ms.bufMu.Lock()
	defer ms.bufMu.Unlock()
	return ms.Subscribe(), ms.bufferLocked()
}

// Unsubscribe removes a subscriber and signals it to stop.
func (ms *ManagedSession) Unsubscribe(sub *Subscriber) {
	ms.subMu.Lock()
	delete(ms.subscribers, sub)
	ms.subMu.Unlock()

	// Signal subscriber goroutine to stop, and release a broadcast waiting on it.
	sub.close()

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

// Get returns the live session for the given ID, if any.
func (sm *SessionManager) Get(id string) (*ManagedSession, bool) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	ms, ok := sm.sessions[id]
	if !ok || ms.IsDead() {
		return nil, false
	}
	return ms, true
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
