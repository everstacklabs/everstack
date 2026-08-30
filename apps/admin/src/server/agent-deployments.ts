import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { AgentsService } from '@everstack/proto/everstack/agents/v1/agents_service_pb'
import type {
    AgentDeployment,
    DeploymentKey,
    DeploymentInvocation,
    DeployAgentRequest,
    DeployAgentResponse,
    ListDeploymentsRequest,
    ListDeploymentsResponse,
    GetDeploymentRequest,
    GetDeploymentResponse,
    UpdateDeploymentRequest,
    UpdateDeploymentResponse,
    CreateDeploymentKeyRequest,
    CreateDeploymentKeyResponse,
    ListDeploymentKeysRequest,
    ListDeploymentKeysResponse,
    RevokeDeploymentKeyRequest,
    RevokeDeploymentKeyResponse,
    ListDeploymentInvocationsRequest,
    ListDeploymentInvocationsResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import {
    DeployAgentRequestSchema,
    ListDeploymentsRequestSchema,
    GetDeploymentRequestSchema,
    UpdateDeploymentRequestSchema,
    CreateDeploymentKeyRequestSchema,
    ListDeploymentKeysRequestSchema,
    RevokeDeploymentKeyRequestSchema,
    ListDeploymentInvocationsRequestSchema,
} from '@everstack/proto/everstack/agents/v1/agents_pb'

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
const agentsClient = createClientFor(AgentsService)(transport)

// ─── Deployments ─────────────────────────────────────────────────────

export type DeployAgentParams = {
    tenantId: string
    agentId: string
    name?: string
    description?: string
    changelog?: string
    rateLimitRpm?: number
    maxConcurrentSessions?: number
    maxTurnsPerSession?: number
    sessionTimeoutSeconds?: number
    disableSessionTracking?: boolean
}

export async function deployAgent(params: DeployAgentParams): Promise<DeployAgentResponse> {
    const req: DeployAgentRequest = create(DeployAgentRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
        name: params.name,
        description: params.description,
        changelog: params.changelog,
        rateLimitRpm: params.rateLimitRpm,
        maxConcurrentSessions: params.maxConcurrentSessions,
        maxTurnsPerSession: params.maxTurnsPerSession,
        sessionTimeoutSeconds: params.sessionTimeoutSeconds,
        disableSessionTracking: params.disableSessionTracking,
    })
    return agentsClient.deployAgent(req)
}

export type ListDeploymentsParams = {
    tenantId: string
    agentId: string
    limit?: number
    offset?: number
}

export async function listDeployments(params: ListDeploymentsParams): Promise<ListDeploymentsResponse> {
    const req: ListDeploymentsRequest = create(ListDeploymentsRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listDeployments(req)
}

export async function getDeployment(tenantId: string, id: string): Promise<GetDeploymentResponse> {
    const req: GetDeploymentRequest = create(GetDeploymentRequestSchema, { tenantId, id })
    return agentsClient.getDeployment(req)
}

export type UpdateDeploymentParams = {
    tenantId: string
    id: string
    status?: string
    rateLimitRpm?: number
    maxConcurrentSessions?: number
    maxTurnsPerSession?: number
    sessionTimeoutSeconds?: number
}

export async function updateDeployment(params: UpdateDeploymentParams): Promise<UpdateDeploymentResponse> {
    const req: UpdateDeploymentRequest = create(UpdateDeploymentRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        status: params.status,
        rateLimitRpm: params.rateLimitRpm,
        maxConcurrentSessions: params.maxConcurrentSessions,
        maxTurnsPerSession: params.maxTurnsPerSession,
        sessionTimeoutSeconds: params.sessionTimeoutSeconds,
    })
    return agentsClient.updateDeployment(req)
}

export type CreateDeploymentKeyParams = {
    tenantId: string
    deploymentId: string
    name?: string
    expiresAt?: Date
}

export async function createDeploymentKey(params: CreateDeploymentKeyParams): Promise<CreateDeploymentKeyResponse> {
    const req: CreateDeploymentKeyRequest = create(CreateDeploymentKeyRequestSchema, {
        tenantId: params.tenantId,
        deploymentId: params.deploymentId,
        name: params.name,
    })
    return agentsClient.createDeploymentKey(req)
}

export type ListDeploymentKeysParams = {
    tenantId: string
    deploymentId: string
}

export async function listDeploymentKeys(params: ListDeploymentKeysParams): Promise<ListDeploymentKeysResponse> {
    const req: ListDeploymentKeysRequest = create(ListDeploymentKeysRequestSchema, {
        tenantId: params.tenantId,
        deploymentId: params.deploymentId,
    })
    return agentsClient.listDeploymentKeys(req)
}

export async function revokeDeploymentKey(tenantId: string, keyId: string): Promise<RevokeDeploymentKeyResponse> {
    const req: RevokeDeploymentKeyRequest = create(RevokeDeploymentKeyRequestSchema, {
        tenantId,
        keyId,
    })
    return agentsClient.revokeDeploymentKey(req)
}

export type ListDeploymentInvocationsParams = {
    tenantId: string
    deploymentId: string
    limit?: number
    offset?: number
}

export async function listDeploymentInvocations(params: ListDeploymentInvocationsParams): Promise<ListDeploymentInvocationsResponse> {
    const req: ListDeploymentInvocationsRequest = create(ListDeploymentInvocationsRequestSchema, {
        tenantId: params.tenantId,
        deploymentId: params.deploymentId,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listDeploymentInvocations(req)
}

export type { AgentDeployment, DeploymentKey, DeploymentInvocation }
