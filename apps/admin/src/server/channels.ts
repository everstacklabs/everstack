import { getApiBaseUrl } from '@/lib/api-url'
import { createServerTransport } from '@/server'
import type { JsonObject } from '@everstack/client'
import { create, createClientFor } from '@everstack/client'
import type {
    ChannelConfig,
    CreateChannelResponse,
    DeleteChannelResponse,
    GetChannelResponse,
    ListChannelSessionsResponse,
    ListChannelStatusesResponse,
    ListChannelsResponse,
    ListPlatformChannelsResponse,
    PlatformChannelInfo,
    TestChannelResponse,
    UpdateChannelResponse
} from '@everstack/proto/everstack/channels/v1/channels_pb'
import {
    ChannelStatus,
    CreateChannelRequestSchema,
    DeleteChannelRequestSchema,
    GetChannelRequestSchema,
    ListChannelSessionsRequestSchema,
    ListChannelStatusesRequestSchema,
    ListChannelsRequestSchema,
    ListPlatformChannelsRequestSchema,
    Platform,
    SessionMode,
    TestChannelRequestSchema,
    UpdateChannelRequestSchema,
} from '@everstack/proto/everstack/channels/v1/channels_pb'
import { ChannelsService } from '@everstack/proto/everstack/channels/v1/channels_service_pb'

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
const channelsClient = createClientFor(ChannelsService)(transport)

// ─── Types ──────────────────────────────────────────────────────────

export type CreateChannelParams = {
    tenantId: string
    agentId: string
    platform: Platform
    name: string
    sessionMode?: SessionMode
    credentials?: JsonObject
    platformConfig?: JsonObject
    maxMessagesPerMinute?: number
    maxSessionsPerUser?: number
    responseFormat?: string
    maxResponseLength?: number
    maxTokensPerDay?: bigint
    idleSessionTtlSeconds?: number
    coalesceWindowMs?: number
    instanceAffinity?: string
}

export type UpdateChannelParams = {
    id: string
    tenantId: string
    name?: string
    agentId?: string
    enabled?: boolean
    sessionMode?: SessionMode
    credentials?: JsonObject
    platformConfig?: JsonObject
    maxMessagesPerMinute?: number
    maxSessionsPerUser?: number
    responseFormat?: string
    maxResponseLength?: number
    maxTokensPerDay?: bigint
    idleSessionTtlSeconds?: number
    coalesceWindowMs?: number
    instanceAffinity?: string
}

// ─── API Functions ──────────────────────────────────────────────────

export async function createChannel(params: CreateChannelParams): Promise<CreateChannelResponse> {
    const req = create(CreateChannelRequestSchema, {
        tenantId: params.tenantId,
        agentId: params.agentId,
        platform: params.platform,
        name: params.name,
        sessionMode: params.sessionMode ?? SessionMode.THREAD,
        credentials: params.credentials,
        platformConfig: params.platformConfig,
        maxMessagesPerMinute: params.maxMessagesPerMinute,
        maxSessionsPerUser: params.maxSessionsPerUser,
        responseFormat: params.responseFormat,
        maxResponseLength: params.maxResponseLength,
        maxTokensPerDay: params.maxTokensPerDay,
        idleSessionTtlSeconds: params.idleSessionTtlSeconds,
        coalesceWindowMs: params.coalesceWindowMs,
        instanceAffinity: params.instanceAffinity,
    })
    return channelsClient.createChannel(req)
}

export async function getChannel(params: { id: string; tenantId: string }): Promise<GetChannelResponse> {
    return channelsClient.getChannel(create(GetChannelRequestSchema, params))
}

export async function updateChannel(params: UpdateChannelParams): Promise<UpdateChannelResponse> {
    return channelsClient.updateChannel(create(UpdateChannelRequestSchema, params))
}

export async function deleteChannel(params: { id: string; tenantId: string }): Promise<DeleteChannelResponse> {
    return channelsClient.deleteChannel(create(DeleteChannelRequestSchema, params))
}

export async function listChannels(params: {
    tenantId: string
    platform?: Platform
    agentId?: string
    enabled?: boolean
    limit?: number
    offset?: number
}): Promise<ListChannelsResponse> {
    return channelsClient.listChannels(create(ListChannelsRequestSchema, params))
}

export async function testChannel(params: { id: string; tenantId: string }): Promise<TestChannelResponse> {
    return channelsClient.testChannel(create(TestChannelRequestSchema, params))
}

export async function listChannelStatuses(params: { tenantId: string }): Promise<ListChannelStatusesResponse> {
    return channelsClient.listChannelStatuses(create(ListChannelStatusesRequestSchema, params))
}

export async function listChannelSessions(params: {
    channelId: string
    tenantId: string
    limit?: number
    offset?: number
}): Promise<ListChannelSessionsResponse> {
    return channelsClient.listChannelSessions(create(ListChannelSessionsRequestSchema, params))
}

export async function listPlatformChannels(params: {
    channelConfigId: string
    tenantId: string
}): Promise<ListPlatformChannelsResponse> {
    return channelsClient.listPlatformChannels(create(ListPlatformChannelsRequestSchema, params))
}

export { ChannelStatus, Platform, SessionMode }
export type { ChannelConfig, PlatformChannelInfo }

