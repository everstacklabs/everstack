package dialect

import (
	"context"

	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/database/migrations"
)

type Postgres struct{}

func (Postgres) Name() string { return "postgres" }

func (Postgres) EnsureSchema(ctx context.Context, db *sqlx.DB) error {
	return migrations.Ensure(ctx, db, "postgres")
}

func (Postgres) InsertEventQuery() string {
	return `INSERT INTO events(stream, id, type, payload, created_at) VALUES ($1,$2,$3,$4,$5)`
}
