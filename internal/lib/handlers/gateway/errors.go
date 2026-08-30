package gateway

import (
	"errors"
	"fmt"
)

// ErrNotImplemented is returned when a provider capability isn't implemented.
type ErrNotImplemented string

func (e ErrNotImplemented) Error() string {
	return fmt.Sprintf("provider capability not implemented: %s", string(e))
}

// RouterError is returned when routing/config selection fails for a request.
type RouterError struct {
	Message string
}

func (e RouterError) Error() string { return e.Message }

// ErrModelNotFound is returned when the requested model is not available or not activated.
// This is a non-retriable error that should NOT trigger fallback to other models.
type ErrModelNotFound struct {
	RequestedModel string
	Message        string
}

func (e *ErrModelNotFound) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("model not found: %s (%s)", e.RequestedModel, e.Message)
	}
	return fmt.Sprintf("model not found or not activated: %s", e.RequestedModel)
}

// IsModelNotFoundError checks if an error is a model not found error.
func IsModelNotFoundError(err error) bool {
	var modelNotFoundErr *ErrModelNotFound
	return errors.As(err, &modelNotFoundErr)
}

// IsNonRetriableError checks if an error should NOT trigger fallback.
// Model not found errors and not implemented errors are configuration/capability issues and should fail fast.
func IsNonRetriableError(err error) bool {
	if IsModelNotFoundError(err) {
		return true
	}

	// Check if error is ErrNotImplemented (e.g., streaming not supported)
	var notImplErr ErrNotImplemented
	if errors.As(err, &notImplErr) {
		return true
	}

	return false
}
