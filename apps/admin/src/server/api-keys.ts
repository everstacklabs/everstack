import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { ApiKeyService } from '@everstack/proto/everstack/api_key/v1/api_key_service_pb'
import type {
    CreateApiKeyRequest,
    CreateApiKeyResponse,
    DeleteApiKeyRequest,
    DeleteApiKeyResponse,
    GetApiKeyRequest,
    GetApiKeyResponse,
    ListApiKeysRequest,
    ListApiKeysResponse,
    ApiKey,
} from '@everstack/proto/everstack/api_key/v1/api_key_pb'
import {
    CreateApiKeyRequestSchema,
    DeleteApiKeyRequestSchema,
    GetApiKeyRequestSchema,
    ListApiKeysRequestSchema,
    ApiKeyType,
} from '@everstack/proto/everstack/api_key/v1/api_key_pb'

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
const apiKeysClient = createClientFor(ApiKeyService)(transport)

export type CreateApiKeyParams = {
    name: string
    userId?: string
    orgId?: string
    type: ApiKeyType
}

export type ListApiKeysParams = {
    userId?: string
    orgId?: string
    type?: ApiKeyType
}

export async function createApiKey(params: CreateApiKeyParams): Promise<CreateApiKeyResponse> {
    const { name, userId, orgId, type } = params
    const req: CreateApiKeyRequest = create(CreateApiKeyRequestSchema, {
        name,
        userId: userId ?? '',
        orgId: orgId ?? '',
        type,
    })
    return apiKeysClient.createApiKey(req)
}

export async function listApiKeys(params: ListApiKeysParams = {}): Promise<ListApiKeysResponse> {
    const { userId, orgId, type } = params
    const req: ListApiKeysRequest = create(ListApiKeysRequestSchema, {
        userId: userId ?? '',
        orgId: orgId ?? '',
        type,
    })
    return apiKeysClient.listApiKeys(req)
}

export async function getApiKey(id: string): Promise<GetApiKeyResponse> {
    const req: GetApiKeyRequest = create(GetApiKeyRequestSchema, { id })
    return apiKeysClient.getApiKey(req)
}

export async function deleteApiKey(id: string): Promise<DeleteApiKeyResponse> {
    const req: DeleteApiKeyRequest = create(DeleteApiKeyRequestSchema, { id })
    return apiKeysClient.deleteApiKey(req)
}

export { ApiKeyType }
export type { ApiKey }

