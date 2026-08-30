export interface AgentStreamEvent {
    type: string
    sessionId: string
    turnNumber: number
    textDelta: string
    toolCallId: string
    toolName: string
    toolArgs: string
    toolResult: string
    toolSuccess: boolean
    toolDurationMs: number
    finishReason: string
    error: string
    promptTokens: number
    completionTokens: number
    totalTokens: number
    cacheReadTokens?: number
    cacheWriteTokens?: number
    reviewId: string
    approvalAction: string
    pendingToolCalls: Array<{ toolCallId: string; toolName: string; toolArgs: string }>
    sandboxId: string
    sandboxExitCode: number
    sandboxDurationMs: number
    sandboxParentDurationMs?: number
    fallbackFromModel: string
    fallbackToModel: string
    fallbackAttempt: number
    /** User input (ask_user) fields */
    userInputId: string
    /** Arbitrary event payload for low-frequency events (e.g., template lists, port URLs). */
    data?: Record<string, unknown>
}

/** Shared SSE line parser — reads an SSE body stream and dispatches parsed events. */
export async function consumeSSEStream(
    response: Response,
    onEvent: (event: AgentStreamEvent) => void,
    signal?: AbortSignal,
    debugLabel = 'sse',
) {
    if (!response.body) return

    const reader = response.body.getReader()
    const decoder = new TextDecoder()
    let buffer = ''
    try {
        while (true) {
            const { done, value } = await reader.read()
            if (done) break

            const chunk = decoder.decode(value, { stream: true })
            buffer += chunk


            const lines = buffer.split('\n')
            buffer = lines.pop() ?? ''

            for (const line of lines) {
                if (!line.startsWith('data: ')) continue
                const jsonStr = line.slice(6).trim()
                if (!jsonStr || jsonStr === '[DONE]') continue

                try {
                    const event = JSON.parse(jsonStr) as AgentStreamEvent
                    const parentDuration = event?.data?.['sandbox_parent_duration_ms']
                    if (
                        event.sandboxParentDurationMs == null &&
                        typeof parentDuration === 'number'
                    ) {
                        event.sandboxParentDurationMs = parentDuration
                    }
                    onEvent(event)
                } catch (parseErr) {
                    console.warn(`[${debugLabel}] Failed to parse JSON:`, jsonStr.substring(0, 100), parseErr)
                }
            }
        }
    } finally {
        // Cancel the reader if the signal was aborted (e.g. component unmount)
        if (signal?.aborted) {
            reader.cancel().catch(() => {})
        }
    }
}
