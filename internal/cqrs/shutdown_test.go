package cqrs

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
)

// recordingWriter is the store behind the batching writer.
type recordingWriter struct {
	mu      sync.Mutex
	written int
	hold    chan struct{} // when set, Append blocks until it is closed
}

func (w *recordingWriter) Append(_ context.Context, events ...database.Event) error {
	if w.hold != nil {
		<-w.hold
	}
	w.mu.Lock()
	w.written += len(events)
	w.mu.Unlock()
	return nil
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.written
}

// An event a command already reported as dispatched must reach the store when
// the process stops, not wait for a flush timer that will never fire.
func TestShutdownFlushesBufferedEvents(t *testing.T) {
	store := &recordingWriter{}
	// An interval far longer than the test: only Shutdown can flush.
	batched := database.NewBatchingWriter(store, database.BatchingWriterOptions{FlushInterval: time.Hour})
	sys := &System{Writer: batched}

	if err := batched.Append(context.Background(), database.Event{}, database.Event{}, database.Event{}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if store.count() != 0 {
		t.Fatalf("events reached the store before any flush")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sys.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := store.count(); got != 3 {
		t.Fatalf("store holds %d events after Shutdown, want 3", got)
	}
}

// A store that does not answer must not hold the process open past the
// budget; the caller is told the drain did not finish.
func TestShutdownGivesUpWhenTheBudgetRunsOut(t *testing.T) {
	store := &recordingWriter{hold: make(chan struct{})}
	defer close(store.hold)
	batched := database.NewBatchingWriter(store, database.BatchingWriterOptions{FlushInterval: time.Hour})
	sys := &System{Writer: batched}
	_ = batched.Append(context.Background(), database.Event{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := sys.Shutdown(ctx); err == nil {
		t.Fatalf("Shutdown reported success while the store was still blocked")
	}
}

// A writer that writes through has nothing buffered and nothing to close.
func TestShutdownWithUnbufferedWriter(t *testing.T) {
	sys := &System{Writer: &recordingWriter{}}
	if err := sys.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
