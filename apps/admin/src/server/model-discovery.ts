import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ModelDiscoveryService } from '@everstack/proto/everstack/providers/model_discovery_pb'
import type {
    SearchModelsRequest,
    SearchModelsResponse,
    AddCustomModelRequest,
    AddCustomModelResponse,
    ListCustomModelsRequest,
    ListCustomModelsResponse,
    DeleteCustomModelRequest,
    DeleteCustomModelResponse,
    UpdateCustomModelRequest,
    UpdateCustomModelResponse,
    GetCustomModelRequest,
    GetCustomModelResponse,
} from '@everstack/proto/everstack/providers/model_discovery_pb'
import {
    SearchModelsRequestSchema,
    AddCustomModelRequestSchema,
    ListCustomModelsRequestSchema,
    DeleteCustomModelRequestSchema,
    UpdateCustomModelRequestSchema,
    GetCustomModelRequestSchema,
} from '@everstack/proto/everstack/providers/model_discovery_pb'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// No API key interceptor needed - same-origin requests are allowed
const transport = createServerTransport(undefined, {
    baseUrl: `${baseUrl}${connectBase}`,
    interceptors: [],
})
const modelDiscoveryClient = createClientFor(ModelDiscoveryService)(transport)

export type SearchModelsParams = {
    providerName: string
    query?: string
    limit?: number
    useProviderApiKey?: boolean
    customBaseUrl?: string
}

/**
 * Search for available models from a meta-provider
 */
export async function searchModels(params: SearchModelsParams): Promise<SearchModelsResponse> {
    const { providerName, query, limit, useProviderApiKey, customBaseUrl } = params
    const req: SearchModelsRequest = create(SearchModelsRequestSchema, {
        providerName,
        query: query ?? '',
        limit: limit ?? 50,
        useProviderApiKey: useProviderApiKey ?? true,
        customBaseUrl: customBaseUrl ?? '',
    })
    return modelDiscoveryClient.searchModels(req)
}

export type AddCustomModelParams = {
    providerName: string
    modelName: string
    displayName: string
    modelMetadata?: Record<string, string>
    source?: 'manual' | 'discovered'
}

/**
 * Add a custom model to the database
 */
export async function addCustomModel(params: AddCustomModelParams): Promise<AddCustomModelResponse> {
    const { providerName, modelName, displayName, modelMetadata, source } = params
    const req: AddCustomModelRequest = create(AddCustomModelRequestSchema, {
        providerName,
        modelName,
        displayName,
        modelMetadata: modelMetadata ?? {},
        source: source ?? 'manual',
    })
    return modelDiscoveryClient.addCustomModel(req)
}

export type ListCustomModelsParams = {
    providerName?: string
    activeOnly?: boolean
}

/**
 * List custom models, optionally filtered by provider
 */
export async function listCustomModels(params: ListCustomModelsParams = {}): Promise<ListCustomModelsResponse> {
    const { providerName, activeOnly } = params
    const req: ListCustomModelsRequest = create(ListCustomModelsRequestSchema, {
        providerName: providerName ?? '',
        activeOnly: activeOnly ?? true,
    })
    return modelDiscoveryClient.listCustomModels(req)
}

/**
 * Delete a custom model
 */
export async function deleteCustomModel(id: string): Promise<DeleteCustomModelResponse> {
    const req: DeleteCustomModelRequest = create(DeleteCustomModelRequestSchema, { id })
    return modelDiscoveryClient.deleteCustomModel(req)
}

export type UpdateCustomModelParams = {
    id: string
    displayName?: string
    modelMetadata?: Record<string, string>
    isActive?: boolean
}

/**
 * Update a custom model's metadata
 */
export async function updateCustomModel(params: UpdateCustomModelParams): Promise<UpdateCustomModelResponse> {
    const { id, displayName, modelMetadata, isActive } = params
    const req: UpdateCustomModelRequest = create(UpdateCustomModelRequestSchema, {
        id,
        displayName: displayName ?? '',
        modelMetadata: modelMetadata ?? {},
        isActive: isActive ?? true,
    })
    return modelDiscoveryClient.updateCustomModel(req)
}

/**
 * Get a specific custom model by ID
 */
export async function getCustomModel(id: string): Promise<GetCustomModelResponse> {
    const req: GetCustomModelRequest = create(GetCustomModelRequestSchema, { id })
    return modelDiscoveryClient.getCustomModel(req)
}
