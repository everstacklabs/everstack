package authz

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// BackfillStats reports what a backfill wrote.
type BackfillStats struct {
	OrgMemberships       int
	WorkspaceParents     int
	WorkspaceMemberships int
	InstanceParents      int
}

// BackfillFromCloudSchema seeds the tuple store from the existing cloud
// control-plane membership tables so the ReBAC engine reflects current access
// without a flag day. It is idempotent (Write uses ON CONFLICT DO NOTHING), so
// it is safe to run repeatedly during rollout.
//
// Mapping:
//
//	everstack.organization_members(organization_id, user_id, role) -> organization:<org>#<role>@user:<uid>
//	everstack.workspaces(id, organization_id)                      -> workspace:<ws>#parent@organization:<org>
//	everstack.workspace_members(workspace_id, user_id, role)       -> workspace:<ws>#<role>@user:<uid>
//	everstack.managed_instances(id, workspace_id)                  -> instance:<inst>#parent@workspace:<ws>
func BackfillFromCloudSchema(ctx context.Context, db *sqlx.DB, store TupleStore) (BackfillStats, error) {
	var stats BackfillStats

	// The authorization graph is tenant-scoped (relation_tuples.tenant_id), so
	// every tuple is written under its owning organization's tenant. Group by
	// org, then write once per tenant. Workspace/instance rows carry the org via
	// a join so their tuples land under the same tenant as the org graph.
	byTenant := map[string][]Tuple{}

	// Org memberships.
	orgRows, err := db.QueryxContext(ctx,
		`SELECT organization_id::text, user_id::text, role FROM everstack.organization_members`)
	if err != nil {
		return stats, fmt.Errorf("authz backfill: org members: %w", err)
	}
	for orgRows.Next() {
		var orgID, userID, role string
		if err := orgRows.Scan(&orgID, &userID, &role); err != nil {
			orgRows.Close()
			return stats, err
		}
		r := Role(role)
		if !r.Valid() {
			r = RoleMember // tolerate legacy/blank roles as the safe default
		}
		byTenant[orgID] = append(byTenant[orgID], OrgMembership(orgID, userID, r))
		stats.OrgMemberships++
	}
	orgRows.Close()

	// Workspace -> org parent links.
	wsRows, err := db.QueryxContext(ctx,
		`SELECT id::text, organization_id::text FROM everstack.workspaces`)
	if err != nil {
		return stats, fmt.Errorf("authz backfill: workspaces: %w", err)
	}
	for wsRows.Next() {
		var wsID, orgID string
		if err := wsRows.Scan(&wsID, &orgID); err != nil {
			wsRows.Close()
			return stats, err
		}
		byTenant[orgID] = append(byTenant[orgID], WorkspaceParent(wsID, orgID))
		stats.WorkspaceParents++
	}
	wsRows.Close()

	// Explicit workspace memberships (org resolved via the workspace).
	wmRows, err := db.QueryxContext(ctx,
		`SELECT wm.workspace_id::text, wm.user_id::text, wm.role, w.organization_id::text
		   FROM everstack.workspace_members wm
		   JOIN everstack.workspaces w ON w.id = wm.workspace_id`)
	if err != nil {
		return stats, fmt.Errorf("authz backfill: workspace members: %w", err)
	}
	for wmRows.Next() {
		var wsID, userID, role, orgID string
		if err := wmRows.Scan(&wsID, &userID, &role, &orgID); err != nil {
			wmRows.Close()
			return stats, err
		}
		r := Role(role)
		if !r.Valid() {
			r = RoleMember
		}
		byTenant[orgID] = append(byTenant[orgID], WorkspaceMembership(wsID, userID, r))
		stats.WorkspaceMemberships++
	}
	wmRows.Close()

	// Instance -> workspace parent links (org resolved via the workspace).
	instRows, err := db.QueryxContext(ctx,
		`SELECT mi.id::text, mi.workspace_id::text, w.organization_id::text
		   FROM everstack.managed_instances mi
		   JOIN everstack.workspaces w ON w.id = mi.workspace_id`)
	if err != nil {
		return stats, fmt.Errorf("authz backfill: managed instances: %w", err)
	}
	for instRows.Next() {
		var instID, wsID, orgID string
		if err := instRows.Scan(&instID, &wsID, &orgID); err != nil {
			instRows.Close()
			return stats, err
		}
		byTenant[orgID] = append(byTenant[orgID], InstanceParent(instID, wsID))
		stats.InstanceParents++
	}
	instRows.Close()

	for tenant, tuples := range byTenant {
		if err := store.Write(ContextWithTenant(ctx, tenant), tuples...); err != nil {
			return stats, fmt.Errorf("authz backfill: write tuples for tenant %s: %w", tenant, err)
		}
	}
	return stats, nil
}
