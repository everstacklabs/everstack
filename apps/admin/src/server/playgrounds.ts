/**
 * PlaygroundService client — persists playground documents (tasks, dataset,
 * scorers, grid, view flags) as an opaque JSON `config` blob so the shape can
 * evolve without migrations. Mirrors server/datasets.ts transport setup.
 */

import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { PlaygroundService } from '@everstack/proto/everstack/datasets/v1/datasets_service_pb'
import {
    CreatePlaygroundRequestSchema,
    GetPlaygroundRequestSchema,
    ListPlaygroundsRequestSchema,
    UpdatePlaygroundRequestSchema,
    DeletePlaygroundRequestSchema,
    type Playground,
} from '@everstack/proto/everstack/datasets/v1/datasets_pb'

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
const playgroundClient = createClientFor(PlaygroundService)(transport)

/** The persisted config blob: the serialized playground store state. */
export type PlaygroundConfig = Record<string, unknown>

export async function createPlayground(params: {
    tenantId: string
    name: string
    config: PlaygroundConfig
}): Promise<Playground | undefined> {
    const req = create(CreatePlaygroundRequestSchema, {
        tenantId: params.tenantId,
        name: params.name,
        config: params.config as never,
    })
    const resp = await playgroundClient.createPlayground(req)
    return resp.playground
}

export async function getPlayground(id: string, tenantId: string): Promise<Playground | undefined> {
    const req = create(GetPlaygroundRequestSchema, { tenantId, id })
    const resp = await playgroundClient.getPlayground(req)
    return resp.playground
}

export async function listPlaygrounds(params: { tenantId: string; limit?: number }): Promise<Playground[]> {
    const req = create(ListPlaygroundsRequestSchema, {
        tenantId: params.tenantId,
        limit: params.limit,
    })
    const resp = await playgroundClient.listPlaygrounds(req)
    return resp.playgrounds ?? []
}

export async function updatePlayground(params: {
    tenantId: string
    id: string
    name?: string
    config?: PlaygroundConfig
}): Promise<Playground | undefined> {
    const req = create(UpdatePlaygroundRequestSchema, {
        tenantId: params.tenantId,
        id: params.id,
        name: params.name,
        config: params.config as never,
    })
    const resp = await playgroundClient.updatePlayground(req)
    return resp.playground
}

export async function deletePlayground(id: string, tenantId: string): Promise<boolean> {
    const req = create(DeletePlaygroundRequestSchema, { tenantId, id })
    const resp = await playgroundClient.deletePlayground(req)
    return resp.success
}

export type { Playground }
