package apikey

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pdb "github.com/everstacklabs/everstack/pkg/database"
)

var errDatabaseDown = errors.New("too many clients already")

// Concurrent lookups of one key share a single query and its answer.
func TestLookupGuardSharesConcurrentLookups(t *testing.T) {
	g := NewLookupGuard(DefaultFailureWindow)
	var calls atomic.Int32
	release := make(chan struct{})
	fn := func(context.Context) (interface{}, error) {
		calls.Add(1)
		<-release
		return "record", nil
	}
	var wg sync.WaitGroup
	results := make(chan interface{}, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := g.Do(context.Background(), "hash-a", fn)
			if err != nil {
				t.Errorf("Do: %v", err)
			}
			results <- v
		}()
	}
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for v := range results {
		if v != "record" {
			t.Fatalf("a waiter got %v", v)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("%d queries for 50 concurrent lookups of one key, want 1", got)
	}
}

// After a failure the database is not asked about that key again until the
// window passes; then it is.
func TestLookupGuardRemembersAFailureForTheWindowOnly(t *testing.T) {
	g := NewLookupGuard(2 * time.Second)
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }
	var calls int
	failing := true
	fn := func(context.Context) (interface{}, error) {
		calls++
		if failing {
			return nil, errDatabaseDown
		}
		return "record", nil
	}
	if _, err := g.Do(context.Background(), "hash-a", fn); !errors.Is(err, errDatabaseDown) {
		t.Fatalf("first lookup: %v, want the database's error", err)
	}
	failing = false
	if _, err := g.Do(context.Background(), "hash-a", fn); !errors.Is(err, ErrLookupUnavailable) || calls != 1 {
		t.Fatalf("within the window: %v after %d queries, want unavailable without a query", err, calls)
	}
	if v, err := g.Do(context.Background(), "hash-b", fn); err != nil || v != "record" {
		t.Fatalf("another key is not affected: %v %v", v, err)
	}
	now = now.Add(2 * time.Second)
	if v, err := g.Do(context.Background(), "hash-a", fn); err != nil || v != "record" || calls != 3 {
		t.Fatalf("after the window: %v %v after %d queries, want the record from a fresh query", v, err, calls)
	}
}

// Callers with different tenant contexts never share a lookup or a failure.
func TestLookupGuardSeparatesTenantContexts(t *testing.T) {
	g := NewLookupGuard(time.Minute)
	ctxA := pdb.WithTenantSchema(context.Background(), "inst_a")
	ctxB := pdb.WithTenantSchema(context.Background(), "inst_b")
	if _, err := g.Do(ctxA, "hash-a", func(context.Context) (interface{}, error) { return nil, errDatabaseDown }); err == nil {
		t.Fatalf("want the failure")
	}
	if v, err := g.Do(ctxB, "hash-a", func(context.Context) (interface{}, error) { return "b", nil }); err != nil || v != "b" {
		t.Fatalf("tenant B got %v %v; a failure under tenant A must not apply to it", v, err)
	}
}

// A waiter whose own context ends stops waiting; the shared lookup carries on
// for the others and is not cancelled by it.
func TestLookupGuardCallerCancellation(t *testing.T) {
	g := NewLookupGuard(DefaultFailureWindow)
	release := make(chan struct{})
	var lookupErr atomic.Value
	fn := func(ctx context.Context) (interface{}, error) {
		<-release
		if err := ctx.Err(); err != nil {
			lookupErr.Store(err)
		}
		return "record", nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := g.Do(ctx, "hash-a", fn)
		done <- err
	}()
	other := make(chan interface{}, 1)
	go func() {
		v, _ := g.Do(context.Background(), "hash-a", fn)
		other <- v
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled caller got %v", err)
	}
	close(release)
	if v := <-other; v != "record" {
		t.Fatalf("the other waiter got %v", v)
	}
	if err := lookupErr.Load(); err != nil {
		t.Fatalf("the shared lookup saw the first caller's cancellation: %v", err)
	}
}

// The failures remembered at once are bounded.
func TestLookupGuardBoundsRememberedFailures(t *testing.T) {
	g := NewLookupGuard(time.Minute)
	g.max = 3
	fail := func(context.Context) (interface{}, error) { return nil, errDatabaseDown }
	for _, h := range []string{"a", "b", "c", "d", "e"} {
		_, _ = g.Do(context.Background(), h, fail)
	}
	if len(g.failed) != 3 {
		t.Fatalf("remembered %d failures, want the bound of 3", len(g.failed))
	}
	var calls int
	if _, err := g.Do(context.Background(), "e", func(context.Context) (interface{}, error) { calls++; return "record", nil }); err != nil || calls != 1 {
		t.Fatalf("a key past the bound is looked up again: %v after %d calls", err, calls)
	}
}

func TestNilLookupGuardRunsTheLookup(t *testing.T) {
	var g *LookupGuard
	v, err := g.Do(context.Background(), "hash", func(ctx context.Context) (interface{}, error) {
		if _, ok := ctx.Deadline(); !ok {
			return nil, errors.New("no timeout")
		}
		return "record", nil
	})
	if err != nil || v != "record" {
		t.Fatalf("%v %v", v, err)
	}
}
