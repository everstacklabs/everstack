import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { GatewayService } from '@everstack/proto/everstack/gateway/v1/gateway_service_pb'
import type {
    ListModelsResponse,
    ProviderModels,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'
import {
    ListModelsRequestSchema,
} from '@everstack/proto/everstack/gateway/v1/gateway_pb'

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

const gatewayClient = createClientFor(GatewayService)(transport)

export type { ProviderModels }

/**
 * List all available models grouped by provider from the gateway.
 */
export async function listGatewayModels(): Promise<ProviderModels[]> {
    const req = create(ListModelsRequestSchema, {})
    const resp: ListModelsResponse = await gatewayClient.listModels(req)
    return resp.providers ?? []
}
