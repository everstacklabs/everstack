// Package scoringstate models the async-scoring state machine (traces-module-replan
// section 4.4, M3-T1): which scorers were triggered for a scope, their idempotency
// keys, attempt counts, and status. It is the Everstack analogue of Braintrust's
// _async_scoring_state. This package is pure and persistence-agnostic; a Store
// implementation (Postgres-backed) durably records the state for cross-process
// visibility and retry. The in-memory State is used to surface a summary on the
// scorer spans within a single scoring run.
package scoringstate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Status is the lifecycle state of a triggered scorer function.
type Status string

const (
	StatusPending Status = "pending"
	StatusDone    Status = "done"
	StatusFailed  Status = "failed"
)

// TriggeredFunction records one scorer's execution state within a scope.
type TriggeredFunction struct {
	FunctionID     string
	IdempotencyKey string
	Attempts       int
	Status         Status
	ScoreCount     int
}

// State aggregates triggered functions for one scope (a trace or a turn).
type State struct {
	TraceID   string
	Scope     string // "trace" | "turn"
	Functions map[string]*TriggeredFunction
}

// NewState creates an empty state for a scope.
func NewState(traceID, scope string) *State {
	return &State{TraceID: traceID, Scope: scope, Functions: map[string]*TriggeredFunction{}}
}

// IdempotencyKey derives a stable key from the scorer id, trace id, and a
// monotonic discriminator (turn number or transaction id). Re-running the same
// scorer for the same (trace, discriminator) yields the same key, so a Store can
// dedupe and avoid double-scoring.
func IdempotencyKey(functionID, traceID string, disc int64) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d", functionID, traceID, disc)))
	return hex.EncodeToString(sum[:])
}

// Trigger marks a function as pending, incrementing its attempt count, and returns
// its record (creating it on first trigger). The idempotency key is set once and
// stays stable across retries.
func (s *State) Trigger(functionID string, disc int64) *TriggeredFunction {
	tf, ok := s.Functions[functionID]
	if !ok {
		tf = &TriggeredFunction{
			FunctionID:     functionID,
			IdempotencyKey: IdempotencyKey(functionID, s.TraceID, disc),
		}
		s.Functions[functionID] = tf
	}
	tf.Attempts++
	tf.Status = StatusPending
	return tf
}

// Complete marks a function done with the number of scores it produced.
func (s *State) Complete(functionID string, scoreCount int) {
	if tf, ok := s.Functions[functionID]; ok {
		tf.Status = StatusDone
		tf.ScoreCount = scoreCount
	}
}

// Fail marks a function failed (eligible for retry by a Store).
func (s *State) Fail(functionID string) {
	if tf, ok := s.Functions[functionID]; ok {
		tf.Status = StatusFailed
	}
}

// Summary aggregates function counts by status.
type Summary struct {
	Pending int
	Done    int
	Failed  int
	Total   int
}

// Summary computes the status breakdown across all triggered functions.
func (s *State) Summary() Summary {
	var sum Summary
	for _, tf := range s.Functions {
		sum.Total++
		switch tf.Status {
		case StatusPending:
			sum.Pending++
		case StatusDone:
			sum.Done++
		case StatusFailed:
			sum.Failed++
		}
	}
	return sum
}

// String renders a compact human summary like "2 done, 1 failed" for a span
// attribute. Returns "none" when nothing was triggered.
func (sum Summary) String() string {
	var parts []string
	if sum.Done > 0 {
		parts = append(parts, fmt.Sprintf("%d done", sum.Done))
	}
	if sum.Pending > 0 {
		parts = append(parts, fmt.Sprintf("%d pending", sum.Pending))
	}
	if sum.Failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", sum.Failed))
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, ", ")
}
