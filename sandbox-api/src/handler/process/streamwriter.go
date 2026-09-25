package process

import (
	"io"
	"sync"
)

// maxPendingStreamBytes bounds what one attaching stream queues while its
// backlog is replayed. Past it the queue is dropped and the stream says so:
// output the reader can still get from the log file is not worth holding the
// guest's memory for.
const maxPendingStreamBytes = 1024 * 1024

// streamGapMarker is sent when the queue overflowed, so a reader does not take
// the stream for a contiguous one.
const streamGapMarker = "[output truncated: the stream fell behind]\n"

type pendingEvent struct {
	// event is the stream the data belongs to, or "" for a raw write.
	event string
	data  []byte
}

// pendingWriter is attached to a process before its backlog is read, so output
// produced while it is being read is queued rather than lost, and still arrives
// after the backlog. release passes everything through from then on.
type pendingWriter struct {
	target io.Writer

	mu       sync.Mutex
	queue    []pendingEvent
	queued   int
	dropped  bool
	released bool
}

func newPendingWriter(target io.Writer) *pendingWriter {
	return &pendingWriter{target: target}
}

func (p *pendingWriter) Write(data []byte) (int, error) {
	if p.queue0("", data) {
		return len(data), nil
	}
	return p.target.Write(data)
}

func (p *pendingWriter) WriteEvent(eventType string, data string) (int, error) {
	if p.queue0(eventType, []byte(data)) {
		return len(data), nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sendLocked(eventType, []byte(data))
	return len(data), nil
}

// IsJSONStreamWriter reports what the target is: WriteEvent handles both, but
// callers use this to decide whether events are structured.
func (p *pendingWriter) IsJSONStreamWriter() bool {
	jw, ok := p.target.(JSONStreamWriter)
	return ok && jw.IsJSONStreamWriter()
}

func (p *pendingWriter) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.released {
		return
	}
	if f, ok := p.target.(interface{ Flush() }); ok {
		f.Flush()
	}
}

// queue0 queues data while the writer is still pending, and says whether it
// did: false means the caller writes it through itself.
func (p *pendingWriter) queue0(event string, data []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.released {
		return false
	}
	if p.queued+len(data) > maxPendingStreamBytes {
		p.dropped = true
		return true
	}
	p.queue = append(p.queue, pendingEvent{event: event, data: append([]byte(nil), data...)})
	p.queued += len(data)
	return true
}

// release flushes what was queued and stops queueing.
func (p *pendingWriter) release() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for _, event := range p.queue {
		if event.event == "" {
			_, _ = p.target.Write(event.data)
			continue
		}
		p.sendLocked(event.event, event.data)
	}
	// The gap is between what was queued and what is written from now on, so it
	// is said here rather than before the queue.
	if p.dropped {
		p.sendLocked("stdout", []byte(streamGapMarker))
	}
	p.queue = nil
	p.queued = 0
	p.released = true
}

func (p *pendingWriter) sendLocked(eventType string, data []byte) {
	writeToLogWriter(p.target, eventType, data)
}

// unwrapWriter returns the writer a caller handed to StreamProcessOutput, so it
// can still be found by identity once it has been wrapped.
func unwrapWriter(w io.Writer) io.Writer {
	if pending, ok := w.(*pendingWriter); ok {
		return pending.target
	}
	return w
}
