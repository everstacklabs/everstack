import { useMemo, useRef } from 'react'
import type { AgentStreamEvent } from '@/lib/sse-utils'

interface LiveTokenCount {
    promptTokens: number
    completionTokens: number
    totalTokens: number
}

const ZERO_TOKENS: LiveTokenCount = { promptTokens: 0, completionTokens: 0, totalTokens: 0 }

/**
 * Returns the most recent llm.end event's promptTokens.
 *
 * This is the total input-token count the provider billed for the
 * latest LLM call — i.e. the size of everything the model received
 * as input on that request. Depending on provider/model that input
 * may include:
 *   - system / developer / instruction messages
 *   - tool and function schemas
 *   - prior user / assistant turns and tool results
 *   - retrieved context (RAG snippets, attached files)
 *   - cached prompt prefixes (still in the context window even when
 *     billed at the cache-hit rate)
 *   - any provider-specific request overhead that counts toward usage
 *
 * Note: providers differ on whether "input_tokens" / "prompt_tokens"
 * includes the cached portion. OpenAI's prompt_tokens is the total
 * (cached + fresh); Anthropic's input_tokens is fresh-only and the
 * cached portion is in cache_read_input_tokens / cache_creation_input_tokens.
 * The Anthropic adapter (see internal/providers/anthropic/client.go)
 * normalises to the OpenAI convention so this value is the true
 * total-input size across providers.
 *
 * Compare against max_context_tokens to render a "context used" gauge.
 * Don't use cumulative sums of per-turn promptTokens for that purpose:
 * each turn's prompt re-includes the prior conversation, so summing
 * over-counts the actual context-window occupancy.
 *
 * Returns 0 if no llm.end event has fired yet.
 */
export function useLatestPromptTokens(events: AgentStreamEvent[]): number {
    return useMemo(() => {
        for (let i = events.length - 1; i >= 0; i--) {
            const e = events[i]
            if (e.type === 'llm.end') {
                return e.promptTokens || 0
            }
        }
        return 0
    }, [events])
}

/**
 * Derives cumulative token counts from streamed SSE events.
 * Sums `promptTokens`, `completionTokens`, and `totalTokens`
 * from all `llm.end` events in the array.
 *
 * Returns a stable object reference when values haven't changed.
 */
export function useLiveTokenCount(events: AgentStreamEvent[]): LiveTokenCount {
    const prevRef = useRef(ZERO_TOKENS)

    return useMemo(() => {
        let promptTokens = 0
        let completionTokens = 0
        let totalTokens = 0

        for (const e of events) {
            if (e.type === 'llm.end') {
                promptTokens += e.promptTokens || 0
                completionTokens += e.completionTokens || 0
                totalTokens += e.totalTokens || 0
            }
        }

        const prev = prevRef.current
        if (
            prev.promptTokens === promptTokens &&
            prev.completionTokens === completionTokens &&
            prev.totalTokens === totalTokens
        ) {
            return prev
        }

        const next = { promptTokens, completionTokens, totalTokens }
        prevRef.current = next
        return next
    }, [events])
}
