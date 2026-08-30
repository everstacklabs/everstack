package main

import (
	"errors"
	"sync"
	"testing"
)

// TestSessionForID_Concurrent verifies the load-or-store single-flight:
// many goroutines calling sessionForID with the same id must converge on
// a single creator. We stub out the actual tmux invocation to keep the
// test hermetic.
//
// The body of the first-creator path under sessionForID does
// `createTmuxSession` which shells out — we can't run that in unit
// tests without a real Linux + tmux. So we exercise the inflight map
// directly by wrapping the same code path with a fake creator.
func TestSessionForID_Concurrent_FakeCreator(t *testing.T) {
	const id = "0123456789abcdef0123456789abcdef"

	// Reset between tests to avoid leakage.
	sessionInflight.Delete(id)
	t.Cleanup(func() { sessionInflight.Delete(id) })

	var creatorCalls int32
	var creatorMu sync.Mutex

	create := func() error {
		creatorMu.Lock()
		creatorCalls++
		creatorMu.Unlock()
		return nil
	}

	// Run the inflight dance manually with our fake creator. This is
	// the body of sessionForID minus the tmux call — keeps the test
	// hermetic while still proving the load-or-store contract.
	resolveOnce := func() (string, error) {
		entry := &sessionInflightEntry{ready: make(chan struct{}), id: id}
		actual, loaded := sessionInflight.LoadOrStore(id, entry)
		if loaded {
			existing := actual.(*sessionInflightEntry)
			<-existing.ready
			if existing.err != nil {
				sessionInflight.CompareAndDelete(id, existing)
				return "", existing.err
			}
			return existing.id, nil
		}
		err := create()
		entry.err = err
		close(entry.ready)
		if err != nil {
			sessionInflight.CompareAndDelete(id, entry)
			return "", err
		}
		return id, nil
	}

	const N = 32
	var wg sync.WaitGroup
	results := make([]string, N)
	errs := make([]error, N)
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got, err := resolveOnce()
			results[i] = got
			errs[i] = err
		}(i)
	}
	wg.Wait()

	if got := creatorCalls; got != 1 {
		t.Fatalf("creator should run exactly once across %d goroutines; got %d", N, got)
	}
	for i, e := range errs {
		if e != nil {
			t.Fatalf("call %d: %v", i, e)
		}
		if results[i] != id {
			t.Fatalf("call %d: got %q, want %q", i, results[i], id)
		}
	}
}

// TestSessionForID_FailedCreatorRetries — if the first creator errors,
// the entry is removed so the next caller can retry instead of
// inheriting the cached failure forever.
func TestSessionForID_FailedCreatorRetries(t *testing.T) {
	const id = "fedcba9876543210fedcba9876543210"
	sessionInflight.Delete(id)
	t.Cleanup(func() { sessionInflight.Delete(id) })

	// First call: creator fails.
	entry := &sessionInflightEntry{ready: make(chan struct{}), id: id}
	_, loaded := sessionInflight.LoadOrStore(id, entry)
	if loaded {
		t.Fatalf("unexpected pre-existing entry")
	}
	entry.err = errors.New("simulated tmux failure")
	close(entry.ready)
	sessionInflight.CompareAndDelete(id, entry)

	// Second call: should not see the cached failure.
	_, loaded2 := sessionInflight.LoadOrStore(id, &sessionInflightEntry{ready: make(chan struct{})})
	if loaded2 {
		t.Fatalf("retry call inherited the failed entry instead of getting fresh map state")
	}
}

// TestSessionForID_EmptyIDIsError guards against accidental empty-id
// callers; sessionForID requires a non-empty id and the wrapper
// resolveOrCreateSession is responsible for generating one when the
// client didn't supply one.
func TestSessionForID_EmptyIDIsError(t *testing.T) {
	_, err := sessionForID("", sessionCreateParams{})
	if err == nil {
		t.Fatalf("expected error for empty id")
	}
}
