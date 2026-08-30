package query

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// DefaultQueryBus implements QueryBus for handling read operations.
type DefaultQueryBus struct {
	handlers map[string]QueryHandler
	mu       sync.RWMutex
}

// NewQueryBus creates a new query bus.
func NewQueryBus() *DefaultQueryBus {
	return &DefaultQueryBus{
		handlers: make(map[string]QueryHandler),
	}
}

// RegisterHandler registers a query handler for a specific query type.
func (qb *DefaultQueryBus) RegisterHandler(handler QueryHandler) {
	qb.mu.Lock()
	defer qb.mu.Unlock()
	qb.handlers[handler.QueryType()] = handler
	logger.Debugf("registered query handler for type: %s", handler.QueryType())
}

// Execute routes a query to its handler and returns the result.
func (qb *DefaultQueryBus) Execute(ctx context.Context, query Query) (interface{}, error) {
	start := time.Now()
	correlationID := correlation.GetCorrelationID(ctx)

	// Validate query
	if err := query.Validate(); err != nil {
		logger.WithFields(
			"query_type", query.QueryType(),
			"correlation_id", correlationID,
			"error", err.Error(),
		).Warn("query validation failed")
		return nil, fmt.Errorf("query validation failed: %w", err)
	}

	// Find handler
	qb.mu.RLock()
	handler, exists := qb.handlers[query.QueryType()]
	qb.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no handler registered for query type: %s", query.QueryType())
	}

	logger.WithFields(
		"query_type", query.QueryType(),
		"correlation_id", correlationID,
	).Debug("executing query")

	// Execute query
	result, err := handler.Handle(ctx, query)
	elapsed := time.Since(start)

	if err != nil {
		// Context cancellation is expected (e.g., client disconnects in historical mode)
		// Log it as debug instead of error
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logger.WithFields(
				"query_type", query.QueryType(),
				"correlation_id", correlationID,
				"elapsed_ms", elapsed.Milliseconds(),
			).Debug("query canceled by client (expected)")
			return nil, err
		}

		// Actual errors should still be logged as errors
		logger.WithFields(
			"query_type", query.QueryType(),
			"correlation_id", correlationID,
			"error", err.Error(),
			"elapsed_ms", elapsed.Milliseconds(),
		).Error("query execution failed")
		return nil, fmt.Errorf("query execution failed: %w", err)
	}

	logger.WithFields(
		"query_type", query.QueryType(),
		"correlation_id", correlationID,
		"elapsed_ms", elapsed.Milliseconds(),
	).Debug("query executed successfully")

	// Wrap result with metadata
	response := &Response{
		Data:       result,
		ExecutedAt: start,
		Duration:   elapsed,
	}

	return response, nil
}

// GetRegisteredHandlers returns a list of registered query types.
func (qb *DefaultQueryBus) GetRegisteredHandlers() []string {
	qb.mu.RLock()
	defer qb.mu.RUnlock()

	types := make([]string, 0, len(qb.handlers))
	for queryType := range qb.handlers {
		types = append(types, queryType)
	}
	return types
}
