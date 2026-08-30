import { createServerTransport } from '@/server'
import { Code, ConnectError, createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { AgentsService } from '@everstack/proto/everstack/agents/v1/agents_service_pb'
import type {
    ListAgentsRequest,
    ListAgentsResponse,
    AgentDefinition,
    CreateAgentRequest,
    CreateAgentResponse,
    GetAgentRequest,
    GetAgentResponse,
    UpdateAgentRequest,
    UpdateAgentResponse,
    DeleteAgentRequest,
    DeleteAgentResponse,
    AgentRevision,
    GetActiveAgentRevisionRequest,
    CreateSessionRequest,
    CreateSessionResponse,
    GetSessionRequest,
    GetSessionResponse,
    ListSessionsRequest,
    ListSessionsResponse,
    CancelSessionRequest,
    CancelSessionResponse,
    CompleteSessionResponse,
    SteerSessionRequest,
    SteerSessionResponse,
    SubmitReviewRequest,
    SubmitReviewResponse,
    GetReviewRequest,
    GetReviewResponse,
    ListReviewsRequest,
    ListReviewsResponse,
    AgentSession,
    AgentSessionTurn,
    ApprovalReview,
    PendingToolCall,
    ToolCallDecision,
    AgentEvent,
    ListAgentMemoriesRequest,
    ListAgentMemoriesResponse,
    CreateAgentMemoryRequest,
    CreateAgentMemoryResponse,
    DeleteAgentMemoryRequest,
    DeleteAgentMemoryResponse,
    UpdateAgentMemoryRequest,
    UpdateAgentMemoryResponse,
    DeactivateAgentMemoryRequest,
    DeactivateAgentMemoryResponse,
    AgentMemoryEntry,
    AgentLink,
    CreateAgentLinkRequest,
    CreateAgentLinkResponse,
    ListAgentLinksRequest,
    ListAgentLinksResponse,
    DeleteAgentLinkRequest,
    DeleteAgentLinkResponse,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import {
    ListAgentsRequestSchema,
    CreateAgentRequestSchema,
    GetAgentRequestSchema,
    UpdateAgentRequestSchema,
    DeleteAgentRequestSchema,
    GetActiveAgentRevisionRequestSchema,
    CreateSessionRequestSchema,
    GetSessionRequestSchema,
    ListSessionsRequestSchema,
    CancelSessionRequestSchema,
    CompleteSessionRequestSchema,
    SteerSessionRequestSchema,
    SubmitReviewRequestSchema,
    GetReviewRequestSchema,
    ListReviewsRequestSchema,
    ListAgentMemoriesRequestSchema,
    CreateAgentMemoryRequestSchema,
    DeleteAgentMemoryRequestSchema,
    UpdateAgentMemoryRequestSchema,
    DeactivateAgentMemoryRequestSchema,
    AgentMemoryConfigSchema,
    CreateAgentLinkRequestSchema,
    ListAgentLinksRequestSchema,
    DeleteAgentLinkRequestSchema,
    AgentLinkType,
    AgentLinkProtocol,
    SessionStatus,
    TurnStatus,
    ApprovalReviewStatus,
    ApprovalAction,
    AgentMode,
    TaskPermissionMode,
    AgentLifecycleMode,
    AgentLifecycleStatus,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
type JsonObject = { [k: string]: JsonValue }
type JsonValue = number | string | boolean | null | JsonObject | JsonValue[]

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

// ─── Agent CRUD ──────────────────────────────────────────────────────

export type SandboxConfigParams = {
    image?: string
    cpuLimit?: number
    memoryMb?: number
    diskMb?: number
    timeoutSeconds?: number
    networkMode?: string
    allowedHosts?: string[]
    envVars?: Record<string, string>
    sshEnabled?: boolean
    gitRepoUrl?: string
    gitBranch?: string
    linkedSessionId?: string
}

export type CreateAgentParams = {
    tenantId: string
    name: string
    model: string
    description?: string
    systemPrompt?: string
    tools?: string[]
    config?: JsonObject
    maxTurns?: number
    maxToolCallsPerTurn?: number
    mode?: AgentMode
    maxSteps?: number
    taskPermissionMode?: TaskPermissionMode
    hidden?: boolean
    color?: string
    workingDirectory?: string
    mentionAlias?: string
    lifecycleMode?: AgentLifecycleMode
    identity?: AgentIdentityParams
    sandboxConfig?: SandboxConfigParams
    autoProvision?: boolean
}

export async function createAgent(params: CreateAgentParams): Promise<CreateAgentResponse> {
    const mode = params.mode ?? AgentMode.PRIMARY
    const taskPermissionMode = params.taskPermissionMode ?? TaskPermissionMode.ASK
    const lifecycleMode = params.lifecycleMode ?? AgentLifecycleMode.EPHEMERAL
    const req: CreateAgentRequest = create(CreateAgentRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        model: params.model,
        description: params.description,
        systemPrompt: params.systemPrompt,
        tools: params.tools ?? [],
        config: params.config,
        maxTurns: params.maxTurns,
        maxToolCallsPerTurn: params.maxToolCallsPerTurn,
        mode,
        maxSteps: params.maxSteps,
        taskPermissionMode,
        hidden: params.hidden,
        color: params.color,
        workingDirectory: params.workingDirectory,
        mentionAlias: params.mentionAlias,
        lifecycleMode,
        executionPolicy: {
            taskPermissionMode,
            maxSteps: params.maxSteps,
            workingDirectory: params.workingDirectory,
        },
        ...(params.identity ? {
            identity: {
                soulMd: params.identity.soulMd ?? '',
                identityMd: params.identity.identityMd ?? '',
                userMd: params.identity.userMd ?? '',
                roleMd: params.identity.roleMd ?? '',
            },
        } : {}),
        ...(params.sandboxConfig ? {
            sandboxConfig: {
                image: params.sandboxConfig.image ?? '',
                cpuLimit: params.sandboxConfig.cpuLimit ?? 0,
                memoryMb: BigInt(params.sandboxConfig.memoryMb ?? 0),
                diskMb: BigInt(params.sandboxConfig.diskMb ?? 0),
                timeoutSeconds: params.sandboxConfig.timeoutSeconds ?? 0,
                networkMode: params.sandboxConfig.networkMode ?? '',
                allowedHosts: params.sandboxConfig.allowedHosts ?? [],
                envVars: params.sandboxConfig.envVars ?? {},
                sshEnabled: params.sandboxConfig.sshEnabled ?? false,
                gitRepoUrl: params.sandboxConfig.gitRepoUrl ?? '',
                gitBranch: params.sandboxConfig.gitBranch ?? '',
                ...(params.sandboxConfig.linkedSessionId ? { linkedSessionId: params.sandboxConfig.linkedSessionId } : {}),
            },
        } : {}),
        autoProvision: params.autoProvision ?? false,
    })
    return agentsClient.createAgent(req)
}

export async function getAgent(id: string, tenantId: string): Promise<GetAgentResponse> {
    const req: GetAgentRequest = create(GetAgentRequestSchema, { tenantId, id })
    return agentsClient.getAgent(req)
}

export async function getActiveAgentRevision(agentId: string, tenantId: string): Promise<AgentRevision | null> {
    const req: GetActiveAgentRevisionRequest = create(GetActiveAgentRevisionRequestSchema, {
        tenantId,
        agentId,
    })
    try {
        const response = await agentsClient.getActiveAgentRevision(req)
        return response.revision ?? null
    } catch (error) {
        if (error instanceof ConnectError && (error.code === Code.NotFound || error.code === Code.Unimplemented)) {
            return null
        }
        throw error
    }
}

export type ListAgentsParams = {
    tenantId?: string
    enabled?: boolean
    limit?: number
    offset?: number
    includeHidden?: boolean
    mode?: AgentMode
    lifecycleMode?: 'ephemeral' | 'persistent'
}

export async function listAgents(params: ListAgentsParams = {}): Promise<ListAgentsResponse> {
    const { tenantId, enabled, limit, offset, includeHidden, mode, lifecycleMode } = params
    const req: ListAgentsRequest = create(ListAgentsRequestSchema, {
        tenantId: tenantId ?? '',
        enabled,
        limit,
        offset,
        includeHidden: includeHidden ?? false,
        mode: mode ?? AgentMode.UNSPECIFIED,
        // lifecycleMode filter: 1 = ephemeral, 2 = persistent (proto enum values)
        ...(lifecycleMode === 'ephemeral' ? { lifecycleMode: 1 } :
            lifecycleMode === 'persistent' ? { lifecycleMode: 2 } : {}),
    })
    return agentsClient.listAgents(req)
}

export type AgentIdentityParams = {
    soulMd?: string
    identityMd?: string
    userMd?: string
    roleMd?: string
}

export type AgentMemoryConfigParams = {
    enabled?: boolean
    scope?: string
    autoRetrieve?: boolean
    autoRetrieveTopK?: number
    autoExtract?: boolean
}

export type UpdateAgentParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    model?: string
    systemPrompt?: string
    tools?: string[]
    config?: JsonObject
    maxTurns?: number
    maxToolCallsPerTurn?: number
    enabled?: boolean
    mode?: AgentMode
    maxSteps?: number
    taskPermissionMode?: TaskPermissionMode
    hidden?: boolean
    color?: string
    workingDirectory?: string
    mentionAlias?: string
    lifecycleMode?: AgentLifecycleMode
    identity?: AgentIdentityParams
    memoryConfig?: AgentMemoryConfigParams
    sandboxConfig?: SandboxConfigParams
}

export async function updateAgent(params: UpdateAgentParams): Promise<UpdateAgentResponse> {
    const executionPolicy = params.taskPermissionMode !== undefined || params.maxSteps !== undefined || params.workingDirectory !== undefined
        ? {
            taskPermissionMode: params.taskPermissionMode ?? TaskPermissionMode.UNSPECIFIED,
            maxSteps: params.maxSteps,
            workingDirectory: params.workingDirectory,
        }
        : undefined

    const identity = params.identity
        ? {
            soulMd: params.identity.soulMd ?? '',
            identityMd: params.identity.identityMd ?? '',
            userMd: params.identity.userMd ?? '',
            roleMd: params.identity.roleMd ?? '',
        }
        : undefined

    const memoryConfig = params.memoryConfig
        ? create(AgentMemoryConfigSchema, {
            enabled: params.memoryConfig.enabled ?? false,
            scope: params.memoryConfig.scope ?? '',
            autoRetrieve: params.memoryConfig.autoRetrieve ?? false,
            autoRetrieveTopK: params.memoryConfig.autoRetrieveTopK ?? 0,
            autoExtract: params.memoryConfig.autoExtract ?? false,
        })
        : undefined

    const req: UpdateAgentRequest = create(UpdateAgentRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        model: params.model,
        systemPrompt: params.systemPrompt,
        tools: params.tools ?? [],
        clearTools: params.tools !== undefined && params.tools.length === 0,
        config: params.config,
        maxTurns: params.maxTurns,
        maxToolCallsPerTurn: params.maxToolCallsPerTurn,
        enabled: params.enabled,
        mode: params.mode,
        maxSteps: params.maxSteps,
        taskPermissionMode: params.taskPermissionMode,
        hidden: params.hidden,
        color: params.color,
        workingDirectory: params.workingDirectory,
        mentionAlias: params.mentionAlias,
        lifecycleMode: params.lifecycleMode,
        executionPolicy,
        memoryConfig,
        identity,
        ...(params.sandboxConfig ? {
            sandboxConfig: {
                image: params.sandboxConfig.image ?? '',
                cpuLimit: params.sandboxConfig.cpuLimit ?? 0,
                memoryMb: BigInt(params.sandboxConfig.memoryMb ?? 0),
                diskMb: BigInt(params.sandboxConfig.diskMb ?? 0),
                timeoutSeconds: params.sandboxConfig.timeoutSeconds ?? 0,
                networkMode: params.sandboxConfig.networkMode ?? '',
                allowedHosts: params.sandboxConfig.allowedHosts ?? [],
                envVars: params.sandboxConfig.envVars ?? {},
                sshEnabled: params.sandboxConfig.sshEnabled ?? false,
                gitRepoUrl: params.sandboxConfig.gitRepoUrl ?? '',
                gitBranch: params.sandboxConfig.gitBranch ?? '',
                ...(params.sandboxConfig.linkedSessionId ? { linkedSessionId: params.sandboxConfig.linkedSessionId } : {}),
            },
        } : {}),
    })
    return agentsClient.updateAgent(req)
}

export async function deleteAgent(id: string, tenantId: string): Promise<DeleteAgentResponse> {
    const req: DeleteAgentRequest = create(DeleteAgentRequestSchema, { tenantId, id })
    return agentsClient.deleteAgent(req)
}

// ─── Session Management ──────────────────────────────────────────────

export type CreateSessionParams = {
    tenantId: string
    agentId: string
    metadata?: JsonObject
}

export async function createSession(params: CreateSessionParams): Promise<CreateSessionResponse> {
    const req: CreateSessionRequest = create(CreateSessionRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
        metadata: params.metadata,
    })
    return agentsClient.createSession(req)
}

export async function getSession(id: string, tenantId: string): Promise<GetSessionResponse> {
    const req: GetSessionRequest = create(GetSessionRequestSchema, { tenantId, id })
    return agentsClient.getSession(req)
}

export type ListSessionsParams = {
    tenantId?: string
    agentId?: string
    trooperId?: string
    status?: SessionStatus
    limit?: number
    offset?: number
}

export async function listSessions(params: ListSessionsParams = {}): Promise<ListSessionsResponse> {
    const req: ListSessionsRequest = create(ListSessionsRequestSchema, {
        tenantId: params.tenantId ?? '',
        agentId: params.agentId,
        trooperId: params.trooperId,
        status: params.status,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listSessions(req)
}

export async function cancelSession(sessionId: string, tenantId: string): Promise<CancelSessionResponse> {
    const req: CancelSessionRequest = create(CancelSessionRequestSchema, { tenantId, sessionId })
    return agentsClient.cancelSession(req)
}

export async function completeSession(sessionId: string, tenantId: string): Promise<CompleteSessionResponse> {
    const req = create(CompleteSessionRequestSchema, { tenantId, sessionId })
    return agentsClient.completeSession(req)
}

export type SteerSessionParams = {
    tenantId: string
    sessionId: string
    role: string
    content: string
}

export async function steerSession(params: SteerSessionParams): Promise<SteerSessionResponse> {
    const req: SteerSessionRequest = create(SteerSessionRequestSchema, {
        tenantId: params.tenantId,
        sessionId: params.sessionId,
        role: params.role,
        content: params.content,
    })
    return agentsClient.steerSession(req)
}

// ─── HITL Approval Reviews ──────────────────────────────────────────

export type SubmitReviewParams = {
    tenantId: string
    reviewId: string
    action: ApprovalAction
    decisions?: ToolCallDecision[]
    reason?: string
    resolvedBy?: string
}

export async function submitReview(params: SubmitReviewParams): Promise<SubmitReviewResponse> {
    const req: SubmitReviewRequest = create(SubmitReviewRequestSchema, {
        tenantId: params.tenantId,
        reviewId: params.reviewId,
        action: params.action,
        decisions: params.decisions ?? [],
        reason: params.reason ?? '',
        resolvedBy: params.resolvedBy ?? '',
    })
    return agentsClient.submitReview(req)
}

export async function getReview(reviewId: string, tenantId: string): Promise<GetReviewResponse> {
    const req: GetReviewRequest = create(GetReviewRequestSchema, { tenantId, reviewId })
    return agentsClient.getReview(req)
}

export type ListReviewsParams = {
    tenantId?: string
    sessionId?: string
    status?: ApprovalReviewStatus
    limit?: number
    offset?: number
}

export async function listReviews(params: ListReviewsParams = {}): Promise<ListReviewsResponse> {
    const req: ListReviewsRequest = create(ListReviewsRequestSchema, {
        tenantId: params.tenantId ?? '',
        sessionId: params.sessionId,
        status: params.status,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listReviews(req)
}

// ─── Agent Memory ────────────────────────────────────────────────────

export type ListAgentMemoriesParams = {
    tenantId?: string
    agentId: string
    memoryType?: string
    scope?: string
    activeOnly?: boolean
    limit?: number
    offset?: number
}

export async function listAgentMemories(params: ListAgentMemoriesParams): Promise<ListAgentMemoriesResponse> {
    const req: ListAgentMemoriesRequest = create(ListAgentMemoriesRequestSchema, {
        tenantId: params.tenantId ?? '',
        agentId: params.agentId,
        memoryType: params.memoryType,
        scope: params.scope,
        activeOnly: params.activeOnly,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listAgentMemories(req)
}

export type CreateAgentMemoryParams = {
    tenantId?: string
    agentId: string
    memoryType: string
    content: string
    factKey?: string
    confidence?: number
    scope?: string
}

export async function createAgentMemory(params: CreateAgentMemoryParams): Promise<CreateAgentMemoryResponse> {
    const req: CreateAgentMemoryRequest = create(CreateAgentMemoryRequestSchema, {
        tenantId: params.tenantId ?? '',
        agentId: params.agentId,
        memoryType: params.memoryType,
        content: params.content,
        factKey: params.factKey,
        confidence: params.confidence ?? 1.0,
        scope: params.scope ?? 'agent',
    })
    return agentsClient.createAgentMemory(req)
}

export type UpdateAgentMemoryParams = {
    tenantId?: string
    memoryId: string
    content: string
    factKey?: string
    confidence?: number
}

export async function updateAgentMemory(params: UpdateAgentMemoryParams): Promise<UpdateAgentMemoryResponse> {
    const req: UpdateAgentMemoryRequest = create(UpdateAgentMemoryRequestSchema, {
        tenantId: params.tenantId ?? '',
        memoryId: params.memoryId,
        content: params.content,
        factKey: params.factKey,
        confidence: params.confidence ?? 1.0,
    })
    return agentsClient.updateAgentMemory(req)
}

export type DeactivateAgentMemoryParams = {
    tenantId?: string
    memoryId: string
}

export async function deactivateAgentMemory(params: DeactivateAgentMemoryParams): Promise<DeactivateAgentMemoryResponse> {
    const req: DeactivateAgentMemoryRequest = create(DeactivateAgentMemoryRequestSchema, {
        tenantId: params.tenantId ?? '',
        memoryId: params.memoryId,
    })
    return agentsClient.deactivateAgentMemory(req)
}

export type DeleteAgentMemoryParams = {
    tenantId?: string
    memoryId: string
}

export async function deleteAgentMemory(params: DeleteAgentMemoryParams): Promise<DeleteAgentMemoryResponse> {
    const req: DeleteAgentMemoryRequest = create(DeleteAgentMemoryRequestSchema, {
        tenantId: params.tenantId ?? '',
        memoryId: params.memoryId,
    })
    return agentsClient.deleteAgentMemory(req)
}

// ─── Agent Capabilities ──────────────────────────────────────────────

export interface AgentCapabilities {
    web_search_available: boolean
}

export async function getAgentCapabilities(): Promise<AgentCapabilities> {
    const response = await fetch(`${baseUrl}/v1/agents/capabilities`)
    if (!response.ok) {
        return { web_search_available: false }
    }
    return response.json()
}

// ─── Agent Links ────────────────────────────────────────────────────

export type CreateAgentLinkParams = {
    tenantId: string
    sourceAgentId: string
    targetType?: string
    targetId: string
    targetName?: string
    linkType: AgentLinkType
    protocol?: AgentLinkProtocol
}

export async function createAgentLink(params: CreateAgentLinkParams): Promise<CreateAgentLinkResponse> {
    const req: CreateAgentLinkRequest = create(CreateAgentLinkRequestSchema, {
        tenantId: params.tenantId,
        sourceAgentId: params.sourceAgentId,
        targetType: params.targetType ?? 'agent',
        targetId: params.targetId,
        targetName: params.targetName,
        linkType: params.linkType,
        protocol: params.protocol ?? AgentLinkProtocol.INTERNAL,
    })
    return agentsClient.createAgentLink(req)
}

export type ListAgentLinksParams = {
    tenantId: string
    agentId: string
}

export async function listAgentLinks(params: ListAgentLinksParams): Promise<ListAgentLinksResponse> {
    const req: ListAgentLinksRequest = create(ListAgentLinksRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
    })
    return agentsClient.listAgentLinks(req)
}

export async function deleteAgentLink(linkId: string, tenantId: string): Promise<DeleteAgentLinkResponse> {
    const req: DeleteAgentLinkRequest = create(DeleteAgentLinkRequestSchema, {
        tenantId,
        linkId,
    })
    return agentsClient.deleteAgentLink(req)
}

export { SessionStatus, TurnStatus, ApprovalReviewStatus, ApprovalAction, AgentMode, TaskPermissionMode, AgentLifecycleMode, AgentLifecycleStatus, AgentLinkType, AgentLinkProtocol }
export type {
    AgentDefinition,
    AgentSession,
    AgentSessionTurn,
    ApprovalReview,
    PendingToolCall,
    ToolCallDecision,
    AgentEvent,
    AgentMemoryEntry,
    AgentLink,
}
