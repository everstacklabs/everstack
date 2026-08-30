// Package query implements the Query side of CQRS pattern.
//
// This package provides:
// - Query definitions and validation
// - Query bus for routing and handling
// - Query handlers for data retrieval
// - Read models optimized for specific use cases
// - Projections from events to read models
//
// Queries represent read operations in the system.
// They are routed to appropriate handlers that return data
// from optimized read models and projections.
package query

import (
	"context"
	"time"
)

// Query represents a read operation request.
type Query interface {
	// QueryType returns the type/name of this query.
	QueryType() string
	// Validate performs basic validation of query parameters.
	Validate() error
}

// QueryHandler processes queries and returns data.
type QueryHandler interface {
	// Handle processes a query and returns the result.
	Handle(ctx context.Context, query Query) (interface{}, error)
	// QueryType returns the query type this handler can process.
	QueryType() string
}

// QueryBus coordinates query handling.
type QueryBus interface {
	// Execute routes a query to its handler and returns the result.
	Execute(ctx context.Context, query Query) (interface{}, error)
	// RegisterHandler registers a query handler for a specific query type.
	RegisterHandler(handler QueryHandler)
}

// BaseQuery provides common query functionality.
type BaseQuery struct {
	ID        string            `json:"id"`
	Timestamp time.Time         `json:"timestamp"`
	UserID    string            `json:"user_id,omitempty"`
	APIKey    string            `json:"api_key,omitempty"`
	TraceID   string            `json:"trace_id,omitempty"`
	Filters   map[string]string `json:"filters,omitempty"`
	Limit     int               `json:"limit,omitempty"`
	Offset    int               `json:"offset,omitempty"`
}

// GetID returns the query identifier.
func (q BaseQuery) GetID() string { return q.ID }

// GetTimestamp returns when the query was created.
func (q BaseQuery) GetTimestamp() time.Time { return q.Timestamp }

// GetUserID returns the user who issued the query.
func (q BaseQuery) GetUserID() string { return q.UserID }

// GetAPIKey returns the API key used for the query.
func (q BaseQuery) GetAPIKey() string { return q.APIKey }

// GetTraceID returns the trace ID for distributed tracing.
func (q BaseQuery) GetTraceID() string { return q.TraceID }

// PaginationInfo represents pagination parameters.
type PaginationInfo struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
	Total  int `json:"total"`
}

// Response wraps query results with metadata.
type Response struct {
	Data       interface{}     `json:"data"`
	Pagination *PaginationInfo `json:"pagination,omitempty"`
	ExecutedAt time.Time       `json:"executed_at"`
	Duration   time.Duration   `json:"duration"`
}
