import { useQuery } from '@tanstack/react-query'
import { PermissionSet, EMPTY_PERMISSIONS } from '@everstack/admin-core'
import { batchCheck, type AuthzCheck } from '@/server/authz'

export interface UsePermissionSetResult {
  /** Per-resource grants from the backend; EMPTY_PERMISSIONS until loaded. */
  permissions: PermissionSet
  isLoading: boolean
}

/**
 * usePermissionSet fetches the fine-grained (per-resource) permission snapshot
 * for a set of (permission, object) checks via the backend BatchCheck endpoint,
 * and exposes it as a PermissionSet. This is the per-resource PDP mirror; combine
 * it with the coarse role layer (usePermissions) at the gate:
 *
 *   const canDelete = can('resource:delete') || permissions.has('resource:delete', obj)
 *
 * so a creator/shared user resolves via the snapshot while owners/admins resolve
 * via their role. Fails safe: an empty snapshot just means "no per-resource
 * grant — fall back to the role". Object keys are "type:id" (e.g. "dataset:42").
 */
export function usePermissionSet(checks: AuthzCheck[]): UsePermissionSetResult {
  // Stable, order-independent cache key for this set of checks.
  const key = checks
    .map((c) => `${c.permission}@${c.object}`)
    .sort()
    .join(',')

  const query = useQuery({
    queryKey: ['authz', 'batch-check', key],
    queryFn: async () => new PermissionSet(await batchCheck(checks)),
    enabled: checks.length > 0,
    staleTime: 60_000,
  })

  return {
    permissions: query.data ?? EMPTY_PERMISSIONS,
    isLoading: query.isLoading,
  }
}
