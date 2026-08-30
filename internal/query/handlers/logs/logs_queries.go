package logs

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/query"
)

// CustomAttrColumn is one user-defined attribute-sourced log column. The
// handler projects each as max(LogAttributes[?]) so the value surfaces from
// whichever log line in the correlation group carries it. Key is a validated
// identifier (safe to inline as a map key); Ref is bound as a parameter.
type CustomAttrColumn struct {
	Key string // validated ^[a-zA-Z0-9_]+$, safe to inline as a map key
	Ref string // LogAttributes key, bound as a query parameter
}

// ListLogsQuery retrieves gateway request logs from ClickHouse OTEL logs
type ListLogsQuery struct {
	query.BaseQuery
	From   time.Time `json:"from,omitempty"`
	To     time.Time `json:"to,omitempty"`
	Offset int       `json:"offset,omitempty"`

	// CustomAttrColumns are tenant-defined columns sourced from a LogAttributes
	// key. Projected as a map column in the list query and resolved into the
	// read model's CustomAttrValues.
	CustomAttrColumns []CustomAttrColumn `json:"custom_attr_columns,omitempty"`
}

func NewListLogsQuery(from, to time.Time, userID, traceID string) *ListLogsQuery {
	return &ListLogsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
			Limit:     100,
		},
		From:   from,
		To:     to,
		Offset: 0,
	}
}

func (q ListLogsQuery) QueryType() string { return "ListLogs" }

func (q ListLogsQuery) Validate() error {
	if !q.From.IsZero() && !q.To.IsZero() && q.From.After(q.To) {
		return fmt.Errorf("from cannot be after to")
	}
	return nil
}
