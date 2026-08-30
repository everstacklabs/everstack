package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/everstacklabs/everstack/internal/database/dialect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SQLWriter is a simple sqlx-backed event appender for SQL backends.
type SQLWriter struct {
	conn *Conn
	d    dialect.Dialect
}

func NewSQLWriter(conn *Conn, d dialect.Dialect) *SQLWriter { return &SQLWriter{conn: conn, d: d} }

func (w *SQLWriter) Conn() *Conn { return w.conn }

func (w *SQLWriter) Append(ctx context.Context, events ...Event) error {
	if w.conn == nil || w.conn.RW == nil {
		return fmt.Errorf("sql writer: missing connection")
	}
	tx, err := w.conn.RW.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Choose dialect-specific insert; migrations are handled at startup, not per write
	insert := `INSERT INTO events(stream, id, type, payload, created_at) VALUES ($1,$2,$3,$4,$5)`
	if w.d != nil {
		insert = w.d.InsertEventQuery()
	}
	// Extract tenant ID from context for ClickHouse multi-tenancy.
	// Self-hosted: empty string (matches DEFAULT '').
	tenantID := TenantSchemaFromContext(ctx)

	// Loud warning when ClickHouse writes go in without a tenant. The
	// column has DEFAULT '' so the INSERT silently succeeds, and no tenant
	// query (which all filter by tenant_id) ever sees the row again —
	// that's how internal/system events become invisible "ghost" rows.
	// We don't hard-fail because self-hosted single-tenant deployments
	// intentionally have no tenant context, but this warn surfaces every
	// missing call site so we can fix them.
	if _, isClickHouse := w.d.(dialect.ClickHouse); isClickHouse && tenantID == "" && len(events) > 0 {
		streams := make(map[string]struct{}, len(events))
		types := make(map[string]struct{}, len(events))
		for _, e := range events {
			streams[e.Stream] = struct{}{}
			types[e.Type] = struct{}{}
		}
		streamList := make([]string, 0, len(streams))
		for s := range streams {
			streamList = append(streamList, s)
		}
		typeList := make([]string, 0, len(types))
		for t := range types {
			typeList = append(typeList, t)
		}
		logger.WithFields(
			"streams", streamList,
			"event_types", typeList,
			"event_count", len(events),
		).Warn("event writer: ClickHouse insert with empty tenant_id — call site missing WithTenantSchema; rows will be invisible to tenant queries")
	}

	for _, e := range events {
		// Defaults
		payloadSize := len(e.Payload)
		sum := sha256.Sum256(e.Payload)
		payloadHash := hex.EncodeToString(sum[:])
		var blobID *string // set for CH if externalized

		// For ClickHouse, optionally externalize large payloads to event_blobs
		switch w.d.(type) {
		case dialect.ClickHouse:
			// Externalize if > 64KB
			const threshold = 64 * 1024
			if payloadSize > threshold {
				id := payloadHash // deterministic id for dedupe potential
				blobID = &id
				// Best-effort insert blob; duplicates are acceptable
				if _, err := tx.ExecContext(ctx,
					"INSERT INTO event_blobs (tenant_id, blob_id, size_bytes, content) VALUES (?,?,?,?)",
					tenantID, id, payloadSize, string(e.Payload),
				); err != nil {
					logger.WithFields("error", err.Error()).Warn("failed to insert event blob; continuing")
				}
			}
			// Pass string(e.Payload) so ClickHouse stores proper UTF-8 strings;
			// []byte is stored as binary which breaks JSONExtractString queries.
			if _, err := tx.ExecContext(ctx, insert, tenantID, e.Stream, e.ID, e.Type, string(e.Payload), e.CreatedAt, payloadSize, payloadHash, blobID); err != nil {
				return err
			}
		default:
			if _, err := tx.ExecContext(ctx, insert, e.Stream, e.ID, e.Type, e.Payload, e.CreatedAt); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// MemoryWriter is a no-op/memory placeholder.
type MemoryWriter struct{ sink *[]Event }

func NewMemoryWriter() *MemoryWriter { b := make([]Event, 0); return &MemoryWriter{sink: &b} }

func (w *MemoryWriter) Append(ctx context.Context, events ...Event) error {
	if w.sink == nil {
		b := make([]Event, 0)
		w.sink = &b
	}
	*w.sink = append(*w.sink, events...)
	_, _ = json.Marshal(events) // placeholder for future logging/metrics
	return nil
}

// FanoutWriter writes to a primary writer and then best-effort to replicas.
// It fails fast if the primary writer fails, but only logs warnings for replica failures.
type FanoutWriter struct {
	primary  Writer
	replicas []Writer
}

func NewFanoutWriter(primary Writer, replicas ...Writer) *FanoutWriter {
	return &FanoutWriter{primary: primary, replicas: replicas}
}

func (w *FanoutWriter) Append(ctx context.Context, events ...Event) error {
	if w.primary == nil {
		return fmt.Errorf("fanout writer: missing primary writer")
	}
	if err := w.primary.Append(ctx, events...); err != nil {
		return err
	}
	for _, r := range w.replicas {
		if r == nil {
			continue
		}
		if err := r.Append(ctx, events...); err != nil {
			logger.WithFields("error", err.Error(), "replica", fmt.Sprintf("%T", r)).Warn("replica append failed; continuing")
		}
	}
	return nil
}

// VisibilityFilteredWriter wraps a writer and only persists events visible to users.
// Internal-only events are filtered out and not written to local storage.
// This is used to prevent usage/billing data from being stored in user-accessible databases.
type VisibilityFilteredWriter struct {
	inner       Writer
	cloudSender CloudEventSender // Optional: sends internal/both events to cloud
}

// CloudEventSender is an interface for sending events to Everstack Cloud
type CloudEventSender interface {
	SendEvents(ctx context.Context, events ...Event) error
}

// NewVisibilityFilteredWriter creates a writer that filters events by visibility.
// Only user-visible events are written to the inner writer.
// If cloudSender is provided, internal events are sent there instead.
func NewVisibilityFilteredWriter(inner Writer, cloudSender CloudEventSender) *VisibilityFilteredWriter {
	return &VisibilityFilteredWriter{
		inner:       inner,
		cloudSender: cloudSender,
	}
}

func (w *VisibilityFilteredWriter) Append(ctx context.Context, events ...Event) error {
	var userEvents []Event
	var cloudEvents []Event

	for _, e := range events {
		if e.IsVisibleToUser() {
			userEvents = append(userEvents, e)
		}
		if e.IsVisibleToCloud() {
			cloudEvents = append(cloudEvents, e)
		}
	}

	// Write user-visible events to local storage
	if len(userEvents) > 0 && w.inner != nil {
		if err := w.inner.Append(ctx, userEvents...); err != nil {
			return fmt.Errorf("local write failed: %w", err)
		}
	}

	// Send cloud events (best effort - don't fail the request)
	if len(cloudEvents) > 0 && w.cloudSender != nil {
		if err := w.cloudSender.SendEvents(ctx, cloudEvents...); err != nil {
			logger.WithFields(
				"error", err.Error(),
				"event_count", len(cloudEvents),
			).Warn("failed to send events to cloud; continuing")
			// Don't return error - cloud sync is best-effort
		}
	}

	return nil
}
