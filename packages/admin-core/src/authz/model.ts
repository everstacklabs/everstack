// Canonical authorization model for the frontend — the single source of truth
// for roles and permissions, mirroring the Go pkg/authz/model.go. Both apps
// (cloud + admin) derive their UI gating from here so they cannot drift from
// each other or from the backend's coarse layer.
//
// Per-resource (fine-grained) decisions come from the backend's permission
// snapshot via PermissionSet (see permission-set.ts), NOT from this matrix —
// this matrix is only the coarse role -> permission layer.

export type Role = "owner" | "admin" | "member" | "viewer";

export const ALL_ROLES: Role[] = ["owner", "admin", "member", "viewer"];

// Privilege ranking, highest last. Used for "at least this role" checks.
const RANK: Record<Role, number> = { viewer: 1, member: 2, admin: 3, owner: 4 };

export function isRole(value: string): value is Role {
  return value in RANK;
}

/** roleAtLeast reports whether `role` is at least as privileged as `min`. */
export function roleAtLeast(role: Role, min: Role): boolean {
  return RANK[role] >= RANK[min];
}

export type Permission =
  | "org:view"
  | "org:manage_members"
  | "org:manage_billing"
  | "org:manage_workspaces"
  | "org:delete"
  | "workspace:manage"
  | "resource:view"
  | "resource:create"
  | "resource:edit"
  | "resource:delete"
  | "storage:read"
  | "storage:write"
  | "storage:manage";

export const ALL_PERMISSIONS: Permission[] = [
  "org:view",
  "org:manage_members",
  "org:manage_billing",
  "org:manage_workspaces",
  "org:delete",
  "workspace:manage",
  "resource:view",
  "resource:create",
  "resource:edit",
  "resource:delete",
  "storage:read",
  "storage:write",
  "storage:manage",
];

// Coarse role -> permissions matrix. Mirrors coarseMatrix in pkg/authz/model.go.
const ROLE_PERMISSIONS: Record<Role, ReadonlySet<Permission>> = {
  owner: new Set<Permission>([
    "org:view",
    "org:manage_members",
    "org:manage_billing",
    "org:manage_workspaces",
    "org:delete",
    "workspace:manage",
    "resource:view",
    "resource:create",
    "resource:edit",
    "resource:delete",
    "storage:read",
    "storage:write",
    "storage:manage",
  ]),
  admin: new Set<Permission>([
    "org:view",
    "org:manage_members",
    "org:manage_workspaces",
    "workspace:manage",
    "resource:view",
    "resource:create",
    "resource:edit",
    "resource:delete",
    "storage:read",
    "storage:write",
    "storage:manage",
  ]),
  member: new Set<Permission>([
    "org:view",
    "resource:view",
    "resource:create",
    "resource:edit",
    "storage:read",
    "storage:write",
  ]),
  viewer: new Set<Permission>(["org:view", "resource:view", "storage:read"]),
};

/**
 * can reports whether a bare role grants a permission in the coarse layer.
 * Use for org/workspace-level UI gating (settings, billing, member management).
 * Per-resource decisions must use the backend PermissionSet instead.
 */
export function can(
  role: Role | string | undefined,
  perm: Permission,
): boolean {
  if (!role || !isRole(role)) return false;
  return ROLE_PERMISSIONS[role].has(perm);
}

/** permissionsFor returns the coarse permissions a role grants. */
export function permissionsFor(role: Role): Permission[] {
  return ALL_PERMISSIONS.filter((p) => ROLE_PERMISSIONS[role].has(p));
}
