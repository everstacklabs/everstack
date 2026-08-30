import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { VoiceService } from '@everstack/proto/everstack/voice/v1/voice_service_pb'
import type {
    VoiceCloneProfile,
} from '@everstack/proto/everstack/voice/v1/voice_pb'
import {
    CreateVoiceCloneProfileRequestSchema,
    ListVoiceCloneProfilesRequestSchema,
    GetVoiceCloneProfileRequestSchema,
    UpdateVoiceCloneProfileRequestSchema,
    DeleteVoiceCloneProfileRequestSchema,
} from '@everstack/proto/everstack/voice/v1/voice_pb'

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
const voiceClient = createClientFor(VoiceService)(transport)

// ─── Voice Clone Profiles ────────────────────────────────────────────

export type ListVoiceCloneProfilesParams = {
    orgId: string
}

export async function listVoiceCloneProfiles(params: ListVoiceCloneProfilesParams) {
    const req = create(ListVoiceCloneProfilesRequestSchema, {
        orgId: params.orgId,
    })
    return voiceClient.listVoiceCloneProfiles(req)
}

export type CreateVoiceCloneProfileParams = {
    orgId: string
    name: string
    description?: string
    referenceAudio?: Uint8Array
    referenceText?: string
    provider?: string
    model?: string
}

export async function createVoiceCloneProfile(params: CreateVoiceCloneProfileParams) {
    const req = create(CreateVoiceCloneProfileRequestSchema, {
        orgId: params.orgId,
        name: params.name,
        description: params.description ?? '',
        referenceAudio: params.referenceAudio,
        referenceText: params.referenceText ?? '',
        provider: params.provider ?? 'qwen',
        model: params.model ?? 'qwen3-tts-vc-2026-01-22',
    })
    return voiceClient.createVoiceCloneProfile(req)
}

export type GetVoiceCloneProfileParams = {
    id: string
}

export async function getVoiceCloneProfile(params: GetVoiceCloneProfileParams) {
    const req = create(GetVoiceCloneProfileRequestSchema, {
        id: params.id,
    })
    return voiceClient.getVoiceCloneProfile(req)
}

export type UpdateVoiceCloneProfileParams = {
    id: string
    name?: string
    description?: string
    referenceText?: string
}

export async function updateVoiceCloneProfile(params: UpdateVoiceCloneProfileParams) {
    const req = create(UpdateVoiceCloneProfileRequestSchema, {
        id: params.id,
        name: params.name,
        description: params.description,
        referenceText: params.referenceText,
    })
    return voiceClient.updateVoiceCloneProfile(req)
}

export type DeleteVoiceCloneProfileParams = {
    id: string
}

export async function deleteVoiceCloneProfile(params: DeleteVoiceCloneProfileParams) {
    const req = create(DeleteVoiceCloneProfileRequestSchema, {
        id: params.id,
    })
    return voiceClient.deleteVoiceCloneProfile(req)
}

export type { VoiceCloneProfile }
