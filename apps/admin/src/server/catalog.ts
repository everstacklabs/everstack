import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { CatalogService } from '@everstack/proto/everstack/catalog/v1/catalog_service_pb'
import type {
    CatalogStatus,
    Changelog,
    NewModels,
    SyncStatus,
} from '@everstack/proto/everstack/catalog/v1/catalog_pb'
import {
    GetCatalogStatusRequestSchema,
    GetChangelogRequestSchema,
    GetNewModelsRequestSchema,
    TriggerSyncRequestSchema,
} from '@everstack/proto/everstack/catalog/v1/catalog_pb'

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
const catalogClient = createClientFor(CatalogService)(transport)

/**
 * Fetches the current catalog status
 */
export async function getCatalogStatus(): Promise<CatalogStatus> {
    const request = create(GetCatalogStatusRequestSchema, {})
    const response = await catalogClient.getCatalogStatus(request)
    return response
}

/**
 * Fetches the changelog for catalog updates
 */
export async function getChangelog(fromVersion?: string): Promise<Changelog> {
    const request = create(GetChangelogRequestSchema, {
        fromVersion: fromVersion || '',
    })
    const response = await catalogClient.getChangelog(request)
    return response
}

/**
 * Fetches new models since last view
 */
export async function getNewModels(provider?: string): Promise<NewModels> {
    const request = create(GetNewModelsRequestSchema, {
        provider: provider || '',
    })
    const response = await catalogClient.getNewModels(request)
    return response
}

/**
 * Triggers manual catalog sync
 */
export async function triggerSync(force: boolean = false): Promise<SyncStatus> {
    const request = create(TriggerSyncRequestSchema, {
        force,
    })
    const response = await catalogClient.triggerSync(request)
    return response
}

// Re-export types for convenience
export type {
    CatalogStatus,
    Changelog,
    NewModels,
    SyncStatus,
}
