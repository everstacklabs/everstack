// Package jtiseen tracks recently-seen JWT IDs (jti claims) to enforce
// single-use semantics on short-lived tokens.
//
// Designed for tokens whose replay window is naturally bounded by their
// exp claim — typically seconds to single-digit minutes. The tracker
// holds entries until their (caller-supplied) expiry, then sweeps. A
// token whose exp has already passed never needs to be tracked because
// signature/exp validation catches it independently.
//
// Single-process: each binary keeps its own set. Multi-replica deployments
// would need a Redis-backed implementation, but our auth + per-instance
// services are single-replica.
package jtiseen

import (
	"sync"
	"time"
)

// Seen is a thread-safe map of jti → expiry timestamp. A background
// goroutine sweeps expired entries every minute so the map can't grow
// unbounded under scanner traffic.
type Seen struct {
	mu      sync.Mutex
	entries map[string]time.Time
	stopCh  chan struct{}
}

// New constructs a Seen and launches the background sweeper. Call Stop
// to terminate the sweeper (optional — process exit is just as good).
func New() *Seen {
	s := &Seen{
		entries: make(map[string]time.Time),
		stopCh:  make(chan struct{}),
	}
	go s.sweepLoop()
	return s
}

// Stop terminates the background sweeper.
func (s *Seen) Stop() {
	select {
	case <-s.stopCh:
	default:
		close(s.stopCh)
	}
}

// CheckAndMark records the jti if not already seen, returning true. If the
// jti has already been recorded (and its tracked expiry hasn't passed),
// returns false — caller should reject the token as a replay. The expiry
// argument is the JWT's exp claim; pass time.Now() if no exp is available
// (the entry will be swept on the next 1-minute tick).
//
// Concurrency: safe to call from multiple goroutines. Atomic check + insert
// — two simultaneous CheckAndMark calls for the same jti will see exactly
// one return true.
func (s *Seen) CheckAndMark(jti string, exp time.Time) bool {
	if jti == "" {
		// Caller chose not to enforce single-use for this token. Allow.
		return true
	}
	if exp.IsZero() {
		exp = time.Now().Add(1 * time.Minute)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.entries[jti]; ok && existing.After(time.Now()) {
		return false
	}
	s.entries[jti] = exp
	return true
}

func (s *Seen) sweepLoop() {
	t := time.NewTicker(1 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case now := <-t.C:
			s.mu.Lock()
			for k, exp := range s.entries {
				if !exp.After(now) {
					delete(s.entries, k)
				}
			}
			s.mu.Unlock()
		}
	}
}
