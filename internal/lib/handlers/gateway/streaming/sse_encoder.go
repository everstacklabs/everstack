// Package streaming provides high-performance streaming utilities for the gateway.
package streaming

import (
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

var (
	// SSE frame prefixes and suffixes as byte slices (avoid string->[]byte conversion)
	sseDataPrefix = []byte("data: ")
	sseEventEnd   = []byte("\n\n")
	sseDoneEvent  = []byte("data: [DONE]\n\n")
)

// PooledSSEWriter provides zero-allocation SSE event writing using pooled buffers.
//
// Performance targets:
//   - WriteEvent: <100µs per chunk
//   - Zero allocations per event (after initial buffer acquisition)
type PooledSSEWriter struct {
	w       io.Writer
	flusher http.Flusher
	buf     *[]byte
	closed  atomic.Bool

	// Stats
	eventsWritten atomic.Uint64
	bytesWritten  atomic.Uint64
	flushes       atomic.Uint64
}

// NewPooledSSEWriter creates a new SSE writer with a pooled buffer.
// The caller must call Close() when done to return the buffer to the pool.
func NewPooledSSEWriter(w http.ResponseWriter) *PooledSSEWriter {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable nginx buffering

	var flusher http.Flusher
	if f, ok := w.(http.Flusher); ok {
		flusher = f
	}

	return &PooledSSEWriter{
		w:       w,
		flusher: flusher,
		buf:     GetBuffer(),
	}
}

// NewPooledSSEWriterFromWriter creates an SSE writer from any io.Writer.
// Headers must be set by the caller. Use this for testing or non-HTTP streams.
func NewPooledSSEWriterFromWriter(w io.Writer, flusher http.Flusher) *PooledSSEWriter {
	return &PooledSSEWriter{
		w:       w,
		flusher: flusher,
		buf:     GetBuffer(),
	}
}

// WriteEvent writes an SSE data event with the given payload.
// The payload should be JSON (or any string data).
// This method uses the pooled buffer to avoid allocations.
func (p *PooledSSEWriter) WriteEvent(data []byte) error {
	if p.closed.Load() {
		return io.ErrClosedPipe
	}

	// Reset buffer and build SSE frame
	buf := *p.buf
	buf = buf[:0]
	buf = append(buf, sseDataPrefix...)
	buf = append(buf, data...)
	buf = append(buf, sseEventEnd...)

	// Write to underlying writer
	n, err := p.w.Write(buf)
	if err != nil {
		return err
	}

	p.bytesWritten.Add(uint64(n))
	p.eventsWritten.Add(1)

	// Flush immediately for SSE
	if p.flusher != nil {
		p.flusher.Flush()
		p.flushes.Add(1)
	}

	// Update buffer reference (in case append reallocated)
	*p.buf = buf

	return nil
}

// WriteEventString is a convenience method for string data.
func (p *PooledSSEWriter) WriteEventString(data string) error {
	return p.WriteEvent([]byte(data))
}

// WriteRaw writes raw bytes directly without SSE framing.
// Useful for custom event types or comments.
func (p *PooledSSEWriter) WriteRaw(data []byte) error {
	if p.closed.Load() {
		return io.ErrClosedPipe
	}

	n, err := p.w.Write(data)
	if err != nil {
		return err
	}

	p.bytesWritten.Add(uint64(n))

	if p.flusher != nil {
		p.flusher.Flush()
		p.flushes.Add(1)
	}

	return nil
}

// WriteDone writes the SSE [DONE] event to signal end of stream.
func (p *PooledSSEWriter) WriteDone() error {
	if p.closed.Load() {
		return io.ErrClosedPipe
	}

	n, err := p.w.Write(sseDoneEvent)
	if err != nil {
		return err
	}

	p.bytesWritten.Add(uint64(n))
	p.eventsWritten.Add(1)

	if p.flusher != nil {
		p.flusher.Flush()
		p.flushes.Add(1)
	}

	return nil
}

// Flush forces a flush of buffered data.
func (p *PooledSSEWriter) Flush() {
	if p.flusher != nil {
		p.flusher.Flush()
		p.flushes.Add(1)
	}
}

// Close returns the buffer to the pool and marks the writer as closed.
// Always call this when done, preferably with defer.
func (p *PooledSSEWriter) Close() error {
	if p.closed.Swap(true) {
		return nil // Already closed
	}

	if p.buf != nil {
		PutBuffer(p.buf)
		p.buf = nil
	}

	return nil
}

// Stats returns the writer's statistics.
func (p *PooledSSEWriter) Stats() (events, bytes, flushes uint64) {
	return p.eventsWritten.Load(), p.bytesWritten.Load(), p.flushes.Load()
}

// BatchedSSEWriter batches multiple small events before flushing.
// This reduces the number of syscalls at the cost of slightly higher latency.
type BatchedSSEWriter struct {
	*PooledSSEWriter
	batchSize    int
	batchCount   int
	maxBatchWait time.Duration
	lastFlush    time.Time
}

// NewBatchedSSEWriter creates an SSE writer that batches events.
// Events are flushed when:
//   - batchSize events have been buffered
//   - maxWait time has passed since the last flush
//   - Flush() is called explicitly
func NewBatchedSSEWriter(w http.ResponseWriter, batchSize int, maxWait time.Duration) *BatchedSSEWriter {
	return &BatchedSSEWriter{
		PooledSSEWriter: NewPooledSSEWriter(w),
		batchSize:       batchSize,
		maxBatchWait:    maxWait,
		lastFlush:       time.Now(),
	}
}

// WriteEvent writes an event, potentially batching before flush.
func (b *BatchedSSEWriter) WriteEvent(data []byte) error {
	if b.closed.Load() {
		return io.ErrClosedPipe
	}

	// Build SSE frame into buffer
	buf := *b.buf
	buf = append(buf, sseDataPrefix...)
	buf = append(buf, data...)
	buf = append(buf, sseEventEnd...)
	*b.buf = buf

	b.eventsWritten.Add(1)
	b.batchCount++

	// Check if we should flush
	shouldFlush := b.batchCount >= b.batchSize ||
		time.Since(b.lastFlush) >= b.maxBatchWait

	if shouldFlush {
		return b.flush()
	}

	return nil
}

// flush writes the buffered data and resets the buffer.
func (b *BatchedSSEWriter) flush() error {
	buf := *b.buf
	if len(buf) == 0 {
		return nil
	}

	n, err := b.w.Write(buf)
	if err != nil {
		return err
	}

	b.bytesWritten.Add(uint64(n))
	*b.buf = buf[:0]
	b.batchCount = 0
	b.lastFlush = time.Now()

	if b.flusher != nil {
		b.flusher.Flush()
		b.flushes.Add(1)
	}

	return nil
}

// Flush forces a flush of any buffered events.
func (b *BatchedSSEWriter) Flush() {
	_ = b.flush()
}

// Close flushes remaining data and returns the buffer to the pool.
func (b *BatchedSSEWriter) Close() error {
	_ = b.flush()
	return b.PooledSSEWriter.Close()
}

