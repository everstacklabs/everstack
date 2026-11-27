// Package interop holds the admin-facing control plane for Everstack's outward
// protocol surfaces: which MCP-server tools are exposed, which agents are
// published over A2A, and the registry of external A2A agents callable via the
// call_external_agent tool. It is a thin, tenant-scoped store plus the small
// interfaces the MCP/A2A runtime reads from.
//
// Every query filters by tenant_id and fails closed on an empty tenant - these
// settings gate externally reachable surfaces, so isolation is non-negotiable.
package interop

import (
	"context"
	"database/sql"
	"errors"
	a2a "github.com/everstacklabs/everstack/internal/a2a/client"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// ErrNoTenant is returned when a store method is called without a tenant.
var ErrNoTenant = errors.New("interop: tenant id is required")

// Store is the postgres-backed interop control-plane store.
type Store struct {
	db            *sqlx.DB
	cipher        credentialCipher
	credentialErr error
}

// NewStore constructs a Store.
func NewStore(db *sqlx.DB) *Store {
	cipher, err := loadRemoteCipher()
	switch {
	case err != nil:
		logger.Warnf("interop: EVS_INTEROP_CREDENTIAL_MASTER_KEY or its previous keys are invalid; saving remote-agent tokens is disabled")
	case cipher == nil:
		logger.Warnf("interop: EVS_INTEROP_CREDENTIAL_MASTER_KEY is not set; remote agents can be saved without a token only (existing plaintext tokens still work)")
	}
	return &Store{db: db, cipher: cipher, credentialErr: err}
}

// ─── A2A publish ────────────────────────────────────────────────────

// IsAgentPublished reports whether agentID is published over A2A for the tenant.
// Opt-in: a missing row means not published.
func (s *Store) IsAgentPublished(ctx context.Context, tenantID, agentID string) (bool, error) {
	if tenantID == "" || agentID == "" {
		return false, ErrNoTenant
	}
	var enabled bool
	err := s.db.GetContext(ctx, &enabled,
		`SELECT enabled FROM a2a_published_agents WHERE tenant_id = $1 AND agent_id = $2`,
		tenantID, agentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return enabled, nil
}

// SetAgentPublished sets the A2A publish flag for an agent (upsert).
func (s *Store) SetAgentPublished(ctx context.Context, tenantID, agentID string, enabled bool) error {
	if tenantID == "" || agentID == "" {
		return ErrNoTenant
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO a2a_published_agents (tenant_id, agent_id, enabled, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (tenant_id, agent_id) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		tenantID, agentID, enabled)
	return err
}

// ListPublishedAgents returns the explicit publish state for the tenant
// (agentID -> enabled). Agents without a row are absent (treated as not published).
func (s *Store) ListPublishedAgents(ctx context.Context, tenantID string) (map[string]bool, error) {
	if tenantID == "" {
		return nil, ErrNoTenant
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT agent_id, enabled FROM a2a_published_agents WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		var en bool
		if err := rows.Scan(&id, &en); err != nil {
			return nil, err
		}
		out[id] = en
	}
	return out, rows.Err()
}

// ─── MCP tool settings ──────────────────────────────────────────────

// ToolSettings returns explicit per-tool enable/disable overrides for the tenant
// (tool name -> enabled). Tools without a row use their default (enabled).
func (s *Store) ToolSettings(ctx context.Context, tenantID string) (map[string]bool, error) {
	if tenantID == "" {
		return nil, ErrNoTenant
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT tool_name, enabled FROM mcp_tool_settings WHERE tenant_id = $1`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		var en bool
		if err := rows.Scan(&name, &en); err != nil {
			return nil, err
		}
		out[name] = en
	}
	return out, rows.Err()
}

// SetToolEnabled sets the enabled override for a tool (upsert).
func (s *Store) SetToolEnabled(ctx context.Context, tenantID, toolName string, enabled bool) error {
	if tenantID == "" || toolName == "" {
		return ErrNoTenant
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO mcp_tool_settings (tenant_id, tool_name, enabled, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (tenant_id, tool_name) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		tenantID, toolName, enabled)
	return err
}

// ─── A2A remote agents ──────────────────────────────────────────────

// RemoteAgent is a saved external A2A agent.
type RemoteAgent struct {
	ID            string    `db:"id" json:"id"`
	TenantID      string    `db:"tenant_id" json:"-"`
	Name          string    `db:"name" json:"name"`
	Endpoint      string    `db:"endpoint" json:"endpoint"`
	Protocol      string    `db:"protocol" json:"protocol"`
	RoutingTenant string    `db:"routing_tenant" json:"routing_tenant,omitempty"`
	HasCredential bool      `db:"has_credential" json:"has_credential"`
	AuthToken     string    `db:"-" json:"-"` // never serialized; resolved server-side
	CreatedAt     time.Time `db:"created_at" json:"created_at"`
	UpdatedAt     time.Time `db:"updated_at" json:"updated_at"`
}

type remoteAgentRow struct {
	ID             string         `db:"id"`
	TenantID       string         `db:"tenant_id"`
	Name           string         `db:"name"`
	Endpoint       string         `db:"endpoint"`
	Protocol       string         `db:"protocol"`
	RoutingTenant  string         `db:"routing_tenant"`
	AuthToken      sql.NullString `db:"auth_token"`
	AuthCiphertext []byte         `db:"auth_ciphertext"`
	AuthKeyID      sql.NullString `db:"auth_key_id"`
	CreatedAt      time.Time      `db:"created_at"`
	UpdatedAt      time.Time      `db:"updated_at"`
}

const remoteColumns = `id,tenant_id,name,endpoint,protocol,routing_tenant,auth_token,auth_ciphertext,auth_key_id,created_at,updated_at`

func (r remoteAgentRow) toAgent() RemoteAgent {
	return RemoteAgent{ID: r.ID, TenantID: r.TenantID, Name: r.Name, Endpoint: r.Endpoint, Protocol: r.Protocol, RoutingTenant: r.RoutingTenant, HasCredential: r.AuthToken.String != "" || len(r.AuthCiphertext) > 0, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ListRemoteAgents does not load plaintext or encrypted credential material.
func (s *Store) ListRemoteAgents(ctx context.Context, tenant string) ([]RemoteAgent, error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	list := []RemoteAgent{}
	err := s.db.SelectContext(ctx, &list, `SELECT id,tenant_id,name,endpoint,protocol,routing_tenant,(COALESCE(auth_token,'')<>'' OR auth_ciphertext IS NOT NULL) AS has_credential,created_at,updated_at FROM a2a_remote_agents WHERE tenant_id=$1 ORDER BY name`, tenant)
	return list, err
}
func (s *Store) GetRemoteAgentByName(ctx context.Context, tenant, name string) (*RemoteAgent, error) {
	return s.getRemote(ctx, tenant, name, false)
}
func (s *Store) GetRemoteAgentByID(ctx context.Context, tenant, id string) (*RemoteAgent, error) {
	return s.getRemote(ctx, tenant, id, true)
}
func (s *Store) getRemote(ctx context.Context, tenant, key string, byID bool) (*RemoteAgent, error) {
	if tenant == "" || key == "" {
		return nil, ErrNoTenant
	}
	column := "name"
	if byID {
		column = "id"
	}
	var row remoteAgentRow
	err := s.db.GetContext(ctx, &row, `SELECT `+remoteColumns+` FROM a2a_remote_agents WHERE tenant_id=$1 AND `+column+`=$2`, tenant, key)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	agent := row.toAgent()
	ref := credentialReference(row.Name, row.Endpoint, row.Protocol, row.RoutingTenant)
	if len(row.AuthCiphertext) > 0 {
		if s.cipher == nil || s.credentialErr != nil {
			return nil, ErrCredentialUnavailable
		}
		plain, err := s.cipher.Open(tenant, ref, row.AuthKeyID.String, row.AuthCiphertext)
		if err != nil {
			return nil, ErrCredentialUnavailable
		}
		agent.AuthToken = string(plain)
	} else {
		agent.AuthToken = row.AuthToken.String
		if agent.AuthToken != "" && s.credentialErr != nil {
			return nil, ErrCredentialUnavailable
		}
		// Migrate legacy plaintext on successful resolution when the operator has
		// configured the key. CAS prevents overwriting a concurrent rotation/edit.
		if agent.AuthToken != "" && s.cipher != nil && s.credentialErr == nil {
			ciphertext, keyID, err := s.cipher.Seal(tenant, ref, []byte(agent.AuthToken))
			if err != nil {
				return nil, ErrCredentialUnavailable
			}
			_, err = s.db.ExecContext(ctx, `UPDATE a2a_remote_agents SET auth_token=NULL,auth_ciphertext=$4,auth_key_id=$5 WHERE tenant_id=$1 AND id=$2 AND auth_token=$3 AND endpoint=$6 AND protocol=$7 AND routing_tenant=$8`, tenant, row.ID, agent.AuthToken, ciphertext, keyID, row.Endpoint, row.Protocol, row.RoutingTenant)
			if err != nil {
				return nil, err
			}
		}
	}
	return &agent, nil
}

// SaveRemote preserves credentials when omitted. Empty explicitly clears them.
// A changed endpoint/protocol/routing identity must not silently inherit a key.
func (s *Store) SaveRemote(ctx context.Context, tenant, name, endpoint, protocol, routing string, token *string) error {
	name = strings.TrimSpace(name)
	endpoint = strings.TrimSpace(endpoint)
	if tenant == "" || name == "" || len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return ErrInvalid
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize upserts, including the absent-row case, within this database.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenant+"/"+name); err != nil {
		return err
	}
	var old remoteAgentRow
	err = tx.GetContext(ctx, &old, `SELECT `+remoteColumns+` FROM a2a_remote_agents WHERE tenant_id=$1 AND name=$2 FOR UPDATE`, tenant, name)
	exists := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if protocol == "" {
		protocol = old.Protocol
		if protocol == "" {
			protocol = a2a.Legacy
		}
	}
	if token == nil && exists && (endpoint != old.Endpoint || protocol != old.Protocol || routing != old.RoutingTenant) && (old.AuthToken.String != "" || len(old.AuthCiphertext) > 0) {
		return ErrCredentialReplacement
	}
	ciphertext := old.AuthCiphertext
	keyID := old.AuthKeyID
	auth := ""
	if token != nil {
		auth = *token
		ciphertext = nil
		keyID = sql.NullString{}
	} else if len(ciphertext) == 0 {
		auth = old.AuthToken.String
	}
	credentialForValidation := auth
	if len(ciphertext) > 0 {
		credentialForValidation = "present"
	}
	if err = a2a.Validate(a2a.Connection{Endpoint: endpoint, Protocol: protocol, Token: credentialForValidation, Tenant: routing}); err != nil {
		return err
	}
	if auth != "" {
		if s.cipher == nil || s.credentialErr != nil {
			return ErrCredentialUnavailable
		}
		var id string
		ciphertext, id, err = s.cipher.Seal(tenant, credentialReference(name, endpoint, protocol, routing), []byte(auth))
		if err != nil {
			return ErrCredentialUnavailable
		}
		keyID = sql.NullString{String: id, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO a2a_remote_agents (tenant_id,name,endpoint,protocol,routing_tenant,auth_ciphertext,auth_key_id,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,NOW()) ON CONFLICT (tenant_id,name) DO UPDATE SET endpoint=EXCLUDED.endpoint,protocol=EXCLUDED.protocol,routing_tenant=EXCLUDED.routing_tenant,auth_token=NULL,auth_ciphertext=EXCLUDED.auth_ciphertext,auth_key_id=EXCLUDED.auth_key_id,updated_at=NOW()`, tenant, name, endpoint, protocol, routing, ciphertext, keyID)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) UpsertRemoteAgent(ctx context.Context, tenant, name, endpoint, token string) error {
	return s.SaveRemote(ctx, tenant, name, endpoint, "", "", &token)
}

// DeleteRemoteAgent removes a saved remote by id, scoped to the tenant.
func (s *Store) DeleteRemoteAgent(ctx context.Context, tenantID, id string) error {
	if tenantID == "" || id == "" {
		return ErrNoTenant
	}
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM a2a_remote_agents WHERE tenant_id = $1 AND id = $2`, tenantID, id)
	return err
}

// ─── per-tenant capability settings ─────────────────────────────────

// SettingADKRuntime is the per-tenant flag gating the run_adk_agent tool.
const SettingADKRuntime = "adk_runtime"

// GetSetting returns a per-tenant capability flag (default false / opt-in).
func (s *Store) GetSetting(ctx context.Context, tenantID, key string) (bool, error) {
	if tenantID == "" || key == "" {
		return false, ErrNoTenant
	}
	var enabled bool
	err := s.db.GetContext(ctx, &enabled,
		`SELECT enabled FROM interop_settings WHERE tenant_id = $1 AND key = $2`, tenantID, key)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return enabled, nil
}

// SetSetting upserts a per-tenant capability flag.
func (s *Store) SetSetting(ctx context.Context, tenantID, key string, enabled bool) error {
	if tenantID == "" || key == "" {
		return ErrNoTenant
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO interop_settings (tenant_id, key, enabled, updated_at)
		 VALUES ($1, $2, $3, NOW())
		 ON CONFLICT (tenant_id, key) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		tenantID, key, enabled)
	return err
}

// IsADKEnabled reports whether the tenant has opted into the ADK runtime. This
// is the per-tenant half of the two-level gate; the instance must also be
// capable (the EVS_ENABLE_ADK_RUNTIME env, an operator/platform control).
func (s *Store) IsADKEnabled(ctx context.Context, tenantID string) (bool, error) {
	return s.GetSetting(ctx, tenantID, SettingADKRuntime)
}

// SetADKEnabled sets the tenant's ADK opt-in.
func (s *Store) SetADKEnabled(ctx context.Context, tenantID string, enabled bool) error {
	return s.SetSetting(ctx, tenantID, SettingADKRuntime, enabled)
}
