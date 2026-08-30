import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { WorkflowsService } from '@everstack/proto/everstack/workflows/v1/workflows_service_pb'
import {
    ListWorkflowExecutionsRequestSchema,
    GetWorkflowExecutionRequestSchema,
} from '@everstack/proto/everstack/workflows/v1/workflows_pb'
import type {
    ListWorkflowExecutionsResponse,
    GetWorkflowExecutionResponse,
    WorkflowExecution,
} from '@everstack/proto/everstack/workflows/v1/workflows_pb'

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

export async function listWorkflowExecutions(
    workflowId: string,
    tenantId: string,
    opts?: { pageSize?: number; offset?: number; statusFilter?: string },
): Promise<ListWorkflowExecutionsResponse> {
    const req = create(ListWorkflowExecutionsRequestSchema, {
        tenantId,
        workflowId,
        pageSize: opts?.pageSize ?? 20,
        offset: opts?.offset ?? 0,
        statusFilter: opts?.statusFilter ?? '',
    })
    return workflowsClient.listWorkflowExecutions(req)
}

export async function getWorkflowExecution(
    executionId: string,
    tenantId: string,
): Promise<GetWorkflowExecutionResponse> {
    const req = create(GetWorkflowExecutionRequestSchema, {
        tenantId,
        executionId,
    })
    return workflowsClient.getWorkflowExecution(req)
}

export type { WorkflowExecution, ListWorkflowExecutionsResponse, GetWorkflowExecutionResponse }
