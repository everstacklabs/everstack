import { getApiBaseUrl } from '@/lib/api-url'
import type { Permission } from '@everstack/admin-core'

const baseUrl = getApiBaseUrl()

/** A single (permission, object) the UI wants to gate on. object is "type:id". */
export interface AuthzCheck {
  permission: Permission
  object: string
}

interface BatchResponse {
  granted?: string[]
}

/**
 * batchCheck asks the backend which of the given (permission, object) pairs the
 * current user is granted, using the SAME ReBAC engine the gateway enforces with
 * so the UI cannot drift from enforcement. Returns the granted entries as
 * PermissionSet keys ("permission@object").
 *
 * Degrades safely: when authz is disabled (404), the user is unauthenticated
 * (401), or the request fails, it returns []. Callers combine this with the
 * coarse role layer, so an empty result simply means "no per-resource grants —
 * fall back to the role".
 */
export async function batchCheck(checks: AuthzCheck[]): Promise<string[]> {
  if (checks.length === 0) return []
  try {
    const res = await fetch(`${baseUrl}/api/authz/batch-check`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ checks }),
    })
    if (!res.ok) return []
    const data = (await res.json().catch(() => ({}))) as BatchResponse
    return data.granted ?? []
  } catch {
    return []
  }
}
