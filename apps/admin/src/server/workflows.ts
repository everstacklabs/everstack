import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { WorkflowsService } from '@everstack/proto/everstack/workflows/v1/workflows_service_pb'
import type {
    CreateWorkflowRequest,
    CreateWorkflowResponse,
    GetWorkflowRequest,
    GetWorkflowResponse,
    ListWorkflowsRequest,
    ListWorkflowsResponse,
    UpdateWorkflowRequest,
    UpdateWorkflowResponse,
    DeleteWorkflowRequest,
    DeleteWorkflowResponse,
    GetWorkflowVersionHistoryResponse,
    GetWorkflowAtVersionResponse,
    SaveWorkflowDraftRequest,
    SaveWorkflowDraftResponse,
    PublishWorkflowRequest,
    PublishWorkflowResponse,
    UnpublishWorkflowRequest,
    UnpublishWorkflowResponse,
    Workflow,
    WorkflowNode,
    WorkflowEdge,
    WorkflowViewport,
    WorkflowVersionEntry,
    WorkflowChangeDetail,
} from '@everstack/proto/everstack/workflows/v1/workflows_pb'
import {
    CreateWorkflowRequestSchema,
    GetWorkflowRequestSchema,
    ListWorkflowsRequestSchema,
    UpdateWorkflowRequestSchema,
    DeleteWorkflowRequestSchema,
    GetWorkflowVersionHistoryRequestSchema,
    GetWorkflowAtVersionRequestSchema,
    SaveWorkflowDraftRequestSchema,
    PublishWorkflowRequestSchema,
    UnpublishWorkflowRequestSchema,
    WorkflowNodeSchema,
    WorkflowEdgeSchema,
    WorkflowViewportSchema,
    WorkflowNodePositionSchema,
} from '@everstack/proto/everstack/workflows/v1/workflows_pb'
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
const workflowsClient = createClientFor(WorkflowsService)(transport)

export type CreateWorkflowParams = {
    tenantId: string
    name: string
    description?: string
    nodes?: Array<{
        id: string
        type: string
        label: string
        position: { x: number; y: number }
        config?: JsonObject
    }>
    edges?: Array<{
        id: string
        source: string
        target: string
        sourceHandle?: string
        targetHandle?: string
    }>
    viewport?: { x: number; y: number; zoom: number }
}

export type UpdateWorkflowParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    nodes?: Array<{
        id: string
        type: string
        label: string
        position: { x: number; y: number }
        config?: JsonObject
    }>
    edges?: Array<{
        id: string
        source: string
        target: string
        sourceHandle?: string
        targetHandle?: string
    }>
    viewport?: { x: number; y: number; zoom: number }
    enabled?: boolean
}

export type ListWorkflowsParams = {
    tenantId?: string
    enabled?: boolean
    limit?: number
    offset?: number
}

function buildProtoNodes(nodes: CreateWorkflowParams['nodes']): any[] {
    if (!nodes) return []
    return nodes.map(n => create(WorkflowNodeSchema, {
        id: n.id,
        type: n.type,
        label: n.label,
        position: create(WorkflowNodePositionSchema, { x: n.position.x, y: n.position.y }),
        config: n.config,
    }))
}

function buildProtoEdges(edges: CreateWorkflowParams['edges']): any[] {
    if (!edges) return []
    return edges.map(e => create(WorkflowEdgeSchema, {
        id: e.id,
        source: e.source,
        target: e.target,
        sourceHandle: e.sourceHandle ?? '',
        targetHandle: e.targetHandle ?? '',
    }))
}

export async function createWorkflow(params: CreateWorkflowParams): Promise<CreateWorkflowResponse> {
    const req: CreateWorkflowRequest = create(CreateWorkflowRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        description: params.description,
        nodes: buildProtoNodes(params.nodes),
        edges: buildProtoEdges(params.edges),
    })

    if (params.viewport) {
        req.viewport = create(WorkflowViewportSchema, {
            x: params.viewport.x,
            y: params.viewport.y,
            zoom: params.viewport.zoom,
        })
    }

    return workflowsClient.createWorkflow(req)
}

export async function getWorkflow(id: string, tenantId: string): Promise<GetWorkflowResponse> {
    const req: GetWorkflowRequest = create(GetWorkflowRequestSchema, { tenantId, id })
    return workflowsClient.getWorkflow(req)
}

export async function listWorkflows(params: ListWorkflowsParams = {}): Promise<ListWorkflowsResponse> {
    const { tenantId, enabled, limit, offset } = params
    const req: ListWorkflowsRequest = create(ListWorkflowsRequestSchema, {
        tenantId: tenantId ?? '',
        enabled,
        limit,
        offset,
    })
    return workflowsClient.listWorkflows(req)
}

export async function updateWorkflow(params: UpdateWorkflowParams): Promise<UpdateWorkflowResponse> {
    const req: UpdateWorkflowRequest = create(UpdateWorkflowRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        nodes: buildProtoNodes(params.nodes),
        edges: buildProtoEdges(params.edges),
        enabled: params.enabled,
    })

    if (params.viewport) {
        req.viewport = create(WorkflowViewportSchema, {
            x: params.viewport.x,
            y: params.viewport.y,
            zoom: params.viewport.zoom,
        })
    }

    return workflowsClient.updateWorkflow(req)
}

export async function deleteWorkflow(id: string, tenantId: string): Promise<DeleteWorkflowResponse> {
    const req: DeleteWorkflowRequest = create(DeleteWorkflowRequestSchema, { tenantId, id })
    return workflowsClient.deleteWorkflow(req)
}

export async function getWorkflowVersionHistory(id: string, tenantId: string): Promise<GetWorkflowVersionHistoryResponse> {
    const req = create(GetWorkflowVersionHistoryRequestSchema, { tenantId, id })
    return workflowsClient.getWorkflowVersionHistory(req)
}

export async function getWorkflowAtVersion(id: string, tenantId: string, version: number): Promise<GetWorkflowAtVersionResponse> {
    const req = create(GetWorkflowAtVersionRequestSchema, { tenantId, id, version })
    return workflowsClient.getWorkflowAtVersion(req)
}

export type SaveWorkflowDraftParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    nodes?: Array<{
        id: string
        type: string
        label: string
        position: { x: number; y: number }
        config?: JsonObject
    }>
    edges?: Array<{
        id: string
        source: string
        target: string
        sourceHandle?: string
        targetHandle?: string
    }>
    viewport?: { x: number; y: number; zoom: number }
}

export async function saveWorkflowDraft(params: SaveWorkflowDraftParams): Promise<SaveWorkflowDraftResponse> {
    const req: SaveWorkflowDraftRequest = create(SaveWorkflowDraftRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        description: params.description,
        nodes: buildProtoNodes(params.nodes),
        edges: buildProtoEdges(params.edges),
    })

    if (params.viewport) {
        req.viewport = create(WorkflowViewportSchema, {
            x: params.viewport.x,
            y: params.viewport.y,
            zoom: params.viewport.zoom,
        })
    }

    return workflowsClient.saveWorkflowDraft(req)
}

export async function publishWorkflow(id: string, tenantId: string): Promise<PublishWorkflowResponse> {
    const req: PublishWorkflowRequest = create(PublishWorkflowRequestSchema, { tenantId, id })
    return workflowsClient.publishWorkflow(req)
}

export async function unpublishWorkflow(id: string, tenantId: string): Promise<UnpublishWorkflowResponse> {
    const req: UnpublishWorkflowRequest = create(UnpublishWorkflowRequestSchema, { tenantId, id })
    return workflowsClient.unpublishWorkflow(req)
}

export type {
    Workflow, WorkflowNode, WorkflowEdge, WorkflowViewport,
    WorkflowVersionEntry, WorkflowChangeDetail, GetWorkflowAtVersionResponse,
    SaveWorkflowDraftResponse, PublishWorkflowResponse, UnpublishWorkflowResponse,
}
