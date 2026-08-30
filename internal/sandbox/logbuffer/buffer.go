// Package logbuffer provides a shared, thread-safe log buffer used by sandbox
// backends (Docker, Kubernetes) to capture exec and shell output.
package logbuffer

import (
	"fmt"
	"sync"
	"time"
)

// MaxBufferBytes is the approximate per-sandbox log buffer size limit.
const MaxBufferBytes = 1 << 20 // 1MB

// Entry represents a single captured log line from exec or shell output.
type Entry struct {
	Timestamp time.Time
	Stream    string // "stdout", "stderr", "system"
	Line      string
}

// Buffer is a per-sandbox, thread-safe circular log buffer that captures output
// from exec and shell sessions. Container log drivers typically can't see exec
// output, so backends write to this buffer and serve it via the Logs API.
type Buffer struct {
	mu      sync.Mutex
	entries []Entry
	size    int          // approximate byte count of stored entries
	subs    []chan Entry  // subscribers for Follow mode
	closed  bool
}

// NewBuffer creates a new empty log buffer.
func NewBuffer() *Buffer {
	return &Buffer{}
}

// Append adds a log entry, evicting old entries if the buffer exceeds ~1MB.
func (b *Buffer) Append(e Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}

	entrySize := len(e.Line) + len(e.Stream) + 40 // rough overhead
	b.entries = append(b.entries, e)
	b.size += entrySize

	// Evict oldest entries when buffer exceeds limit
	for b.size > MaxBufferBytes && len(b.entries) > 0 {
		old := b.entries[0]
		b.size -= len(old.Line) + len(old.Stream) + 40
		b.entries = b.entries[1:]
	}

	// Notify all followers
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // drop if subscriber is slow
		}
	}
}

// Snapshot returns a copy of current entries. If tail > 0, only the last
// tail entries are returned.
func (b *Buffer) Snapshot(tail int) []Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	if tail > 0 && tail < len(b.entries) {
		out := make([]Entry, tail)
		copy(out, b.entries[len(b.entries)-tail:])
		return out
	}
	out := make([]Entry, len(b.entries))
	copy(out, b.entries)
	return out
}

// Subscribe returns a channel that receives new log entries in real time.
func (b *Buffer) Subscribe() chan Entry {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Entry, 64)
	if b.closed {
		close(ch)
		return ch
	}
	b.subs = append(b.subs, ch)
	return ch
}

// Unsubscribe removes a subscriber channel.
func (b *Buffer) Unsubscribe(ch chan Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.subs {
		if sub == ch {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			break
		}
	}
}

// Close marks the log buffer as closed and closes all subscriber channels.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, ch := range b.subs {
		close(ch)
	}
	b.subs = nil
}

// FormatEntry formats a log entry as a single line, optionally with timestamps.
func FormatEntry(e Entry, timestamps bool) string {
	if timestamps {
		return fmt.Sprintf("%s [%s] %s", e.Timestamp.Format(time.RFC3339Nano), e.Stream, e.Line)
	}
	return fmt.Sprintf("[%s] %s", e.Stream, e.Line)
}
