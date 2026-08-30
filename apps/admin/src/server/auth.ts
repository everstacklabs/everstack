import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { AuthService } from '@everstack/proto/everstack/auth/v1/auth_service_pb'
import {
    GetAuthModeRequestSchema,
    GetSessionRequestSchema,
    RefreshSessionRequestSchema,
    RequestMagicLinkRequestSchema,
    VerifyMagicLinkRequestSchema,
    SignOutRequestSchema,
    InviteTeamMemberRequestSchema,
    AcceptInvitationRequestSchema,
    ListTeamMembersRequestSchema,
    RemoveTeamMemberRequestSchema,
    RevokeInvitationRequestSchema,
} from '@everstack/proto/everstack/auth/v1/auth_pb'
import type {
    GetAuthModeResponse,
    GetSessionResponse,
    RefreshSessionResponse,
    RegisterResponse,
    LoginResponse,
    RequestMagicLinkRequest,
    RequestMagicLinkResponse,
    VerifyMagicLinkRequest,
    VerifyMagicLinkResponse,
    SignOutResponse,
    InviteTeamMemberRequest,
    InviteTeamMemberResponse,
    AcceptInvitationRequest,
    AcceptInvitationResponse,
    ListTeamMembersResponse,
    RemoveTeamMemberRequest,
    RemoveTeamMemberResponse,
    RevokeInvitationRequest,
    RevokeInvitationResponse,
} from '@everstack/proto/everstack/auth/v1/auth_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// Auth endpoints don't require API key (they're public or session-based)
const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})

const authClient = createClientFor(AuthService)(transport)

/**
 * Get the authentication mode (cloud vs self-hosted)
 */
export async function getAuthMode(): Promise<GetAuthModeResponse> {
    const req = create(GetAuthModeRequestSchema, {})
    return authClient.getAuthMode(req)
}

/**
 * Get current session
 */
export async function getSession(): Promise<GetSessionResponse> {
    const req = create(GetSessionRequestSchema, {})
    return authClient.getSession(req)
}

/**
 * Refresh the current session (extends session and refreshes OAuth tokens if needed)
 */
export async function refreshSession(): Promise<RefreshSessionResponse> {
    const req = create(RefreshSessionRequestSchema, {})
    return authClient.refreshSession(req)
}

/**
 * Register first admin user (self-hosted only)
 * Uses HTTP endpoint instead of ConnectRPC to properly set cookies
 */
export async function register(
    email: string,
    password: string,
    name?: string
): Promise<RegisterResponse> {
    const response = await fetch(`${baseUrl}/auth/register`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        credentials: 'include', // Include cookies in the request/response
        body: JSON.stringify({ email, password, name }),
    })

    if (!response.ok) {
        const error = await response.json().catch(() => ({ error: 'Registration failed' }))
        throw new Error(error.error || 'Registration failed')
    }

    return response.json()
}

/**
 * Login with email/password (self-hosted only)
 * Uses HTTP endpoint instead of ConnectRPC to properly set cookies
 */
export async function login(email: string, password: string): Promise<LoginResponse> {
    const response = await fetch(`${baseUrl}/auth/login`, {
        method: 'POST',
        headers: {
            'Content-Type': 'application/json',
        },
        credentials: 'include', // Include cookies in the request/response
        body: JSON.stringify({ email, password }),
    })

    if (!response.ok) {
        const error = await response.json().catch(() => ({ error: 'Login failed' }))
        throw new Error(error.error || 'Login failed')
    }

    return response.json()
}

/**
 * Request a magic link (self-hosted only)
 */
export async function requestMagicLink(email: string): Promise<RequestMagicLinkResponse> {
    const req: RequestMagicLinkRequest = create(RequestMagicLinkRequestSchema, {
        email,
    })
    return authClient.requestMagicLink(req)
}

/**
 * Verify a magic link token (self-hosted only)
 */
export async function verifyMagicLink(token: string): Promise<VerifyMagicLinkResponse> {
    const req: VerifyMagicLinkRequest = create(VerifyMagicLinkRequestSchema, {
        token,
    })
    return authClient.verifyMagicLink(req)
}

/**
 * Sign out
 */
export async function signOut(): Promise<SignOutResponse> {
    const req = create(SignOutRequestSchema, {})
    return authClient.signOut(req)
}

/**
 * Invite a team member
 */
export async function inviteTeamMember(
    email: string,
    role: number
): Promise<InviteTeamMemberResponse> {
    const req: InviteTeamMemberRequest = create(InviteTeamMemberRequestSchema, {
        email,
        role,
    })
    return authClient.inviteTeamMember(req)
}

/**
 * Accept an invitation
 */
export async function acceptInvitation(
    token: string,
    password: string,
    name?: string
): Promise<AcceptInvitationResponse> {
    const req: AcceptInvitationRequest = create(AcceptInvitationRequestSchema, {
        token,
        password,
        name,
    })
    return authClient.acceptInvitation(req)
}

/**
 * List team members
 */
export async function listTeamMembers(): Promise<ListTeamMembersResponse> {
    const req = create(ListTeamMembersRequestSchema, {})
    return authClient.listTeamMembers(req)
}

/**
 * Remove a team member
 */
export async function removeTeamMember(userId: string): Promise<RemoveTeamMemberResponse> {
    const req: RemoveTeamMemberRequest = create(RemoveTeamMemberRequestSchema, {
        userId,
    })
    return authClient.removeTeamMember(req)
}

/**
 * Revoke an invitation
 */
export async function revokeInvitation(invitationId: string): Promise<RevokeInvitationResponse> {
    const req: RevokeInvitationRequest = create(RevokeInvitationRequestSchema, {
        invitationId,
    })
    return authClient.revokeInvitation(req)
}
