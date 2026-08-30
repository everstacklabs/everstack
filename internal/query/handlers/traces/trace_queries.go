package traces

import (
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/query"
	"github.com/google/uuid"
)

// GetTraceByIDQuery retrieves all spans for a specific trace
type GetTraceByIDQuery struct {
	query.BaseQuery
	TraceID string `json:"trace_id"`
}

func NewGetTraceByIDQuery(traceID, userID, traceIDForTracking string) *GetTraceByIDQuery {
	return &GetTraceByIDQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceIDForTracking,
		},
		TraceID: traceID,
	}
}

func (q GetTraceByIDQuery) QueryType() string { return "GetTraceByID" }

func (q GetTraceByIDQuery) Validate() error {
	if q.TraceID == "" {
		return fmt.Errorf("trace_id cannot be empty")
	}
	return nil
}

// ListTracesQuery searches traces with filtering
type ListTracesQuery struct {
	query.BaseQuery
	TenantID      string    `json:"tenant_id,omitempty"`
	StartTime     time.Time `json:"start_time,omitempty"`
	EndTime       time.Time `json:"end_time,omitempty"`
	Model         string    `json:"model,omitempty"`
	Provider      string    `json:"provider,omitempty"`
	StatusCode    string    `json:"status_code,omitempty"`    // "OK", "ERROR"
	CorrelationID string    `json:"correlation_id,omitempty"` // Filter by correlation_id from SpanAttributes

	// Multi-dimension filters (P0.3)
	FilterUserID    string   `json:"filter_user_id,omitempty"`
	FilterSessionID string   `json:"filter_session_id,omitempty"`
	FilterThreadID  string   `json:"filter_thread_id,omitempty"`
	Environment     string   `json:"environment,omitempty"`
	Tags            []string `json:"tags,omitempty"`
	FullTextQuery   string   `json:"full_text_query,omitempty"`
	// Metadata predicates expressed as "key=value" strings.
	Metadata      []string            `json:"metadata,omitempty"`
	Clauses       []TraceFilterClause `json:"clauses,omitempty"`
	MinCost       *float64            `json:"min_cost,omitempty"`
	MaxCost       *float64            `json:"max_cost,omitempty"`
	MinDurationNs *int64              `json:"min_duration_ns,omitempty"`
	MaxDurationNs *int64              `json:"max_duration_ns,omitempty"`

	// CustomAttrColumns are user-defined columns sourced from a span attribute.
	// The handler projects each as max(SpanAttributes[?]) so the value surfaces
	// from whichever span carries it. Key is a validated identifier (safe to
	// inline as a map key); Ref is bound as a parameter.
	CustomAttrColumns []CustomAttrColumn `json:"custom_attr_columns,omitempty"`

	// SemanticMappings is field -> extra attribute keys (tenant aliases). Folded
	// into the coalesce for that field in the list query.
	SemanticMappings map[string][]string `json:"semantic_mappings,omitempty"`

	// ClassificationRules are tenant SpanName-pattern -> kind rules folded into
	// the trace_kinds derivation. Pattern is bound as a parameter; Kind is
	// charset-validated and inlined as the array element value.
	ClassificationRules []ClassificationRule `json:"classification_rules,omitempty"`

	// ActiveSince restricts the result to traces that received at least one span
	// since this instant, while still aggregating each of them over the FULL
	// StartTime..EndTime range.
	//
	// This exists for the live tail. Narrowing StartTime instead would be wrong:
	// the aggregate groups the spans inside the window, so a trace that began
	// four minutes ago would re-aggregate over only its most recent spans and
	// its span count, duration and token totals would visibly shrink on every
	// tick. Zero value disables the filter.
	ActiveSince time.Time `json:"active_since,omitempty"`
}

// ClassificationRule is one tenant trace-kind rule.
type ClassificationRule struct {
	Pattern string // SpanName LIKE pattern, bound as a parameter
	Kind    string // validated label, inlined as the array element value
}

// CustomAttrColumn is one user-defined attribute-sourced column.
type CustomAttrColumn struct {
	Key string // validated ^[a-zA-Z0-9_]+$, safe to inline as a map key
	Ref string // span attribute name, bound as a query parameter
}

// SemanticMappings adds tenant-supplied extra attribute keys per typed field
// (model/provider/session/user/cost/input/output/*_tokens). The handler folds
// these into the coalesce so a tenant's own attribute names populate the
// built-in columns. Keys are charset-validated before inlining.
func (q *ListTracesQuery) extraKeys(field string) []string {
	if q.SemanticMappings == nil {
		return nil
	}
	return q.SemanticMappings[field]
}

func NewListTracesQuery(tenantID string, startTime, endTime time.Time, model, provider, statusCode, correlationID, userID, traceID string) *ListTracesQuery {
	return &ListTracesQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100, // Default limit
		},
		TenantID:      tenantID,
		StartTime:     startTime,
		EndTime:       endTime,
		Model:         model,
		Provider:      provider,
		StatusCode:    statusCode,
		CorrelationID: correlationID,
	}
}

func (q ListTracesQuery) QueryType() string { return "ListTraces" }

func (q ListTracesQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}
	if q.Limit < 0 || q.Limit > 1000 {
		return fmt.Errorf("limit must be between 0 and 1000")
	}
	validStatusCodes := map[string]bool{
		"":      true,
		"OK":    true,
		"ERROR": true,
		"UNSET": true,
	}
	if !validStatusCodes[q.StatusCode] {
		return fmt.Errorf("invalid status_code: %s", q.StatusCode)
	}
	return nil
}

// GetTraceTreeQuery retrieves trace in hierarchical structure
type GetTraceTreeQuery struct {
	query.BaseQuery
	TraceID string `json:"trace_id"`
}

func NewGetTraceTreeQuery(traceID, userID, traceIDForTracking string) *GetTraceTreeQuery {
	return &GetTraceTreeQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceIDForTracking,
		},
		TraceID: traceID,
	}
}

func (q GetTraceTreeQuery) QueryType() string { return "GetTraceTree" }

func (q GetTraceTreeQuery) Validate() error {
	if q.TraceID == "" {
		return fmt.Errorf("trace_id cannot be empty")
	}
	return nil
}

// GetTraceQuery retrieves a single aggregated Trace by ID
type GetTraceQuery struct {
	query.BaseQuery
	TraceID string `json:"trace_id"`
}

func NewGetTraceQuery(traceID, userID, traceIDForTracking string) *GetTraceQuery {
	return &GetTraceQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceIDForTracking,
		},
		TraceID: traceID,
	}
}

func (q GetTraceQuery) QueryType() string { return "GetTrace" }

func (q GetTraceQuery) Validate() error {
	if q.TraceID == "" {
		return fmt.Errorf("trace_id cannot be empty")
	}
	return nil
}

// GetTraceStatsQuery retrieves aggregate trace statistics
type GetTraceStatsQuery struct {
	query.BaseQuery
	TenantID  string    `json:"tenant_id,omitempty"`
	StartTime time.Time `json:"start_time,omitempty"`
	EndTime   time.Time `json:"end_time,omitempty"`
	GroupBy   string    `json:"group_by,omitempty"` // "hour", "day"
}

func NewGetTraceStatsQuery(tenantID string, startTime, endTime time.Time, groupBy, userID, traceID string) *GetTraceStatsQuery {
	return &GetTraceStatsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100,
		},
		TenantID:  tenantID,
		StartTime: startTime,
		EndTime:   endTime,
		GroupBy:   groupBy,
	}
}

func (q GetTraceStatsQuery) QueryType() string { return "GetTraceStats" }

func (q GetTraceStatsQuery) Validate() error {
	if !q.StartTime.IsZero() && !q.EndTime.IsZero() && q.StartTime.After(q.EndTime) {
		return fmt.Errorf("start_time cannot be after end_time")
	}

	validGroupBy := map[string]bool{
		"":     true,
		"hour": true,
		"day":  true,
	}
	if !validGroupBy[q.GroupBy] {
		return fmt.Errorf("invalid group_by: %s", q.GroupBy)
	}

	return nil
}
