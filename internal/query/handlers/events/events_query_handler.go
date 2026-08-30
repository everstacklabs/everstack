package events

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/database/sqlutil"
	eventspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/events/v1"
)

func pickDB(sys *cqrs.System) (*sqlx.DB, string, error) {
	// Prefer analytics DB (ClickHouse) for events reads; required for payload externalization
	if sys != nil && sys.AnalyticsDB != nil {
		return sys.AnalyticsDB, "clickhouse", nil
	}
	return nil, "", errors.New("analytics DB is not configured")
}

func ListEvents(ctx context.Context, sys *cqrs.System, req *eventspb.ListEventsRequest) (*eventspb.ListEventsResponse, error) {
	db, dialect, err := pickDB(sys)
	if err != nil {
		return nil, err
	}

	// Basic pagination by created_at + id; replace with your token scheme later
	limit := 100
	if req.PageSize > 0 && req.PageSize < 1000 {
		limit = int(req.PageSize)
	}

	// ClickHouse-specific query
	// Use toString for consistent String output to avoid block structure mismatch
	// when ClickHouse streams results with different part schemas
	// NOTE: We avoid formatDateTime with '%H:%M:%S' because sqlx.Named incorrectly
	// parses the colons as named parameter markers (e.g., :%M becomes :M)
	query := `
SELECT id,
       type,
       JSONExtractString(payload, 'api_key_hash')   AS api_key_hash,
       JSONExtractString(payload, 'correlation_id') AS correlation_id,
       toString(toDateTime(toUInt64(created_at))) AS created_at,
       payload,
       payload_size_bytes,
       blob_id
FROM events
WHERE tenant_id = :tenant_id
  {{and_type}}
  {{and_api_hash}}
  {{and_from}}
  {{and_to}}
ORDER BY toDateTime(created_at) DESC, id DESC
LIMIT :limit`

	args := map[string]any{"limit": limit, "tenant_id": database.TenantSchemaFromContext(ctx)}
	if req.Type != "" {
		query = strings.ReplaceAll(query, "{{and_type}}", "AND type = :type")
		args["type"] = req.Type
	}
	if req.ApiKeyHash != "" {
		query = strings.ReplaceAll(query, "{{and_api_hash}}", "AND JSONExtractString(payload, 'api_key_hash') = :api_key_hash")
		args["api_key_hash"] = req.ApiKeyHash
	}
	if req.From != "" {
		query = strings.ReplaceAll(query, "{{and_from}}", "AND toDateTime(created_at) >= parseDateTimeBestEffort(:from_ts)")
		args["from_ts"] = req.From
	}
	if req.To != "" {
		query = strings.ReplaceAll(query, "{{and_to}}", "AND toDateTime(created_at) <= parseDateTimeBestEffort(:to_ts)")
		args["to_ts"] = req.To
	}
	// remove unused placeholders
	query = strings.ReplaceAll(query, "{{and_type}}", "")
	query = strings.ReplaceAll(query, "{{and_api_hash}}", "")
	query = strings.ReplaceAll(query, "{{and_from}}", "")
	query = strings.ReplaceAll(query, "{{and_to}}", "")

	bound, bindArgs, err := sqlutil.BindNamed(dialect, query, args)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryxContext(ctx, bound, bindArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	resp := &eventspb.ListEventsResponse{}
	for rows.Next() {
		var e eventspb.Event
		var apiKeyHash sql.NullString
		var correlationID sql.NullString
		var blobID sql.NullString
		var payload sql.NullString
		if err := rows.Scan(&e.Id, &e.Type, &apiKeyHash, &correlationID, &e.CreatedAt, &payload, &e.PayloadSizeBytes, &blobID); err != nil {
			return nil, err
		}
		if apiKeyHash.Valid {
			e.ApiKeyHash = apiKeyHash.String
		}
		if correlationID.Valid {
			e.CorrelationId = correlationID.String
		}
		if blobID.Valid {
			e.BlobId = blobID.String
		}
		if payload.Valid {
			e.Payload = []byte(payload.String)
		}
		resp.Events = append(resp.Events, &e)
	}
	return resp, rows.Err()
}

func GetEvent(ctx context.Context, sys *cqrs.System, req *eventspb.GetEventRequest) (*eventspb.GetEventResponse, error) {
	db, dialect, err := pickDB(sys)
	if err != nil {
		return nil, err
	}

	query := `
SELECT id,
       type,
       JSONExtractString(payload, 'api_key_hash')   AS api_key_hash,
       JSONExtractString(payload, 'correlation_id') AS correlation_id,
       toString(toDateTime(toUInt64(created_at))) AS created_at,
       payload,
       payload_size_bytes,
       blob_id
FROM events
WHERE tenant_id = :tenant_id AND id = :id
LIMIT 1`

	bound, bindArgs, err := sqlutil.BindNamed(dialect, query, map[string]any{"id": req.Id, "tenant_id": database.TenantSchemaFromContext(ctx)})
	if err != nil {
		return nil, err
	}

	var e eventspb.Event
	var apiKeyHash sql.NullString
	var correlationID sql.NullString
	var blobID sql.NullString
	var payload sql.NullString
	if err := db.QueryRowxContext(ctx, bound, bindArgs...).Scan(
		&e.Id, &e.Type, &apiKeyHash, &correlationID, &e.CreatedAt, &e.PayloadSizeBytes, &blobID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &eventspb.GetEventResponse{}, nil
		}
		return nil, err
	}
	if apiKeyHash.Valid {
		e.ApiKeyHash = apiKeyHash.String
	}
	if correlationID.Valid {
		e.CorrelationId = correlationID.String
	}
	if blobID.Valid {
		e.BlobId = blobID.String
	}
	if payload.Valid {
		e.Payload = []byte(payload.String)
	}
	return &eventspb.GetEventResponse{Event: &e}, nil
}

func GetEventPayload(ctx context.Context, sys *cqrs.System, req *eventspb.GetEventPayloadRequest) (*eventspb.GetEventPayloadResponse, error) {
	db, dialect, err := pickDB(sys)
	if err != nil {
		return nil, err
	}

	query := `
SELECT coalesce(e.payload, b.content) AS payload
FROM events e
LEFT JOIN event_blobs b ON e.blob_id = b.blob_id AND b.tenant_id = :tenant_id
WHERE e.tenant_id = :tenant_id AND e.id = :id
LIMIT 1`
	bound, bindArgs, err := sqlutil.BindNamed(dialect, query, map[string]any{"id": req.Id, "tenant_id": database.TenantSchemaFromContext(ctx)})
	if err != nil {
		return nil, err
	}

	var payload []byte
	if err := db.QueryRowxContext(ctx, bound, bindArgs...).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return &eventspb.GetEventPayloadResponse{}, nil
		}
		return nil, err
	}
	return &eventspb.GetEventPayloadResponse{Payload: payload}, nil
}
