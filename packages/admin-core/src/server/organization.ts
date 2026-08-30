import { createClientFor, createConnectTransport, timestampDate } from '@everstack/client'
import { OrganizationService } from '@everstack/proto/everstack/org/v1/org_service_pb'
import type {
    Organization as ProtoOrganization,
    OrganizationMember as ProtoMember,
    Workspace as ProtoWorkspace,
    OrganizationWithRole as ProtoOrgWithRole,
    MemberRole as ProtoMemberRole,
    WorkspaceEnvironment as ProtoWorkspaceEnv,
} from '@everstack/proto/everstack/org/v1/org_pb'
import {
    OrganizationAccessDecision as ProtoOrganizationAccessDecision,
    OrganizationIdentityEnforcementMode as ProtoOrganizationIdentityEnforcementMode,
} from '@everstack/proto/everstack/org/v1/org_pb'
import type { Timestamp } from '@everstack/client'

/**
 * Organization object
 */
export interface Organization {
    id: string
    slug: string
    name: string
    planTier: string
    billingEmail?: string
    createdAt: Date
    updatedAt: Date
}

/**
 * Organization with the current user's role
 */
export interface OrganizationWithRole {
    organization: Organization
    role: MemberRole
}

/**
 * Member roles
 */
export type MemberRole = 'owner' | 'admin' | 'member' | 'viewer'

/**
 * Organization member
 */
export interface OrganizationMember {
    id: string
    userId: string
    organizationId: string
    role: MemberRole
    joinedAt: Date
    email: string
    name?: string
    avatarUrl?: string
}

/**
 * Workspace environment types
 */
export type WorkspaceEnvironment = 'development' | 'staging' | 'production'

/**
 * Workspace object
 */
export interface Workspace {
    id: string
    organizationId: string
    slug: string
    name: string
    environment: WorkspaceEnvironment
    gatewayUrl: string
    createdAt: Date
    updatedAt: Date
}

export type OrganizationAccessDecision = 'allow' | 'require_sso' | 'deny'

export interface OrganizationAccess {
    decision: OrganizationAccessDecision
    reason: string
    organizationId: string
    organizationSlug: string
}

export type OrganizationIdentityEnforcementMode = 'optional' | 'required'

export interface OrganizationIdentityPolicy {
    organizationId: string
    enforcementMode: OrganizationIdentityEnforcementMode
    connectionId?: string
    createdAt?: Date
    updatedAt?: Date
}

export interface OrganizationIdentityConnection {
    id: string
    displayName: string
    domains: string[]
    enabled: boolean
}

export interface OrganizationIdentitySettings {
    policy: OrganizationIdentityPolicy
    connections: OrganizationIdentityConnection[]
}

/**
 * Create an organization service client
 */
export function createOrganizationClient(baseUrl?: string) {
    const transport = createConnectTransport({
        baseUrl: baseUrl || (typeof window !== 'undefined' ? window.location.origin : ''),
        // Include cookies for session auth
        fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
    })
    return createClientFor(OrganizationService)(transport)
}

/**
 * Organization client type
 */
export type OrganizationClient = ReturnType<typeof createOrganizationClient>

// ========== Converters ==========

function protoRoleToRole(protoRole: ProtoMemberRole): MemberRole {
    // Proto enum values: 0=unspecified, 1=owner, 2=admin, 3=member, 4=viewer
    switch (protoRole) {
        case 1: return 'owner'
        case 2: return 'admin'
        case 3: return 'member'
        case 4: return 'viewer'
        default: return 'member'
    }
}

function roleToProtoRole(role: MemberRole): ProtoMemberRole {
    switch (role) {
        case 'owner': return 1 as ProtoMemberRole
        case 'admin': return 2 as ProtoMemberRole
        case 'member': return 3 as ProtoMemberRole
        case 'viewer': return 4 as ProtoMemberRole
        default: return 3 as ProtoMemberRole
    }
}

function protoEnvToEnv(protoEnv: ProtoWorkspaceEnv): WorkspaceEnvironment {
    switch (protoEnv) {
        case 1: return 'development'
        case 2: return 'staging'
        case 3: return 'production'
        default: return 'development'
    }
}

function envToProtoEnv(env: WorkspaceEnvironment): ProtoWorkspaceEnv {
    switch (env) {
        case 'development': return 1 as ProtoWorkspaceEnv
        case 'staging': return 2 as ProtoWorkspaceEnv
        case 'production': return 3 as ProtoWorkspaceEnv
        default: return 1 as ProtoWorkspaceEnv
    }
}

/**
 * Safely convert a proto timestamp to a Date
 * Uses the @bufbuild/protobuf timestampDate helper
 */
function safeTimestampToDate(ts: Timestamp | undefined): Date {
    if (!ts) return new Date()
    return timestampDate(ts)
}

export function protoToOrganization(proto: ProtoOrganization): Organization {
    return {
        id: proto.id,
        slug: proto.slug,
        name: proto.name,
        planTier: proto.planTier,
        billingEmail: proto.billingEmail ?? undefined,
        createdAt: safeTimestampToDate(proto.createdAt),
        updatedAt: safeTimestampToDate(proto.updatedAt),
    }
}

export function protoToOrganizationWithRole(proto: ProtoOrgWithRole): OrganizationWithRole {
    return {
        organization: protoToOrganization(proto.organization!),
        role: protoRoleToRole(proto.role),
    }
}

export function protoToMember(proto: ProtoMember): OrganizationMember {
    return {
        id: proto.id,
        userId: proto.userId,
        organizationId: proto.organizationId,
        role: protoRoleToRole(proto.role),
        joinedAt: safeTimestampToDate(proto.joinedAt),
        email: proto.email,
        name: proto.name ?? undefined,
        avatarUrl: proto.avatarUrl ?? undefined,
    }
}

export function protoToWorkspace(proto: ProtoWorkspace): Workspace {
    return {
        id: proto.id,
        organizationId: proto.organizationId,
        slug: proto.slug,
        name: proto.name,
        environment: protoEnvToEnv(proto.environment),
        gatewayUrl: proto.gatewayUrl,
        createdAt: safeTimestampToDate(proto.createdAt),
        updatedAt: safeTimestampToDate(proto.updatedAt),
    }
}

// ========== Helper Functions ==========

/**
 * Create a new organization
 */
export async function createOrganization(
    client: OrganizationClient,
    name: string,
    slug?: string,
    billingEmail?: string
): Promise<Organization> {
    const response = await client.createOrganization({
        name,
        slug: slug ?? undefined,
        billingEmail: billingEmail ?? undefined,
    })
    return protoToOrganization(response.organization!)
}

/**
 * Get an organization by slug
 */
export async function getOrganizationBySlug(
    client: OrganizationClient,
    slug: string
): Promise<{ organization: Organization; currentUserRole: MemberRole } | null> {
    try {
        const response = await client.getOrganization({
            identifier: {
                case: "slug",
                value: slug,
            }
        })
        if (!response.organization) return null
        return {
            organization: protoToOrganization(response.organization),
            currentUserRole: protoRoleToRole(response.currentUserRole),
        }
    } catch {
        return null
    }
}

/**
 * List all organizations for the current user
 */
export async function listOrganizations(
    client: OrganizationClient
): Promise<OrganizationWithRole[]> {
    const response = await client.listOrganizations({})
    return response.organizations.map(protoToOrganizationWithRole)
}

/**
 * Evaluate organization membership and the authentication assurance used by
 * the current browser session before loading any organization-scoped data.
 */
export async function checkOrganizationAccess(
    client: OrganizationClient,
    identifier: { organizationId: string } | { organizationSlug: string },
): Promise<OrganizationAccess> {
    const response = await client.checkOrganizationAccess({
        identifier: 'organizationId' in identifier
            ? { case: 'organizationId', value: identifier.organizationId }
            : { case: 'organizationSlug', value: identifier.organizationSlug },
    })
    let decision: OrganizationAccessDecision = 'deny'
    if (response.decision === ProtoOrganizationAccessDecision.ALLOW) {
        decision = 'allow'
    } else if (response.decision === ProtoOrganizationAccessDecision.REQUIRE_SSO) {
        decision = 'require_sso'
    }
    return {
        decision,
        reason: response.reason,
        organizationId: response.organizationId,
        organizationSlug: response.organizationSlug,
    }
}

export async function getOrganizationIdentitySettings(
    client: OrganizationClient,
    organizationId: string,
): Promise<OrganizationIdentitySettings> {
    const response = await client.getOrganizationIdentitySettings({
        organizationId,
    })
    const policy = response.policy
    return {
        policy: {
            organizationId: policy?.organizationId ?? organizationId,
            enforcementMode:
                policy?.enforcementMode === ProtoOrganizationIdentityEnforcementMode.REQUIRED
                    ? 'required'
                    : 'optional',
            connectionId: policy?.connectionId,
            createdAt: policy?.createdAt ? safeTimestampToDate(policy.createdAt) : undefined,
            updatedAt: policy?.updatedAt ? safeTimestampToDate(policy.updatedAt) : undefined,
        },
        connections: response.connections.map((connection) => ({
            id: connection.id,
            displayName: connection.displayName,
            domains: [...connection.domains],
            enabled: connection.enabled,
        })),
    }
}

export async function updateOrganizationIdentityPolicy(
    client: OrganizationClient,
    organizationId: string,
    enforcementMode: OrganizationIdentityEnforcementMode,
    connectionId?: string,
): Promise<OrganizationIdentityPolicy> {
    const response = await client.updateOrganizationIdentityPolicy({
        organizationId,
        enforcementMode:
            enforcementMode === 'required'
                ? ProtoOrganizationIdentityEnforcementMode.REQUIRED
                : ProtoOrganizationIdentityEnforcementMode.OPTIONAL,
        connectionId: enforcementMode === 'required' ? connectionId : undefined,
    })
    const policy = response.policy
    if (!policy) {
        throw new Error('Organization identity policy was not returned.')
    }
    return {
        organizationId: policy.organizationId,
        enforcementMode:
            policy.enforcementMode === ProtoOrganizationIdentityEnforcementMode.REQUIRED
                ? 'required'
                : 'optional',
        connectionId: policy.connectionId,
        createdAt: policy.createdAt ? safeTimestampToDate(policy.createdAt) : undefined,
        updatedAt: policy.updatedAt ? safeTimestampToDate(policy.updatedAt) : undefined,
    }
}

/**
 * List all members of an organization
 */
export async function listMembers(
    client: OrganizationClient,
    organizationId: string
): Promise<OrganizationMember[]> {
    const response = await client.listMembers({ organizationId })
    return response.members.map(protoToMember)
}

/**
 * Invite a user to an organization by email. Returns the new invitation's id
 * and the absolute accept URL — surface the URL to the inviter as a manual
 * fallback in case email delivery fails.
 */
export async function inviteMember(
    client: OrganizationClient,
    organizationId: string,
    email: string,
    role: MemberRole,
): Promise<{ invitationId: string; invitationUrl: string }> {
    const response = await client.inviteMember({
        organizationId,
        email,
        role: roleToProtoRole(role),
    })
    return {
        invitationId: response.invitationId,
        invitationUrl: response.invitationUrl,
    }
}

/**
 * Invitation preview status returned by /api/auth/invite-preview.
 * "ok" means the rest of the fields are populated and the invitation is acceptable.
 * Other statuses signal why the invitation can't be accepted (rendered as different UI).
 */
export type InvitationPreviewStatus =
    | 'ok'
    | 'invalid'
    | 'expired'
    | 'already_accepted'
    | 'not_found'

export interface InvitationPreview {
    status: InvitationPreviewStatus
    reason?: string
    organizationId?: string
    orgName?: string
    orgSlug?: string
    email?: string
    role?: MemberRole
    inviterName?: string
    inviterEmail?: string
    expiresAt?: string
}

/**
 * Fetch the invitation preview by token. No authentication required — the
 * token itself is the capability. Throws on network error; otherwise returns
 * the preview object whose `status` field tells the caller what to render.
 */
export async function getInvitationPreview(token: string, baseUrl?: string): Promise<InvitationPreview> {
    const origin = baseUrl || (typeof window !== 'undefined' ? window.location.origin : '')
    const url = `${origin}/api/auth/invite-preview?token=${encodeURIComponent(token)}`
    const res = await fetch(url, { credentials: 'include' })
    if (!res.ok && res.status !== 200) {
        // Server returned a non-2xx with no JSON; surface a synthetic invalid.
        return { status: 'invalid', reason: `preview request failed (${res.status})` }
    }
    const json = await res.json()
    // Map snake_case → camelCase for ergonomic frontend consumption.
    return {
        status: json.status,
        reason: json.reason,
        organizationId: json.organization_id,
        orgName: json.org_name,
        orgSlug: json.org_slug,
        email: json.email,
        role: json.role as MemberRole | undefined,
        inviterName: json.inviter_name,
        inviterEmail: json.inviter_email,
        expiresAt: json.expires_at,
    }
}

/**
 * Accept an invitation. Requires a valid cloud session (cookie). Returns
 * the org slug on success so the frontend can navigate to the org dashboard.
 */
export type InvitationAcceptStatus = 'ok' | 'email_mismatch' | 'invalid' | 'expired' | 'unauthenticated'

export interface InvitationAcceptResult {
    status: InvitationAcceptStatus
    reason?: string
    orgSlug?: string
}

export async function acceptInvitation(token: string, baseUrl?: string): Promise<InvitationAcceptResult> {
    const origin = baseUrl || (typeof window !== 'undefined' ? window.location.origin : '')
    const url = `${origin}/api/auth/invite-accept?token=${encodeURIComponent(token)}`
    const res = await fetch(url, { method: 'POST', credentials: 'include' })
    const json = await res.json()
    return {
        status: json.status,
        reason: json.reason,
        orgSlug: json.org_slug,
    }
}

/**
 * List workspaces in an organization
 */
export async function listWorkspaces(
    client: OrganizationClient,
    organizationId: string
): Promise<Workspace[]> {
    const response = await client.listWorkspaces({ organizationId })
    return response.workspaces.map(protoToWorkspace)
}

/**
 * Create a workspace
 */
export async function createWorkspace(
    client: OrganizationClient,
    organizationId: string,
    name: string,
    gatewayUrl: string,
    environment: WorkspaceEnvironment = 'development',
    slug?: string
): Promise<Workspace> {
    const response = await client.createWorkspace({
        organizationId,
        name,
        slug: slug ?? undefined,
        environment: envToProtoEnv(environment),
        gatewayUrl,
    })

    return protoToWorkspace(response.workspace!)
}

/**
 * Delete a workspace
 */
export async function deleteWorkspace(
    client: OrganizationClient,
    workspaceId: string
): Promise<boolean> {
    const response = await client.deleteWorkspace({ workspaceId })
    return response.success
}
