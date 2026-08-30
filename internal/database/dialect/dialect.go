package dialect

import (
	"context"

	"github.com/jmoiron/sqlx"
)

// Dialect abstracts SQL differences (placeholders, DDL) per backend.
type Dialect interface {
	Name() string
	EnsureSchema(ctx context.Context, db *sqlx.DB) error
	InsertEventQuery() string
}
