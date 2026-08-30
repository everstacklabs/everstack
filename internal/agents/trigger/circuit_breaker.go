package trigger

import (
	"context"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	// circuitResetPeriod is how long the circuit stays open before transitioning to half-open.
	circuitResetPeriod = 5 * time.Minute
)

// CircuitBreaker manages the circuit breaker state for a trigger.
type CircuitBreaker struct {
	store Store
}

// NewCircuitBreaker creates a new circuit breaker.
func NewCircuitBreaker(store Store) *CircuitBreaker {
	return &CircuitBreaker{store: store}
}

// ShouldExecute checks whether the trigger's circuit allows execution.
// Returns true if execution should proceed. For half-open circuits, allows
// a single probe execution.
func (cb *CircuitBreaker) ShouldExecute(t *Trigger) bool {
	switch t.CircuitState {
	case CircuitClosed, "":
		return true
	case CircuitHalfOpen:
		// Allow a single probe execution
		return true
	case CircuitOpen:
		// Check if reset period has elapsed
		if t.CircuitOpenedAt != nil && time.Since(*t.CircuitOpenedAt) >= circuitResetPeriod {
			return true // will transition to half-open
		}
		return false
	default:
		return true
	}
}

// TransitionToHalfOpen transitions an open circuit to half-open if the reset period has elapsed.
func (cb *CircuitBreaker) TransitionToHalfOpen(ctx context.Context, t *Trigger) error {
	if t.CircuitState == CircuitOpen && t.CircuitOpenedAt != nil && time.Since(*t.CircuitOpenedAt) >= circuitResetPeriod {
		if err := cb.store.HalfOpenCircuit(ctx, t.ID); err != nil {
			return err
		}
		t.CircuitState = CircuitHalfOpen
		logger.WithFields("trigger_id", t.ID, "name", t.Name).Info("trigger: circuit transitioned to half-open")
	}
	return nil
}

// RecordSuccess records a successful execution, closing the circuit if needed.
func (cb *CircuitBreaker) RecordSuccess(ctx context.Context, t *Trigger) {
	if t.CircuitState == CircuitHalfOpen {
		if err := cb.store.CloseCircuit(ctx, t.ID); err != nil {
			logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to close circuit")
			return
		}
		logger.WithFields("trigger_id", t.ID, "name", t.Name).Info("trigger: circuit closed after successful probe")
	} else if t.ConsecutiveFailures > 0 {
		_ = cb.store.ResetFailures(ctx, t.ID)
	}
}

// RecordFailure records a failed execution and opens the circuit if the threshold is exceeded.
// Threshold = max_retries + 1 (i.e., the original attempt plus retries).
func (cb *CircuitBreaker) RecordFailure(ctx context.Context, t *Trigger) {
	count, err := cb.store.IncrementFailures(ctx, t.ID)
	if err != nil {
		logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to increment failures")
		return
	}

	// If in half-open state, any failure re-opens the circuit
	if t.CircuitState == CircuitHalfOpen {
		if err := cb.store.OpenCircuit(ctx, t.ID); err != nil {
			logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to re-open circuit")
		}
		logger.WithFields("trigger_id", t.ID, "name", t.Name).Warn("trigger: circuit re-opened after half-open failure")
		return
	}

	threshold := t.MaxRetries + 1
	if threshold <= 0 {
		threshold = 1
	}
	if count >= threshold {
		if err := cb.store.OpenCircuit(ctx, t.ID); err != nil {
			logger.WithFields("trigger_id", t.ID, "error", err.Error()).Warn("trigger: failed to open circuit")
			return
		}
		logger.WithFields("trigger_id", t.ID, "name", t.Name, "failures", count, "threshold", threshold).
			Warn("trigger: circuit opened after consecutive failures")
	}
}
