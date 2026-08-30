package cqrs

import (
	"context"
	"fmt"
)

// ContextKey for storing CQRS system in context
type ContextKey string

const CQRSSystemKey ContextKey = "cqrs_system"

// GetSystemFromContext retrieves the CQRS system from context.
func GetSystemFromContext(ctx context.Context) (*System, error) {
	if sys, ok := ctx.Value(CQRSSystemKey).(*System); ok && sys != nil {
		return sys, nil
	}
	if sys, ok := ctx.Value("cqrs_system").(*System); ok && sys != nil {
		return sys, nil
	}
	return nil, fmt.Errorf("CQRS system not found in context")
}

// WithSystem adds the CQRS system to context.
func WithSystem(ctx context.Context, system *System) context.Context {
	return context.WithValue(ctx, CQRSSystemKey, system)
}
