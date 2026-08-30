import { useMemo } from 'react'
import {
  can as roleCan,
  roleAtLeast,
  type Permission,
  type Role,
} from '@everstack/admin-core'
import { useSession } from './use-auth'

// MEMBER_ROLE enum (proto everstack.org.v1) -> canonical Role.
// 1=owner 2=admin 3=member 4=viewer (0=unspecified).
const ROLE_BY_NUMBER: Record<number, Role> = {
  1: 'owner',
  2: 'admin',
  3: 'member',
  4: 'viewer',
}

export interface OrgPermissions {
  /** Current user's role in the active org, or undefined while loading / unauthenticated. */
  role: Role | undefined
  /** Coarse role -> permission check; mirrors the backend's coarse matrix. */
  can: (perm: Permission) => boolean
  /** True when the role is at least `min` in the privilege ranking. */
  isAtLeast: (min: Role) => boolean
  /** Session still loading; callers should gate closed until this is false. */
  isLoading: boolean
}

/**
 * usePermissions exposes the current user's coarse authorization in the active
 * organization, derived from the session role. It mirrors the backend's
 * role -> permission layer (pkg/authz coarse matrix) via @everstack/admin-core,
 * so UI gating cannot drift from what the ReBAC PEP enforces.
 *
 * This is the org/workspace-level layer. Per-resource (fine-grained) decisions
 * will come from a PermissionSet fed by the backend BatchCheck endpoint.
 *
 * Fails closed: an unknown/missing role denies everything.
 */
export function usePermissions(): OrgPermissions {
  const { data: session, isLoading } = useSession()
  const roleNum = session?.user?.organizations?.[0]?.role
  return useMemo(() => {
    const role =
      typeof roleNum === 'number' ? ROLE_BY_NUMBER[roleNum] : undefined
    return {
      role,
      can: (perm: Permission) => (role ? roleCan(role, perm) : false),
      isAtLeast: (min: Role) => (role ? roleAtLeast(role, min) : false),
      isLoading,
    }
  }, [roleNum, isLoading])
}
