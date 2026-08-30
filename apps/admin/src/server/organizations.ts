import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { OrganizationService } from '@everstack/proto/everstack/org/v1/org_service_pb'
import {
  ListOrganizationsRequestSchema,
  UpdateOrganizationRequestSchema,
  ListWorkspacesRequestSchema,
  UpdateWorkspaceRequestSchema,
  ListWorkspaceMembersRequestSchema,
  AddWorkspaceMemberRequestSchema,
  UpdateWorkspaceMemberRoleRequestSchema,
  RemoveWorkspaceMemberRequestSchema,
  ListAvailableWorkspaceMembersRequestSchema,
} from '@everstack/proto/everstack/org/v1/org_pb'
import type {
  ListOrganizationsResponse,
  UpdateOrganizationResponse,
  ListWorkspacesResponse,
  UpdateWorkspaceResponse,
  ListWorkspaceMembersResponse,
  AddWorkspaceMemberResponse,
  UpdateWorkspaceMemberRoleResponse,
  RemoveWorkspaceMemberResponse,
  ListAvailableWorkspaceMembersResponse,
} from '@everstack/proto/everstack/org/v1/org_pb'

const env = (
  (typeof import.meta !== 'undefined'
    ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
    : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

const transport = createServerTransport(undefined, {
  baseUrl: `${baseUrl}${connectBase}`,
  interceptors: [],
})

const organizationClient = createClientFor(OrganizationService)(transport)

export async function listOrganizations(): Promise<ListOrganizationsResponse> {
  const req = create(ListOrganizationsRequestSchema, {})
  return organizationClient.listOrganizations(req)
}

export async function updateOrganization(params: {
  organizationId: string
  name?: string
  billingEmail?: string
}): Promise<UpdateOrganizationResponse> {
  const req = create(UpdateOrganizationRequestSchema, {
    organizationId: params.organizationId,
    name: params.name,
    billingEmail: params.billingEmail,
  })

  return organizationClient.updateOrganization(req)
}

// ========== Workspace Operations ==========

export async function listWorkspaces(organizationId: string): Promise<ListWorkspacesResponse> {
  const req = create(ListWorkspacesRequestSchema, { organizationId })
  return organizationClient.listWorkspaces(req)
}

export async function updateWorkspace(params: {
  workspaceId: string
  name?: string
  gatewayUrl?: string
}): Promise<UpdateWorkspaceResponse> {
  const req = create(UpdateWorkspaceRequestSchema, {
    workspaceId: params.workspaceId,
    name: params.name,
    gatewayUrl: params.gatewayUrl,
  })

  return organizationClient.updateWorkspace(req)
}

// ========== Workspace Member Operations ==========

export async function listWorkspaceMembers(workspaceId: string): Promise<ListWorkspaceMembersResponse> {
  const req = create(ListWorkspaceMembersRequestSchema, { workspaceId })
  return organizationClient.listWorkspaceMembers(req)
}

export async function addWorkspaceMember(
  workspaceId: string,
  userId: string,
  role: number
): Promise<AddWorkspaceMemberResponse> {
  const req = create(AddWorkspaceMemberRequestSchema, { workspaceId, userId, role })
  return organizationClient.addWorkspaceMember(req)
}

export async function updateWorkspaceMemberRole(
  workspaceId: string,
  userId: string,
  role: number
): Promise<UpdateWorkspaceMemberRoleResponse> {
  const req = create(UpdateWorkspaceMemberRoleRequestSchema, { workspaceId, userId, role })
  return organizationClient.updateWorkspaceMemberRole(req)
}

export async function removeWorkspaceMember(
  workspaceId: string,
  userId: string
): Promise<RemoveWorkspaceMemberResponse> {
  const req = create(RemoveWorkspaceMemberRequestSchema, { workspaceId, userId })
  return organizationClient.removeWorkspaceMember(req)
}

export async function listAvailableWorkspaceMembers(
  workspaceId: string
): Promise<ListAvailableWorkspaceMembersResponse> {
  const req = create(ListAvailableWorkspaceMembersRequestSchema, { workspaceId })
  return organizationClient.listAvailableWorkspaceMembers(req)
}
