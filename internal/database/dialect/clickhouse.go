package dialect

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/database/migrations"
)

type ClickHouse struct{}

func (ClickHouse) Name() string { return "clickhouse" }

func (ClickHouse) EnsureSchema(ctx context.Context, db *sqlx.DB) error {
	return migrations.Ensure(ctx, db, "clickhouse")
}

func (ClickHouse) InsertEventQuery() string {
	return `INSERT INTO events(tenant_id, stream, id, type, payload, created_at, payload_size_bytes, payload_hash, blob_id) VALUES (?,?,?,?,?,?,?,?,?)`
}
