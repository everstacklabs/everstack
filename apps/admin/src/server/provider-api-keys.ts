import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ProvidersService } from '@everstack/proto/everstack/providers/providers_service_pb'
import type {
    AddProviderAPIKeyRequest,
    AddProviderAPIKeyResponse,
    UpdateAPIKeyWeightRequest,
    UpdateAPIKeyWeightResponse,
    ToggleAPIKeyRequest,
    ToggleAPIKeyResponse,
    DeleteProviderAPIKeyRequest,
    DeleteProviderAPIKeyResponse,
    ListProviderAPIKeysRequest,
    ListProviderAPIKeysResponse,
} from '@everstack/proto/everstack/providers/providers_pb'
import {
    AddProviderAPIKeyRequestSchema,
    UpdateAPIKeyWeightRequestSchema,
    ToggleAPIKeyRequestSchema,
    DeleteProviderAPIKeyRequestSchema,
    ListProviderAPIKeysRequestSchema,
} from '@everstack/proto/everstack/providers/providers_pb'

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
const providersClient = createClientFor(ProvidersService)(transport)

export type AddProviderAPIKeyParams = {
    providerConfigId: string
    keyName: string
    apiKey: string
    weight?: number
}

export type UpdateAPIKeyWeightParams = {
    keyId: string
    weight: number
    providerConfigId?: string // for cache invalidation
}

export type ToggleAPIKeyParams = {
    keyId: string
    isActive: boolean
    providerConfigId?: string // for cache invalidation
}

/**
 * Lists all API keys for a specific provider configuration
 */
export async function listProviderAPIKeys(providerConfigId: string): Promise<ListProviderAPIKeysResponse> {
    const req: ListProviderAPIKeysRequest = create(ListProviderAPIKeysRequestSchema, { providerConfigId })
    return providersClient.listProviderAPIKeys(req)
}

/**
 * Adds a new API key to a provider configuration
 */
export async function addProviderAPIKey(params: AddProviderAPIKeyParams): Promise<AddProviderAPIKeyResponse> {
    const { providerConfigId, keyName, apiKey, weight = 1 } = params
    const req: AddProviderAPIKeyRequest = create(AddProviderAPIKeyRequestSchema, {
        providerConfigId,
        keyName,
        apiKey,
        weight,
    })
    return providersClient.addProviderAPIKey(req)
}

/**
 * Updates the weight of an API key for load balancing
 */
export async function updateAPIKeyWeight(params: UpdateAPIKeyWeightParams): Promise<UpdateAPIKeyWeightResponse> {
    const { keyId, weight } = params
    const req: UpdateAPIKeyWeightRequest = create(UpdateAPIKeyWeightRequestSchema, {
        keyId,
        weight,
    })
    return providersClient.updateAPIKeyWeight(req)
}

/**
 * Toggles (activates/deactivates) an API key
 */
export async function toggleAPIKey(params: ToggleAPIKeyParams): Promise<ToggleAPIKeyResponse> {
    const { keyId, isActive } = params
    const req: ToggleAPIKeyRequest = create(ToggleAPIKeyRequestSchema, {
        keyId,
        isActive,
    })
    return providersClient.toggleAPIKey(req)
}

/**
 * Deletes an API key
 */
export async function deleteProviderAPIKey(keyId: string): Promise<DeleteProviderAPIKeyResponse> {
    const req: DeleteProviderAPIKeyRequest = create(DeleteProviderAPIKeyRequestSchema, { keyId })
    return providersClient.deleteProviderAPIKey(req)
}

