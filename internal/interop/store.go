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
	"time"

	"github.com/jmoiron/sqlx"
)

// ErrNoTenant is returned when a store method is called without a tenant.
var ErrNoTenant = errors.New("interop: tenant id is required")

// Store is the postgres-backed interop control-plane store.
type Store struct {
	db *sqlx.DB
}

// NewStore constructs a Store.
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

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
	ID        string    `db:"id" json:"id"`
	TenantID  string    `db:"tenant_id" json:"-"`
	Name      string    `db:"name" json:"name"`
	Endpoint  string    `db:"endpoint" json:"endpoint"`
	AuthToken string    `db:"-" json:"-"` // never serialized; resolved server-side
	CreatedAt time.Time `db:"created_at" json:"created_at"`
	UpdatedAt time.Time `db:"updated_at" json:"updated_at"`
}

type remoteAgentRow struct {
	ID        string         `db:"id"`
	TenantID  string         `db:"tenant_id"`
	Name      string         `db:"name"`
	Endpoint  string         `db:"endpoint"`
	AuthToken sql.NullString `db:"auth_token"`
	CreatedAt time.Time      `db:"created_at"`
	UpdatedAt time.Time      `db:"updated_at"`
}

func (r remoteAgentRow) toAgent() RemoteAgent {
	return RemoteAgent{
		ID: r.ID, TenantID: r.TenantID, Name: r.Name, Endpoint: r.Endpoint,
		AuthToken: r.AuthToken.String, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// ListRemoteAgents returns the tenant's saved remote A2A agents (without tokens).
func (s *Store) ListRemoteAgents(ctx context.Context, tenantID string) ([]RemoteAgent, error) {
	if tenantID == "" {
		return nil, ErrNoTenant
	}
	var rows []remoteAgentRow
	if err := s.db.SelectContext(ctx, &rows,
		`SELECT id, tenant_id, name, endpoint, auth_token, created_at, updated_at
		 FROM a2a_remote_agents WHERE tenant_id = $1 ORDER BY name ASC`, tenantID); err != nil {
		return nil, err
	}
	out := make([]RemoteAgent, len(rows))
	for i, r := range rows {
		out[i] = r.toAgent()
	}
	return out, nil
}

// GetRemoteAgentByName resolves a saved remote by name (includes the token).
func (s *Store) GetRemoteAgentByName(ctx context.Context, tenantID, name string) (*RemoteAgent, error) {
	if tenantID == "" || name == "" {
		return nil, ErrNoTenant
	}
	var r remoteAgentRow
	err := s.db.GetContext(ctx, &r,
		`SELECT id, tenant_id, name, endpoint, auth_token, created_at, updated_at
		 FROM a2a_remote_agents WHERE tenant_id = $1 AND name = $2`, tenantID, name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	a := r.toAgent()
	return &a, nil
}

// UpsertRemoteAgent creates or updates a saved remote by (tenant, name).
func (s *Store) UpsertRemoteAgent(ctx context.Context, tenantID, name, endpoint, authToken string) error {
	if tenantID == "" || name == "" || endpoint == "" {
		return errors.New("interop: tenant, name and endpoint are required")
	}
	var tok sql.NullString
	if authToken != "" {
		tok = sql.NullString{String: authToken, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO a2a_remote_agents (tenant_id, name, endpoint, auth_token, updated_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (tenant_id, name)
		 DO UPDATE SET endpoint = EXCLUDED.endpoint, auth_token = EXCLUDED.auth_token, updated_at = NOW()`,
		tenantID, name, endpoint, tok)
	return err
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
