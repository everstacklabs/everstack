package runtime

import (
	"sync"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// EventSink receives events from the emitter.
type EventSink interface {
	OnEvent(Event) error
}

// EventSinkFunc adapts a plain function to the EventSink interface.
type EventSinkFunc func(Event) error

func (f EventSinkFunc) OnEvent(e Event) error { return f(e) }

// Emitter fans out events to multiple subscribers without blocking the loop goroutine.
// Sinks are added at setup time. During execution, Emit iterates under a read lock.
// A replay buffer stores all emitted events so late subscribers (e.g. reconnecting
// UI clients) can catch up on missed events via AddSinkWithReplay.
type Emitter struct {
	sinks   []EventSink
	mu      sync.RWMutex
	dropped atomic.Uint64
	closed  atomic.Bool

	// Replay buffer for late subscribers (e.g. SSE reconnect).
	// Protected by its own mutex to avoid deadlock with the sink RWMutex.
	bufMu  sync.Mutex
	buffer []Event
}

// NewEmitter creates a new event emitter.
func NewEmitter() *Emitter {
	return &Emitter{}
}

// AddSink registers an event sink. Must be called before Run starts.
func (e *Emitter) AddSink(sink EventSink) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.sinks = append(e.sinks, sink)
}

// Emit sends an event to all registered sinks.
// Non-blocking: if a sink returns an error, the event is dropped for that sink.
func (e *Emitter) Emit(event Event) {
	if e.closed.Load() {
		return
	}

	// Append to replay buffer before fan-out (separate lock to avoid deadlock).
	e.bufMu.Lock()
	e.buffer = append(e.buffer, event)
	e.bufMu.Unlock()

	e.mu.RLock()
	defer e.mu.RUnlock()
	for _, sink := range e.sinks {
		if err := sink.OnEvent(event); err != nil {
			e.dropped.Add(1)
			logger.WithFields(
				"event_type", string(event.Type),
				"session_id", event.SessionID,
				"error", err.Error(),
			).Debug("event sink dropped event")
		}
	}
}

// AddSinkWithReplay registers an event sink and replays all previously emitted
// events before any new events are delivered. This is safe to call while the
// loop is running — the write lock on mu blocks Emit until replay is complete,
// ensuring no events are missed or duplicated.
func (e *Emitter) AddSinkWithReplay(sink EventSink) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Snapshot the buffer while holding mu.Lock (Emit can't proceed).
	// bufMu is acquired briefly just to copy; Emit releases bufMu before
	// acquiring mu.RLock so there is no lock-ordering deadlock.
	e.bufMu.Lock()
	replay := make([]Event, len(e.buffer))
	copy(replay, e.buffer)
	e.bufMu.Unlock()

	// Replay buffered events to the new sink.
	for _, event := range replay {
		_ = sink.OnEvent(event)
	}

	// Register for future events.
	e.sinks = append(e.sinks, sink)
}

// Dropped returns the total number of dropped events across all sinks.
func (e *Emitter) Dropped() uint64 {
	return e.dropped.Load()
}

// Close marks the emitter as closed. Subsequent Emit calls are no-ops.
func (e *Emitter) Close() {
	e.closed.Store(true)
}
