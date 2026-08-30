// Package authz is the single source of truth for Everstack's authorization:
// the canonical role/permission model (the coarse layer) and a Zanzibar/OpenFGA
// style relationship-based access-control engine (the fine-grained layer).
//
// This package replaces three divergent copies of the role model
// (internal/auth/selfhosted, services/auth, services/organization) and the
// hardcoded CanX() boolean helpers. Both the cloud and instance planes, and
// both the Go backend and (via generated mirror) the TypeScript frontends, are
// meant to derive their authorization decisions from here so they cannot drift.
package authz

// Role is a coarse membership role. Roles are scoped to a container
// (organization or workspace); a user has at most one role per container.
type Role string

const (
	// RoleOwner is the organization owner: full control including billing and
	// deletion. There is no workspace-level owner; ownership lives at the org.
	RoleOwner Role = "owner"
	// RoleAdmin can manage members, workspaces, and resources but not billing
	// or organization deletion.
	RoleAdmin Role = "admin"
	// RoleMember can read and write resources but not manage members.
	RoleMember Role = "member"
	// RoleViewer has read-only access.
	RoleViewer Role = "viewer"
)

// AllRoles lists every valid role, highest privilege first. The DB CHECK
// constraint and any role validation should be generated from this list.
var AllRoles = []Role{RoleOwner, RoleAdmin, RoleMember, RoleViewer}

// rank orders roles by privilege (higher = more privilege). Used for
// "at least this role" comparisons. Unknown roles rank below viewer.
var rank = map[Role]int{
	RoleViewer: 1,
	RoleMember: 2,
	RoleAdmin:  3,
	RoleOwner:  4,
}

// Valid reports whether r is a known role.
func (r Role) Valid() bool {
	_, ok := rank[r]
	return ok
}

// AtLeast reports whether r is at least as privileged as other.
func (r Role) AtLeast(other Role) bool {
	return rank[r] >= rank[other]
}

// Permission is a coarse capability of the form "resource:action". Permissions
// are the vocabulary the central enforcement interceptor and the frontend
// can() helper speak. The fine-grained engine (engine.go) computes the same
// permissions as relations, so the two layers agree by construction.
type Permission string

const (
	// Organization-scoped.
	PermOrgView             Permission = "org:view"
	PermOrgManageMembers    Permission = "org:manage_members"
	PermOrgManageBilling    Permission = "org:manage_billing"
	PermOrgManageWorkspaces Permission = "org:manage_workspaces"
	PermOrgDelete           Permission = "org:delete"

	// Workspace-scoped management (create/update/delete a workspace and manage
	// its members). Resolves to workspace admin (which org owners/admins inherit).
	PermWorkspaceManage Permission = "workspace:manage"

	// Resource-scoped (agents, prompts, datasets, alerts, traces, ...). These
	// are deliberately generic: the interceptor maps an RPC to one of these and
	// the engine resolves it against the specific resource.
	PermResourceView   Permission = "resource:view"
	PermResourceCreate Permission = "resource:create"
	PermResourceEdit   Permission = "resource:edit"
	PermResourceDelete Permission = "resource:delete"

	// Tenant-scoped object storage. Storage connection management is kept
	// separate from object writes so a member can use storage without gaining
	// access to customer-managed credentials or administrative reconciliation.
	PermStorageRead   Permission = "storage:read"
	PermStorageWrite  Permission = "storage:write"
	PermStorageManage Permission = "storage:manage"
)

// AllPermissions lists every permission in the catalog. Used to generate the
// frontend mirror and to validate the RPC->permission registry at startup.
var AllPermissions = []Permission{
	PermOrgView, PermOrgManageMembers, PermOrgManageBilling, PermOrgManageWorkspaces, PermOrgDelete,
	PermWorkspaceManage,
	PermResourceView, PermResourceCreate, PermResourceEdit, PermResourceDelete,
	PermStorageRead, PermStorageWrite, PermStorageManage,
}

// coarseMatrix is the role -> permissions mapping for the coarse layer. It is
// the authoritative answer to "what can a plain org/workspace role do" and is
// what the fine-grained schema's computed relations mirror. Keeping it as data
// (not scattered CanX functions) lets the frontend consume the same table.
var coarseMatrix = map[Role]map[Permission]bool{
	RoleOwner: {
		PermOrgView: true, PermOrgManageMembers: true, PermOrgManageBilling: true,
		PermOrgManageWorkspaces: true, PermOrgDelete: true, PermWorkspaceManage: true,
		PermResourceView: true, PermResourceCreate: true, PermResourceEdit: true, PermResourceDelete: true,
		PermStorageRead: true, PermStorageWrite: true, PermStorageManage: true,
	},
	RoleAdmin: {
		PermOrgView: true, PermOrgManageMembers: true, PermOrgManageWorkspaces: true, PermWorkspaceManage: true,
		PermResourceView: true, PermResourceCreate: true, PermResourceEdit: true, PermResourceDelete: true,
		PermStorageRead: true, PermStorageWrite: true, PermStorageManage: true,
	},
	RoleMember: {
		PermOrgView:      true,
		PermResourceView: true, PermResourceCreate: true, PermResourceEdit: true,
		PermStorageRead: true, PermStorageWrite: true,
	},
	RoleViewer: {
		PermOrgView:      true,
		PermResourceView: true,
		PermStorageRead:  true,
	},
}

// Can reports whether a bare role grants a permission in the coarse layer. This
// is the fast path used where there is no per-resource grant to consider (e.g.
// org-level settings). Per-resource decisions go through the engine's Check.
func Can(role Role, perm Permission) bool {
	perms, ok := coarseMatrix[role]
	if !ok {
		return false
	}
	return perms[perm]
}

// PermissionsFor returns the set of permissions a role grants in the coarse
// layer. Used to build the frontend's permission snapshot.
func PermissionsFor(role Role) []Permission {
	out := make([]Permission, 0, len(coarseMatrix[role]))
	for _, p := range AllPermissions {
		if coarseMatrix[role][p] {
			out = append(out, p)
		}
	}
	return out
}
