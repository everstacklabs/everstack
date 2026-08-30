/**
 * Playground server functions — minimal chat-completion driver for the
 * Phase 1 inline re-run flow. Uses the existing GatewayService stream RPC
 * so the call goes through the same trace ingestion path as production
 * traffic. The resulting trace is queryable in /observability/traces with
 * no extra plumbing.
 */

import { createServerTransport } from '@/server'
import { createClientFor, create } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { GatewayService } from '@everstack/proto/everstack/gateway/v1/gateway_service_pb'
import {
    ChatCompletionRequestSchema,
    type ChatResponseChunk,
    Role,
} from '@everstack/proto/everstack/gateway/v1/chat_pb'

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

export type PlaygroundMessage = {
    role: 'system' | 'user' | 'assistant'
    text: string
}

export type PlaygroundRunParams = {
    model: string
    messages: PlaygroundMessage[]
    temperature?: number
    maxTokens?: number
    topP?: number
    metadata?: Record<string, string>
    /** e.g. { type: 'json_object' } to request JSON output. */
    responseFormat?: Record<string, unknown>
}

function roleEnum(r: PlaygroundMessage['role']): Role {
    switch (r) {
        case 'system':
            return Role.SYSTEM
        case 'assistant':
            return Role.ASSISTANT
        default:
            return Role.USER
    }
}

/**
 * Stream a chat completion. Yields chunks; consumers accumulate the text
 * deltas as they arrive.
 */
export async function* streamChatCompletion(
    params: PlaygroundRunParams,
    opts?: { signal?: AbortSignal },
): AsyncGenerator<ChatResponseChunk, void, unknown> {
    const req = create(ChatCompletionRequestSchema, {
        model: params.model,
        stream: true,
        messages: params.messages.map((m) => ({
            role: roleEnum(m.role),
            content: [
                {
                    type: 'text',
                    data: { case: 'text' as const, value: m.text },
                },
            ],
        })),
        sampling:
            params.temperature !== undefined ||
            params.maxTokens !== undefined ||
            params.topP !== undefined
                ? {
                      temperature: params.temperature ?? 0,
                      maxTokens: params.maxTokens ?? 0,
                      topP: params.topP ?? 0,
                  }
                : undefined,
        responseFormat: params.responseFormat as never,
        // Tag the completion so we can recognise playground re-runs in the
        // traces list and link back to the originating span. The proto field is
        // a google.protobuf.Struct, which protobuf-es represents as a plain
        // object here — pass the flat map directly (wrapping it in the
        // low-level {fields:{kind:…}} shape would create a literal "fields" key).
        metadata: (params.metadata ?? undefined) as never,
    })

    const stream = gatewayClient.chatCompletion(req, { signal: opts?.signal })
    for await (const message of stream) {
        // The ChatCompletionResponse wraps a ChatResponseChunk (per chat.proto).
        // We forward the chunk so the UI can append delta text directly.
        const chunk = (message as any).chunk ?? message
        if (chunk) yield chunk as ChatResponseChunk
    }
}

/** Pull just the text out of a ChatResponseChunk delta. */
export function deltaText(chunk: ChatResponseChunk): string {
    const choice = chunk.choices?.[0]
    if (!choice) return ''
    const parts = choice.delta?.content ?? []
    let out = ''
    for (const p of parts) {
        if (p.data?.case === 'text') out += p.data.value
    }
    return out
}
