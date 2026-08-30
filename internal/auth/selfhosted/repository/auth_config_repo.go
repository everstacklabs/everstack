package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/jmoiron/sqlx"
)

type AuthConfigRepository struct {
	db *sqlx.DB
}

func NewAuthConfigRepository(db *sqlx.DB) *AuthConfigRepository {
	return &AuthConfigRepository{db: db}
}

func (r *AuthConfigRepository) Get(ctx context.Context) (*domain.AuthConfig, error) {
	var cfg domain.AuthConfig
	err := r.db.GetContext(ctx, &cfg, `
		SELECT auth_mode, allow_registration, cloud_organization_id, cloud_organization_slug,
		       cloud_workspace_id, cloud_workspace_slug, cloud_gateway_url, connected_at
		FROM auth_config
		ORDER BY created_at ASC
		LIMIT 1
	`)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (r *AuthConfigRepository) SetCloudManaged(ctx context.Context, organizationID, organizationSlug, workspaceID, workspaceSlug, gatewayURL string) error {
	connectedAt := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE auth_config
		SET auth_mode = 'cloud',
		    cloud_organization_id = $1,
		    cloud_organization_slug = $2,
		    cloud_workspace_id = $3,
		    cloud_workspace_slug = $4,
		    cloud_gateway_url = $5,
		    connected_at = $6,
		    updated_at = NOW()
	`, organizationID, organizationSlug, workspaceID, workspaceSlug, gatewayURL, connectedAt)
	return err
}
