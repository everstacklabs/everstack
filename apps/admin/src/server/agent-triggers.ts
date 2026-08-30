import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { AgentsService } from '@everstack/proto/everstack/agents/v1/agents_service_pb'
import type {
    CreateAgentTriggerRequest,
    CreateAgentTriggerResponse,
    ListAgentTriggersRequest,
    ListAgentTriggersResponse,
    GetAgentTriggerRequest,
    GetAgentTriggerResponse,
    UpdateAgentTriggerRequest,
    UpdateAgentTriggerResponse,
    DeleteAgentTriggerRequest,
    DeleteAgentTriggerResponse,
    TestAgentTriggerRequest,
    TestAgentTriggerResponse,
    ListAgentTriggerExecutionsRequest,
    ListAgentTriggerExecutionsResponse,
    AgentTrigger,
    AgentTriggerExecution,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import {
    CreateAgentTriggerRequestSchema,
    ListAgentTriggersRequestSchema,
    GetAgentTriggerRequestSchema,
    UpdateAgentTriggerRequestSchema,
    DeleteAgentTriggerRequestSchema,
    TestAgentTriggerRequestSchema,
    ListAgentTriggerExecutionsRequestSchema,
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

// ─── Trigger CRUD ────────────────────────────────────────────────────

export type CreateTriggerParams = {
    tenantId: string
    agentId: string
    name: string
    triggerType: string
    cronExpression?: string
    cronTimezone?: string
    eventSourceAgentId?: string
    eventType?: string
    eventFilter?: JsonObject
    inputTemplate?: string
    maxRetries?: number
    retryDelaySeconds?: number
    timeoutSeconds?: number
    maxConcurrent?: number
}

export async function createAgentTrigger(params: CreateTriggerParams): Promise<CreateAgentTriggerResponse> {
    const req: CreateAgentTriggerRequest = create(CreateAgentTriggerRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
        name: params.name,
        triggerType: params.triggerType,
        cronExpression: params.cronExpression,
        cronTimezone: params.cronTimezone,
        eventSourceAgentId: params.eventSourceAgentId,
        eventType: params.eventType,
        eventFilter: params.eventFilter,
        inputTemplate: params.inputTemplate,
        maxRetries: params.maxRetries,
        retryDelaySeconds: params.retryDelaySeconds,
        timeoutSeconds: params.timeoutSeconds,
        maxConcurrent: params.maxConcurrent,
    })
    return agentsClient.createAgentTrigger(req)
}

export async function listAgentTriggers(agentId: string, tenantId: string): Promise<ListAgentTriggersResponse> {
    const req: ListAgentTriggersRequest = create(ListAgentTriggersRequestSchema, {
        tenantId,
        agentId,
    })
    return agentsClient.listAgentTriggers(req)
}

export async function getAgentTrigger(id: string, tenantId: string): Promise<GetAgentTriggerResponse> {
    const req: GetAgentTriggerRequest = create(GetAgentTriggerRequestSchema, {
        tenantId,
        id,
    })
    return agentsClient.getAgentTrigger(req)
}

export type UpdateTriggerParams = {
    tenantId: string
    id: string
    name?: string
    enabled?: boolean
    cronExpression?: string
    cronTimezone?: string
    eventSourceAgentId?: string
    eventType?: string
    eventFilter?: JsonObject
    inputTemplate?: string
    maxRetries?: number
    retryDelaySeconds?: number
    timeoutSeconds?: number
    maxConcurrent?: number
}

export async function updateAgentTrigger(params: UpdateTriggerParams): Promise<UpdateAgentTriggerResponse> {
    const req: UpdateAgentTriggerRequest = create(UpdateAgentTriggerRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        enabled: params.enabled,
        cronExpression: params.cronExpression,
        cronTimezone: params.cronTimezone,
        eventSourceAgentId: params.eventSourceAgentId,
        eventType: params.eventType,
        eventFilter: params.eventFilter,
        inputTemplate: params.inputTemplate,
        maxRetries: params.maxRetries,
        retryDelaySeconds: params.retryDelaySeconds,
        timeoutSeconds: params.timeoutSeconds,
        maxConcurrent: params.maxConcurrent,
    })
    return agentsClient.updateAgentTrigger(req)
}

export async function deleteAgentTrigger(id: string, tenantId: string): Promise<DeleteAgentTriggerResponse> {
    const req: DeleteAgentTriggerRequest = create(DeleteAgentTriggerRequestSchema, {
        tenantId,
        id,
    })
    return agentsClient.deleteAgentTrigger(req)
}

export async function testAgentTrigger(id: string, tenantId: string, testPayload?: JsonObject): Promise<TestAgentTriggerResponse> {
    const req: TestAgentTriggerRequest = create(TestAgentTriggerRequestSchema, {
        tenantId,
        id,
        testPayload,
    })
    return agentsClient.testAgentTrigger(req)
}

export type ListTriggerExecutionsParams = {
    tenantId: string
    triggerId: string
    limit?: number
    offset?: number
}

export async function listAgentTriggerExecutions(params: ListTriggerExecutionsParams): Promise<ListAgentTriggerExecutionsResponse> {
    const req: ListAgentTriggerExecutionsRequest = create(ListAgentTriggerExecutionsRequestSchema, {
        tenantId: params.tenantId,
        triggerId: params.triggerId,
        limit: params.limit,
        offset: params.offset,
    })
    return agentsClient.listAgentTriggerExecutions(req)
}

export type { AgentTrigger, AgentTriggerExecution }
