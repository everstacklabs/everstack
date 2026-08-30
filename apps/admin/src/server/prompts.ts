import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { PromptService } from '@everstack/proto/everstack/prompts/v1/prompts_service_pb'
import type {
    CreatePromptResponse,
    CreatePromptVersionResponse,
    DeletePromptResponse,
    GetPromptResponse,
    GetPromptVersionResponse,
    ListPromptsResponse,
    ListPromptVersionsResponse,
    Prompt,
    PromptMessage,
    PromptVersion,
    SetPromptLabelsResponse,
    UpdatePromptResponse,
} from '@everstack/proto/everstack/prompts/v1/prompts_pb'
import {
    CreatePromptRequestSchema,
    CreatePromptVersionRequestSchema,
    DeletePromptRequestSchema,
    GetPromptRequestSchema,
    GetPromptVersionRequestSchema,
    ListPromptsRequestSchema,
    ListPromptVersionsRequestSchema,
    SetPromptLabelsRequestSchema,
    UpdatePromptRequestSchema,
} from '@everstack/proto/everstack/prompts/v1/prompts_pb'

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
const promptClient = createClientFor(PromptService)(transport)

export type { Prompt, PromptMessage, PromptVersion }

export type PromptMessageInput = {
    role: 'system' | 'user' | 'assistant'
    content: string
}

/** Default inference config stored alongside a version. */
export type PromptConfigInput = {
    model?: string
    temperature?: number
    topP?: number
    maxTokens?: number
}

function configToStruct(config?: PromptConfigInput) {
    if (!config) return undefined
    const entries = Object.entries(config).filter(([, v]) => v !== undefined)
    if (entries.length === 0) return undefined
    return Object.fromEntries(entries)
}

// ─── Prompt CRUD ─────────────────────────────────────────────────────

export type CreatePromptParams = {
    name: string
    description?: string
    tags?: string[]
    messages?: PromptMessageInput[]
    config?: PromptConfigInput
    commitMessage?: string
}

export async function createPrompt(params: CreatePromptParams): Promise<CreatePromptResponse> {
    const req = create(CreatePromptRequestSchema, {
        name: params.name,
        description: params.description ?? '',
        tags: params.tags ?? [],
        messages: params.messages ?? [],
        config: configToStruct(params.config),
        commitMessage: params.commitMessage ?? '',
    })
    return promptClient.createPrompt(req)
}

export async function getPrompt(idOrName: { id?: string; name?: string }): Promise<GetPromptResponse> {
    const req = create(GetPromptRequestSchema, {
        id: idOrName.id ?? '',
        name: idOrName.name ?? '',
    })
    return promptClient.getPrompt(req)
}

export async function listPrompts(params?: { limit?: number; offset?: number }): Promise<ListPromptsResponse> {
    const req = create(ListPromptsRequestSchema, {
        limit: params?.limit,
        offset: params?.offset,
    })
    return promptClient.listPrompts(req)
}

export type UpdatePromptParams = {
    id: string
    name?: string
    description?: string
    tags?: string[]
}

export async function updatePrompt(params: UpdatePromptParams): Promise<UpdatePromptResponse> {
    const req = create(UpdatePromptRequestSchema, {
        id: params.id,
        name: params.name,
        description: params.description,
        tags: params.tags ?? [],
        setTags: params.tags !== undefined,
    })
    return promptClient.updatePrompt(req)
}

export async function deletePrompt(id: string): Promise<DeletePromptResponse> {
    const req = create(DeletePromptRequestSchema, { id })
    return promptClient.deletePrompt(req)
}

// ─── Versions ────────────────────────────────────────────────────────

export type CreatePromptVersionParams = {
    promptId: string
    messages: PromptMessageInput[]
    config?: PromptConfigInput
    commitMessage?: string
    labels?: string[]
}

export async function createPromptVersion(
    params: CreatePromptVersionParams,
): Promise<CreatePromptVersionResponse> {
    const req = create(CreatePromptVersionRequestSchema, {
        promptId: params.promptId,
        messages: params.messages,
        config: configToStruct(params.config),
        commitMessage: params.commitMessage ?? '',
        labels: params.labels ?? [],
    })
    return promptClient.createPromptVersion(req)
}

export async function listPromptVersions(params: {
    promptId: string
    limit?: number
    offset?: number
}): Promise<ListPromptVersionsResponse> {
    const req = create(ListPromptVersionsRequestSchema, {
        promptId: params.promptId,
        limit: params.limit,
        offset: params.offset,
    })
    return promptClient.listPromptVersions(req)
}

export async function getPromptVersion(params: {
    promptId: string
    version?: number
    label?: string
}): Promise<GetPromptVersionResponse> {
    const req = create(GetPromptVersionRequestSchema, {
        promptId: params.promptId,
        version: params.version,
        label: params.label,
    })
    return promptClient.getPromptVersion(req)
}

export async function setPromptLabels(params: {
    promptId: string
    version: number
    labels: string[]
}): Promise<SetPromptLabelsResponse> {
    const req = create(SetPromptLabelsRequestSchema, {
        promptId: params.promptId,
        version: params.version,
        labels: params.labels,
    })
    return promptClient.setPromptLabels(req)
}

/** Pull a typed config object back out of a version's Struct. */
export function versionConfig(version?: PromptVersion | null): PromptConfigInput {
    const fields = (version?.config ?? {}) as Record<string, unknown>
    const num = (v: unknown) => (typeof v === 'number' ? v : undefined)
    return {
        model: typeof fields.model === 'string' ? fields.model : undefined,
        temperature: num(fields.temperature),
        topP: num(fields.topP),
        maxTokens: num(fields.maxTokens),
    }
}
