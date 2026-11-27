package database

import (
	"context"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// BatchingWriter buffers events and flushes them in batches on a background
// goroutine.
//
// It exists because the gateway wrote one row per request, synchronously, on
// the request path. Measured on a live instance under load, that produced
// 11,670 single-row INSERTs and 2,267 merges in ten minutes, with
// avg_rows_per_insert = 1; ClickHouse's own guidance is to insert at least
// 1,000 rows at a time, because every insert creates a part that then has to be
// merged. A goroutine profile at saturation showed more requests waiting on
// that write than on the model provider itself.
//
// Two things are being fixed at once: the write leaves the request path, and
// ClickHouse gets the batch sizes it is designed for.
//
// Events are grouped by tenant. The tenant is read from the request context at
// Append time and replayed onto a fresh context at flush time, so a batch never
// mixes tenants and a flush never depends on a request context that has since
// been cancelled.
type BatchingWriter struct {
	inner Writer

	maxBatch      int
	flushInterval time.Duration

	mu         sync.Mutex
	pending    map[string][]Event // tenant -> events
	maxPending int

	wake     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	// dropped counts events discarded because the buffer was full. Telemetry
	// is worth dropping to keep serving traffic, but never worth dropping
	// silently.
	droppedMu sync.Mutex
	dropped   int
}

// BatchingWriterOptions configures the buffer. Zero values take defaults.
type BatchingWriterOptions struct {
	// MaxBatch is the row count that triggers an immediate flush.
	MaxBatch int
	// FlushInterval bounds how long an event waits when traffic is light.
	FlushInterval time.Duration
	// MaxPending caps total buffered events across all tenants. Beyond it,
	// new events are dropped rather than growing memory without limit.
	MaxPending int
}

const (
	defaultMaxBatch      = 1000
	defaultFlushInterval = 250 * time.Millisecond
	defaultMaxPending    = 50000
)

// NewBatchingWriter wraps inner and starts its flush loop. Call Close to drain.
func NewBatchingWriter(inner Writer, opts BatchingWriterOptions) *BatchingWriter {
	if opts.MaxBatch <= 0 {
		opts.MaxBatch = defaultMaxBatch
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = defaultFlushInterval
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = defaultMaxPending
	}

	w := &BatchingWriter{
		inner:         inner,
		maxBatch:      opts.MaxBatch,
		flushInterval: opts.FlushInterval,
		pending:       make(map[string][]Event),
		wake:          make(chan struct{}, 1),
		stop:          make(chan struct{}),
		done:          make(chan struct{}),
		maxPending:    opts.MaxPending,
	}
	go w.loop()
	return w
}

// Append buffers events and returns immediately. The error result is retained
// for interface compatibility; a caller cannot learn the fate of a buffered
// write, which is the point of moving it off the request path.
func (w *BatchingWriter) Append(ctx context.Context, events ...Event) error {
	if len(events) == 0 {
		return nil
	}
	tenant := TenantSchemaFromContext(ctx)

	w.mu.Lock()
	if w.count()+len(events) > w.maxPending {
		w.mu.Unlock()
		w.recordDropped(len(events))
		return nil
	}
	w.pending[tenant] = append(w.pending[tenant], events...)
	full := len(w.pending[tenant]) >= w.maxBatch
	w.mu.Unlock()

	if full {
		w.signal()
	}
	return nil
}

// count returns total buffered events. Caller must hold mu.
func (w *BatchingWriter) count() int {
	n := 0
	for _, evs := range w.pending {
		n += len(evs)
	}
	return n
}

func (w *BatchingWriter) signal() {
	select {
	case w.wake <- struct{}{}:
	default: // a flush is already pending
	}
}

func (w *BatchingWriter) recordDropped(n int) {
	w.droppedMu.Lock()
	w.dropped += n
	w.droppedMu.Unlock()
}

// Dropped returns how many events have been discarded due to a full buffer.
func (w *BatchingWriter) Dropped() int {
	w.droppedMu.Lock()
	defer w.droppedMu.Unlock()
	return w.dropped
}

func (w *BatchingWriter) loop() {
	defer close(w.done)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stop:
			w.flush()
			return
		case <-ticker.C:
			w.flush()
		case <-w.wake:
			w.flush()
		}
	}
}

// flush drains the buffer, one batch per tenant.
func (w *BatchingWriter) flush() {
	w.mu.Lock()
	if len(w.pending) == 0 {
		w.mu.Unlock()
		w.reportDropped()
		return
	}
	batches := w.pending
	w.pending = make(map[string][]Event)
	w.mu.Unlock()

	for tenant, events := range batches {
		for start := 0; start < len(events); start += w.maxBatch {
			end := start + w.maxBatch
			if end > len(events) {
				end = len(events)
			}
			w.write(tenant, events[start:end])
		}
	}
	w.reportDropped()
}

func (w *BatchingWriter) write(tenant string, events []Event) {
	// A fresh context: the request that produced these events is long gone,
	// and its cancellation must not abort a write that is no longer part of
	// serving it. The tenant is replayed so the rows land in the right place.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if tenant != "" {
		ctx = WithTenantSchema(ctx, tenant)
	}

	if err := w.inner.Append(ctx, events...); err != nil {
		logger.WithFields(
			"error", err.Error(),
			"event_count", len(events),
			"tenant_id", tenant,
		).Error("batching writer: flush failed; events lost")
	}
}

func (w *BatchingWriter) reportDropped() {
	w.droppedMu.Lock()
	n := w.dropped
	w.dropped = 0
	w.droppedMu.Unlock()
	if n > 0 {
		logger.WithFields("dropped", n).Warn("batching writer: buffer full, events dropped")
	}
}

// Close stops the flush loop after draining what is buffered.
func (w *BatchingWriter) Close() error {
	w.stopOnce.Do(func() { close(w.stop) })
	<-w.done
	return nil
}
