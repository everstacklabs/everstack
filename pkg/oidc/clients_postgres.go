package oidc

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// PostgresClientStore is a ClientStore backed by the oidc_clients table. Each
// managed instance is registered as one confidential client at provision time.
type PostgresClientStore struct {
	db    *sqlx.DB
	table string
}

// NewPostgresClientStore builds a DB-backed client store. table defaults to
// "oidc_clients".
func NewPostgresClientStore(db *sqlx.DB, table string) *PostgresClientStore {
	if table == "" {
		table = "oidc_clients"
	}
	return &PostgresClientStore{db: db, table: table}
}

// Get implements ClientStore.
func (s *PostgresClientStore) Get(ctx context.Context, clientID string) (Client, bool, error) {
	var row struct {
		ClientKind   string `db:"client_kind"`
		OrgID        string `db:"org_id"`
		InstanceID   string `db:"instance_id"`
		RedirectURIs []byte `db:"redirect_uris"`
	}
	q := fmt.Sprintf(`SELECT client_kind, org_id, instance_id, redirect_uris FROM %s WHERE client_id = $1`, s.table)
	if err := s.db.GetContext(ctx, &row, q, clientID); err != nil {
		if err == sql.ErrNoRows {
			return Client{}, false, nil
		}
		return Client{}, false, err
	}
	var uris []string
	if len(row.RedirectURIs) > 0 {
		if err := json.Unmarshal(row.RedirectURIs, &uris); err != nil {
			return Client{}, false, fmt.Errorf("oidc: decode redirect_uris for %q: %w", clientID, err)
		}
	}
	return Client{
		ID: clientID, Kind: ClientKind(row.ClientKind), OrgID: row.OrgID,
		InstanceID: row.InstanceID, RedirectURIs: uris,
	}, true, nil
}

// Register upserts a client (idempotent). Called by dynamic client registration
// when an instance is provisioned.
func (s *PostgresClientStore) Register(ctx context.Context, c Client) error {
	uris, err := json.Marshal(c.RedirectURIs)
	if err != nil {
		return err
	}
	kind := c.EffectiveKind()
	q := fmt.Sprintf(`INSERT INTO %s (client_id, client_kind, org_id, instance_id, redirect_uris)
		VALUES ($1,$2,$3,$4,$5::jsonb)
		ON CONFLICT (client_id) DO UPDATE SET
			client_kind=EXCLUDED.client_kind, org_id=EXCLUDED.org_id, instance_id=EXCLUDED.instance_id,
			redirect_uris=EXCLUDED.redirect_uris, updated_at=NOW()`, s.table)
	_, err = s.db.ExecContext(ctx, q, c.ID, kind, c.OrgID, c.InstanceID, string(uris))
	return err
}

var _ ClientStore = (*PostgresClientStore)(nil)
