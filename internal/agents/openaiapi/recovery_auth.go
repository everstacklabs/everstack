package openaiapi

import (
	"context"
	"database/sql"
	"errors"

	"github.com/jmoiron/sqlx"
)

type PostgresRecoveryAuthorizer struct{ db *sqlx.DB }

func NewPostgresRecoveryAuthorizer(db *sqlx.DB) (*PostgresRecoveryAuthorizer, error) {
	if db == nil {
		return nil, ErrUnavailable
	}
	return &PostgresRecoveryAuthorizer{db: db}, nil
}
func (a *PostgresRecoveryAuthorizer) AuthorizeRecovery(ctx context.Context, o Ownership) (Ownership, error) {
	var role string
	if err := a.db.GetContext(ctx, &role, `SELECT COALESCE(role,'') FROM everstack.organization_members WHERE user_id=$1 AND organization_id=$2::uuid LIMIT 1`, o.PrincipalID, o.OrganizationID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Ownership{}, ErrForbidden
		}
		return Ownership{}, ErrUnavailable
	}
	var n int
	if err := a.db.GetContext(ctx, &n, `SELECT COUNT(*) FROM everstack.tenant_config WHERE instance_id=$1 AND organization_id=$2::uuid AND status='active'`, o.InstanceID, o.OrganizationID); err != nil {
		return Ownership{}, ErrUnavailable
	}
	if n != 1 {
		return Ownership{}, ErrForbidden
	}
	o.Role = role
	return o, nil
}
