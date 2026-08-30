import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { FunctionsService } from '@everstack/proto/everstack/functions/v1/functions_service_pb'
import type {
    CreateFunctionRequest,
    CreateFunctionResponse,
    GetFunctionRequest,
    GetFunctionResponse,
    ListFunctionsRequest,
    ListFunctionsResponse,
    UpdateFunctionRequest,
    UpdateFunctionResponse,
    DeleteFunctionRequest,
    DeleteFunctionResponse,
    Function,
    WebhookConfig,
    ProxyConfig,
    IsolatedConfig,
    GetIsolationStatusRequest,
    GetIsolationStatusResponse,
} from '@everstack/proto/everstack/functions/v1/functions_pb'
import {
    CreateFunctionRequestSchema,
    GetFunctionRequestSchema,
    ListFunctionsRequestSchema,
    UpdateFunctionRequestSchema,
    DeleteFunctionRequestSchema,
    GetIsolationStatusRequestSchema,
    ExecutionMode,
    WebhookConfigSchema,
    ProxyConfigSchema,
    IsolatedConfigSchema,
} from '@everstack/proto/everstack/functions/v1/functions_pb'
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
const functionsClient = createClientFor(FunctionsService)(transport)

export type CreateFunctionParams = {
    tenantId: string
    name: string
    description?: string
    mode: ExecutionMode
    parameters?: JsonObject
    webhook?: {
        url: string
        method: string
        headers?: { [key: string]: string }
        timeoutMs?: number
    }
    proxy?: {
        baseUrl: string
        path: string
        method: string
        queryMapping?: { [key: string]: string }
        headerMapping?: { [key: string]: string }
        bodyMapping?: { [key: string]: string }
        responseMapping?: { [key: string]: string }
    }
    isolated?: {
        runtime: string
        code: string
        packages?: string[]
        dockerHost?: string
    }
    timeoutMs?: number
    memoryMb?: number
    maxRetries?: number
}

export type UpdateFunctionParams = {
    tenantId: string
    id: string
    name?: string
    description?: string
    mode?: ExecutionMode
    parameters?: JsonObject
    webhook?: {
        url: string
        method: string
        headers?: { [key: string]: string }
        timeoutMs?: number
    }
    proxy?: {
        baseUrl: string
        path: string
        method: string
        queryMapping?: { [key: string]: string }
        headerMapping?: { [key: string]: string }
        bodyMapping?: { [key: string]: string }
        responseMapping?: { [key: string]: string }
    }
    isolated?: {
        runtime: string
        code: string
        packages?: string[]
        dockerHost?: string
    }
    timeoutMs?: number
    memoryMb?: number
    maxRetries?: number
    enabled?: boolean
}

export type ListFunctionsParams = {
    tenantId?: string
    mode?: ExecutionMode
    enabled?: boolean
    limit?: number
    offset?: number
}

export async function createFunction(params: CreateFunctionParams): Promise<CreateFunctionResponse> {
    const { tenantId, name, description, mode, parameters, webhook, proxy, isolated, timeoutMs, memoryMb, maxRetries } = params

    const req: CreateFunctionRequest = create(CreateFunctionRequestSchema, {
        tenantId,
        name,
        description,
        mode,
        parameters,
        timeoutMs,
        memoryMb,
        maxRetries,
    })

    // Add webhook config if provided
    if (webhook) {
        req.webhook = create(WebhookConfigSchema, {
            url: webhook.url,
            method: webhook.method,
            headers: webhook.headers ?? {},
            timeoutMs: webhook.timeoutMs ?? 30000,
        })
    }

    // Add proxy config if provided
    if (proxy) {
        req.proxy = create(ProxyConfigSchema, {
            baseUrl: proxy.baseUrl,
            path: proxy.path,
            method: proxy.method,
            queryMapping: proxy.queryMapping ?? {},
            headerMapping: proxy.headerMapping ?? {},
            bodyMapping: proxy.bodyMapping ?? {},
            responseMapping: proxy.responseMapping ?? {},
        })
    }

    // Add isolated config if provided
    if (isolated) {
        req.isolated = create(IsolatedConfigSchema, {
            runtime: isolated.runtime,
            code: isolated.code,
            packages: isolated.packages ?? [],
            dockerHost: isolated.dockerHost ?? '',
        })
    }

    return functionsClient.createFunction(req)
}

export async function getFunction(id: string, tenantId: string): Promise<GetFunctionResponse> {
    const req: GetFunctionRequest = create(GetFunctionRequestSchema, { tenantId, id })
    return functionsClient.getFunction(req)
}

export async function listFunctions(params: ListFunctionsParams = {}): Promise<ListFunctionsResponse> {
    const { tenantId, mode, enabled, limit, offset } = params
    const req: ListFunctionsRequest = create(ListFunctionsRequestSchema, {
        tenantId: tenantId ?? '',
        mode,
        enabled,
        limit,
        offset,
    })
    return functionsClient.listFunctions(req)
}

export async function updateFunction(params: UpdateFunctionParams): Promise<UpdateFunctionResponse> {
    const { tenantId, id, name, description, mode, parameters, webhook, proxy, isolated, timeoutMs, memoryMb, maxRetries, enabled } = params

    const req: UpdateFunctionRequest = create(UpdateFunctionRequestSchema, {
        tenantId,
        id,
        name,
        description,
        mode,
        parameters,
        timeoutMs,
        memoryMb,
        maxRetries,
        enabled,
    })

    // Add webhook config if provided
    if (webhook) {
        req.webhook = create(WebhookConfigSchema, {
            url: webhook.url,
            method: webhook.method,
            headers: webhook.headers ?? {},
            timeoutMs: webhook.timeoutMs ?? 30000,
        })
    }

    // Add proxy config if provided
    if (proxy) {
        req.proxy = create(ProxyConfigSchema, {
            baseUrl: proxy.baseUrl,
            path: proxy.path,
            method: proxy.method,
            queryMapping: proxy.queryMapping ?? {},
            headerMapping: proxy.headerMapping ?? {},
            bodyMapping: proxy.bodyMapping ?? {},
            responseMapping: proxy.responseMapping ?? {},
        })
    }

    // Add isolated config if provided
    if (isolated) {
        req.isolated = create(IsolatedConfigSchema, {
            runtime: isolated.runtime,
            code: isolated.code,
            packages: isolated.packages ?? [],
            dockerHost: isolated.dockerHost ?? '',
        })
    }

    return functionsClient.updateFunction(req)
}

export async function deleteFunction(id: string, tenantId: string): Promise<DeleteFunctionResponse> {
    const req: DeleteFunctionRequest = create(DeleteFunctionRequestSchema, { tenantId, id })
    return functionsClient.deleteFunction(req)
}

export async function getIsolationStatus(): Promise<GetIsolationStatusResponse> {
    const req: GetIsolationStatusRequest = create(GetIsolationStatusRequestSchema, {})
    return functionsClient.getIsolationStatus(req)
}

export { ExecutionMode }
export type { Function, WebhookConfig, ProxyConfig, IsolatedConfig, GetIsolationStatusResponse }
