import { createClientFor, createConnectTransport } from '@everstack/client'
import { AuthService } from '@everstack/proto/everstack/auth/v1/auth_service_pb'
import type { UserWithOrganizations as ProtoUserWithOrgs } from '@everstack/proto/everstack/auth/v1/auth_pb'

/**
 * Organization membership for a user (simplified version used in auth context)
 */
export interface UserOrganization {
  id: string
  slug: string
  name: string
  role: 'owner' | 'admin' | 'member' | 'viewer'
}

/**
 * User object representing an authenticated user
 */
export interface User {
  id: string
  email: string
  name?: string
  avatarUrl?: string
  organizations?: UserOrganization[]
}

/**
 * User with organizations response type
 */
export interface UserWithOrganizations {
  user: User
  organizations: UserOrganization[]
}

// Map proto role enum to string
function mapRole(role: number): 'owner' | 'admin' | 'member' | 'viewer' {
  switch (role) {
    case 1: return 'owner'
    case 2: return 'admin'
    case 3: return 'member'
    case 4: return 'viewer'
    default: return 'viewer'
  }
}

/**
 * Convert proto user to our User type
 */
export function protoToUser(protoUser: ProtoUserWithOrgs): User {
  return {
    id: protoUser.user?.id ?? '',
    email: protoUser.user?.email ?? '',
    name: protoUser.user?.name ?? undefined,
    avatarUrl: protoUser.user?.avatarUrl ?? undefined,
    organizations: protoUser.organizations?.map(org => ({
      id: org.id,
      slug: org.slug,
      name: org.name,
      role: mapRole(org.role),
    })),
  }
}

/**
 * Create an auth service client
 */
export function createAuthClient(baseUrl?: string) {
  const transport = createConnectTransport({
    baseUrl: baseUrl || (typeof window !== 'undefined' ? window.location.origin : ''),
    // Include cookies for session auth
    fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
  })
  return createClientFor(AuthService)(transport)
}

/**
 * Auth client type for external use
 */
export type AuthClient = ReturnType<typeof createAuthClient>
