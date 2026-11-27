package database

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingWriter struct {
	mu      sync.Mutex
	batches [][]Event
	tenants []string
}

func (r *recordingWriter) Append(ctx context.Context, events ...Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := make([]Event, len(events))
	copy(cp, events)
	r.batches = append(r.batches, cp)
	r.tenants = append(r.tenants, TenantSchemaFromContext(ctx))
	return nil
}

func (r *recordingWriter) snapshot() ([][]Event, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	b := make([][]Event, len(r.batches))
	copy(b, r.batches)
	t := make([]string, len(r.tenants))
	copy(t, r.tenants)
	return b, t
}

func ev(id string) Event { return Event{ID: id, Stream: "s", Type: "t", Payload: []byte("{}")} }

// The whole point: many single-row Appends must reach the backend as few large
// writes, because ClickHouse creates a part per insert.
func TestAppendsAreCoalescedIntoBatches(t *testing.T) {
	rec := &recordingWriter{}
	w := NewBatchingWriter(rec, BatchingWriterOptions{MaxBatch: 100, FlushInterval: 20 * time.Millisecond})
	defer w.Close()

	for i := 0; i < 250; i++ {
		if err := w.Append(context.Background(), ev("e")); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	batches, _ := rec.snapshot()
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 250 {
		t.Errorf("wrote %d events, want 250", total)
	}
	// 250 single-row appends must not become 250 writes.
	if len(batches) > 5 {
		t.Errorf("coalesced into %d writes; batching is not working", len(batches))
	}
}

// Batching must never merge events from different tenants into one write. The
// tenant is read from the request context and has to survive to flush time.
func TestBatchesAreNeverMixedAcrossTenants(t *testing.T) {
	rec := &recordingWriter{}
	w := NewBatchingWriter(rec, BatchingWriterOptions{MaxBatch: 1000, FlushInterval: 20 * time.Millisecond})

	for i := 0; i < 50; i++ {
		_ = w.Append(WithTenantSchema(context.Background(), "tenant-a"), ev("a"))
		_ = w.Append(WithTenantSchema(context.Background(), "tenant-b"), ev("b"))
	}
	w.Close()

	batches, tenants := rec.snapshot()
	if len(batches) == 0 {
		t.Fatal("nothing was flushed")
	}
	for i, b := range batches {
		want := map[string]string{"tenant-a": "a", "tenant-b": "b"}[tenants[i]]
		if want == "" {
			t.Fatalf("batch %d has unexpected tenant %q", i, tenants[i])
		}
		for _, e := range b {
			if e.ID != want {
				t.Fatalf("batch for tenant %q contains an event from the other tenant: %q", tenants[i], e.ID)
			}
		}
	}
}

// A flush must not inherit the request context. The request that produced an
// event is over by then, and its cancellation must not abort the write.
func TestFlushSurvivesCancelledRequestContext(t *testing.T) {
	rec := &recordingWriter{}
	w := NewBatchingWriter(rec, BatchingWriterOptions{MaxBatch: 1000, FlushInterval: 20 * time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	_ = w.Append(ctx, ev("survives"))
	cancel() // the request goes away before the flush happens

	w.Close()
	batches, _ := rec.snapshot()
	total := 0
	for _, b := range batches {
		total += len(b)
	}
	if total != 1 {
		t.Errorf("wrote %d events, want 1: a cancelled request dropped its own telemetry", total)
	}
}

// Append is on the request path, so it must not block on the backend.
func TestAppendDoesNotBlockOnTheBackend(t *testing.T) {
	slow := &blockingWriter{release: make(chan struct{})}
	w := NewBatchingWriter(slow, BatchingWriterOptions{MaxBatch: 1, FlushInterval: time.Millisecond})
	defer func() { close(slow.release); w.Close() }()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_ = w.Append(context.Background(), ev("x"))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked while the backend was stalled; the write is still on the request path")
	}
}

type blockingWriter struct{ release chan struct{} }

func (b *blockingWriter) Append(ctx context.Context, events ...Event) error {
	<-b.release
	return nil
}

// Dropping is acceptable under extreme load; dropping silently is not.
func TestFullBufferDropsAndCounts(t *testing.T) {
	slow := &blockingWriter{release: make(chan struct{})}
	w := NewBatchingWriter(slow, BatchingWriterOptions{MaxBatch: 10000, FlushInterval: time.Hour, MaxPending: 10})
	defer func() { close(slow.release); w.Close() }()

	for i := 0; i < 100; i++ {
		_ = w.Append(context.Background(), ev("x"))
	}
	if w.Dropped() == 0 {
		t.Error("expected dropped events to be counted when the buffer is full")
	}
}

func TestCloseFlushesRemaining(t *testing.T) {
	rec := &recordingWriter{}
	w := NewBatchingWriter(rec, BatchingWriterOptions{MaxBatch: 1000, FlushInterval: time.Hour})
	_ = w.Append(context.Background(), ev("pending"))
	w.Close() // must drain rather than discard

	batches, _ := rec.snapshot()
	if len(batches) != 1 || len(batches[0]) != 1 {
		t.Errorf("Close did not flush buffered events: %v", batches)
	}
}
