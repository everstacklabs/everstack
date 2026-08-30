package channels

import (
	"context"
	"database/sql"
	"fmt"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// AgentLister lists available agents for the dispatcher.
type AgentLister interface {
	ListAgents(ctx context.Context, tenantID string) ([]agentrt.AgentCatalogEntry, error)
}

// agentRow is a minimal projection of agent_definitions for dispatch matching.
type agentRow struct {
	ID           string         `db:"id"`
	Name         string         `db:"name"`
	Description  sql.NullString `db:"description"`
	Model        string         `db:"model"`
	Tools        pq.StringArray `db:"tools"`
	MentionAlias sql.NullString `db:"mention_alias"`
}

// DBAgentLister queries the agents table directly.
type DBAgentLister struct {
	db *sqlx.DB
}

// NewDBAgentLister creates a new DBAgentLister.
func NewDBAgentLister(db *sqlx.DB) *DBAgentLister {
	return &DBAgentLister{db: db}
}

// ListAgents returns all enabled, non-hidden agents for a tenant.
func (l *DBAgentLister) ListAgents(ctx context.Context, tenantID string) ([]agentrt.AgentCatalogEntry, error) {
	if tenantID == "" {
		return nil, nil
	}
	var rows []agentRow
	err := l.db.SelectContext(ctx, &rows, `
		SELECT id, name, description, model, tools, mention_alias
		FROM agent_definitions
		WHERE tenant_id = $1 AND enabled = TRUE AND hidden = FALSE AND deleted_at IS NULL
		ORDER BY name ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list agents for dispatch: %w", err)
	}

	entries := make([]agentrt.AgentCatalogEntry, 0, len(rows))
	for _, r := range rows {
		entries = append(entries, agentrt.AgentCatalogEntry{
			ID:          r.ID,
			Name:        r.Name,
			Description: r.Description.String,
			Model:       r.Model,
			Tools:       r.Tools,
		})
	}
	return entries, nil
}

// agentRowWithAlias returns the mention_alias for an agent row (used internally by dispatcher).
func agentRowMentionAlias(ctx context.Context, db *sqlx.DB, tenantID, agentID string) string {
	var alias sql.NullString
	_ = db.GetContext(ctx, &alias, `SELECT mention_alias FROM agent_definitions WHERE id = $1 AND tenant_id = $2`, agentID, tenantID)
	return alias.String
}
