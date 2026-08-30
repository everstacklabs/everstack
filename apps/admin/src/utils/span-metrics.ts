/**
 * Cross-vocabulary span metric extraction.
 *
 * Spans reach us from several instrumentations that each spell token/cost/timing
 * attributes differently: Everstack-native (`llm.tokens.*`, `llm.cost.*`), the
 * OpenTelemetry GenAI semconv (`gen_ai.usage.*`), OpenInference (`llm.token_count.*`),
 * and coding agents like Claude Code (bare `input_tokens` / `output_tokens` /
 * `cache_read_tokens` / `cache_creation_tokens`, `ttft_ms`, `cost.estimated_usd`).
 *
 * These helpers coalesce across all of them so the trace UI shows real numbers
 * regardless of which SDK produced the span. The key lists mirror
 * `internal/query/handlers/traces/semconv.go` so the read-model columns and the
 * client agree.
 */
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr } from './traces-common'

function num(v: unknown): number {
    if (v == null || v === '') return 0
    const n = typeof v === 'number' ? v : Number(v)
    return Number.isFinite(n) ? n : 0
}

/** First strictly-positive numeric attribute among the given keys. */
function firstNum(span: Span, keys: readonly string[]): number {
    for (const k of keys) {
        const n = num(getAttr(span, k))
        if (n > 0) return n
    }
    return 0
}

const INPUT_TOKEN_KEYS = [
    'llm.tokens.input',
    'gen_ai.usage.input_tokens',
    'gen_ai.usage.prompt_tokens',
    'llm.token_count.prompt',
    'input_tokens',
] as const
const OUTPUT_TOKEN_KEYS = [
    'llm.tokens.output',
    'gen_ai.usage.output_tokens',
    'gen_ai.usage.completion_tokens',
    'llm.token_count.completion',
    'output_tokens',
] as const
const TOTAL_TOKEN_KEYS = [
    'llm.tokens.total',
    'gen_ai.usage.total_tokens',
    'llm.token_count.total',
] as const
const CACHE_READ_KEYS = [
    'llm.tokens.cache_read',
    'llm.tokens.cached',
    'gen_ai.usage.cache_read_input_tokens',
    'cache_read_tokens',
] as const
const CACHE_WRITE_KEYS = [
    'llm.tokens.cache_creation',
    'gen_ai.usage.cache_creation_input_tokens',
    'cache_creation_tokens',
] as const
const COST_KEYS = ['llm.cost.total', 'cost.estimated_usd', 'cost_usd'] as const
// Time to first token. Native/OTel keys are nanoseconds; coding agents emit ms.
const TTFT_NS_KEYS = [
    'llm.stream.time_to_first_token',
    'llm.time_to_first_token_ns',
    'llm.ttft_ns',
] as const
const TTFT_MS_KEYS = ['ttft_ms'] as const

export interface SpanTokens {
    input: number
    output: number
    cacheRead: number
    cacheWrite: number
    /** input + output + cache when no explicit total key is present. */
    total: number
}

export function getSpanTokens(span: Span): SpanTokens {
    const input = firstNum(span, INPUT_TOKEN_KEYS)
    const output = firstNum(span, OUTPUT_TOKEN_KEYS)
    const cacheRead = firstNum(span, CACHE_READ_KEYS)
    const cacheWrite = firstNum(span, CACHE_WRITE_KEYS)
    const explicitTotal = firstNum(span, TOTAL_TOKEN_KEYS)
    const total = explicitTotal > 0 ? explicitTotal : input + output + cacheRead + cacheWrite
    return { input, output, cacheRead, cacheWrite, total }
}

/** Total LLM cost in USD for a span, across native + ingest-stamped keys. */
export function getSpanCostUSD(span: Span): number {
    return firstNum(span, COST_KEYS)
}

/** Time to first token in milliseconds, normalising ns sources to ms. */
export function getSpanTtftMs(span: Span): number {
    for (const k of TTFT_NS_KEYS) {
        const n = num(getAttr(span, k))
        if (n > 0) return n / 1_000_000
    }
    return firstNum(span, TTFT_MS_KEYS)
}

/**
 * Does this span represent an LLM/model generation? Covers the Everstack gateway
 * (`provider.*` / observation.type GENERATION), the OTel GenAI semconv, and
 * coding-agent SDKs (`span.type=llm_request`, `*.llm_request`, `gen_ai.*`).
 */
export function isGenerationSpan(span: Span): boolean {
    if (span.spanName.startsWith('provider.')) return true
    const obs = String(getAttr(span, 'observation.type') ?? '').toUpperCase()
    if (obs === 'GENERATION' || obs === 'LLM') return true
    const st = String(getAttr(span, 'span.type') ?? '').toLowerCase()
    if (st === 'llm_request' || st === 'generation' || st === 'llm') return true
    const name = span.spanName.toLowerCase()
    return name.includes('llm_request') || name.startsWith('gen_ai')
}
