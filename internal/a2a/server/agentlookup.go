package server

import (
	"context"

	"github.com/jmoiron/sqlx"

	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

// dbAgentLookup is the production AgentLookup backed by the agents read model.
// The query is tenant-scoped (filters by tenant_id), so it can only ever
// resolve agents owned by the calling tenant.
type dbAgentLookup struct{ db *sqlx.DB }

// NewDBAgentLookup builds an AgentLookup over the given database.
func NewDBAgentLookup(db *sqlx.DB) AgentLookup { return &dbAgentLookup{db: db} }

func (l *dbAgentLookup) GetAgent(ctx context.Context, tenantID, agentID string) (string, string, bool, error) {
	if agentID == "" || tenantID == "" {
		return "", "", false, nil
	}
	q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	res, err := agentsquery.NewAgentByIDQueryHandler(l.db).Handle(ctx, q)
	if err != nil {
		return "", "", false, err
	}
	m, ok := res.(*agentsquery.AgentDefinitionReadModel)
	if !ok || m == nil {
		return "", "", false, nil
	}
	desc := ""
	if m.Description.Valid {
		desc = m.Description.String
	}
	return m.Name, desc, true, nil
}
