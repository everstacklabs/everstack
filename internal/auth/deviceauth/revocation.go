package deviceauth

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jmoiron/sqlx"
)

// CredentialsRevoked reports whether every credential of the Everstack user
// was revoked at or after the moment a CLI token was issued, so the token
// must be refused although its signature and expiry are fine.
// everstack.user_credential_revocations is written when an account is
// secured after an email change its owner did not make.
//
// iat has whole-second precision, so a token issued in the same second as
// the revocation is refused too; signing in again issues a fresh one.
//
// A database without the table (this code deployed ahead of the migration
// that adds it, or a self-hosted gateway with no platform schema) has
// revoked nothing, so the answer is false.
func CredentialsRevoked(
	ctx context.Context,
	db sqlx.QueryerContext,
	everstackUserID string,
	issuedAt time.Time,
) (bool, error) {
	var revokedAt sql.NullTime
	err := sqlx.GetContext(
		ctx,
		db,
		&revokedAt,
		`SELECT revoked_at FROM everstack.user_credential_revocations WHERE user_id::text = $1`,
		everstackUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case "42P01", // undefined_table
				"3F000": // invalid_schema_name
				return false, nil
			}
		}
		return false, err
	}
	if !revokedAt.Valid {
		return false, nil
	}
	return !issuedAt.After(revokedAt.Time), nil
}
