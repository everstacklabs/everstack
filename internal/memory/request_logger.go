package memory

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// RequestLog represents a single memory operation log entry.
type RequestLog struct {
	TenantID       string
	CollectionID   string
	CollectionName string
	Operation      string // "query", "add_documents", "delete_document", "create_collection", etc.
	CallerType     string // "admin", "agent", "api"
	CallerID       string
	TraceID        string
	SpanID         string
	LatencyMs      int
	ResultCount    *int
	ChunkCount     *int
	ErrorMessage   string
}

// RequestLogger asynchronously logs memory operations to the database.
type RequestLogger struct {
	db *sqlx.DB
}

// NewRequestLogger creates a new RequestLogger.
func NewRequestLogger(db *sqlx.DB) *RequestLogger {
	return &RequestLogger{db: db}
}

// Log fires and forgets a log entry insertion.
func (l *RequestLogger) Log(entry RequestLog) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := l.db.ExecContext(ctx,
			`INSERT INTO memory_request_log
				(tenant_id, collection_id, collection_name, operation, caller_type, caller_id, trace_id, span_id, latency_ms, result_count, chunk_count, error_message)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			nilIfEmpty(entry.TenantID),
			nilIfEmpty(entry.CollectionID),
			entry.CollectionName,
			entry.Operation,
			nilIfEmpty(entry.CallerType),
			nilIfEmpty(entry.CallerID),
			nilIfEmpty(entry.TraceID),
			nilIfEmpty(entry.SpanID),
			entry.LatencyMs,
			entry.ResultCount,
			entry.ChunkCount,
			nilIfEmpty(entry.ErrorMessage),
		)
		if err != nil {
			logger.WithFields("error", err.Error(), "operation", entry.Operation).
				Warn("memory: failed to log request")
		}
	}()
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
