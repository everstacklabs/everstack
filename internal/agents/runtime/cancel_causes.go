package runtime

import "errors"

var (
	// ErrSessionCancelled marks an explicit session cancel/complete operation.
	ErrSessionCancelled = errors.New("session_cancelled")
	// ErrTurnInterrupted marks a user-requested stop of the active turn only.
	ErrTurnInterrupted = errors.New("turn_interrupted")
)
