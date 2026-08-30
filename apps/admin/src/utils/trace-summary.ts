import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getAttr } from './traces-common'
import { getProviderName } from './extract-provider-name'
import { getSpanCostUSD, getSpanTokens, isGenerationSpan } from './span-metrics'

// A trace's token/cost-bearing spans are its LLM generations, whatever SDK
// produced them (gateway provider.* spans, OTel GenAI, or coding agents like
// Claude Code whose model calls are `claude_code.llm_request`).
function isProviderSpan(span: Span): boolean {
  return isGenerationSpan(span)
}

function isAgentTurnSpan(span: Span): boolean {
  return span.spanName.startsWith('agent.turn.')
}

export interface TraceSummaryMetrics {
  inputTokens: number
  outputTokens: number
  totalTokens: number
  totalCost: number
  latency: number
}

export interface TraceSummary {
  provider: string
  model: string
  status: string
  latency: number
  cost: number
  tokens: TraceSummaryMetrics
  output?: string
  correlationId?: string
}

/**
 * Find the primary provider span (deepest provider.* span) for user-facing display
 * This is typically the actual LLM API call (e.g., provider.openai.chat)
 */
export function findPrimaryProviderSpan(spans: Span[]): Span | undefined {
  // Filter to provider spans only
  const providerSpans = spans.filter(isProviderSpan)

  if (providerSpans.length === 0) {
    // Fallback: when no provider span exists (provider hang, sampler
    // dropped it, tracing config missing), the agent turn span carries
    // the same llm.response.* / llm.tokens.* / llm.cost.* attributes —
    // see internal/agents/runtime/loop.go where they're mirrored.
    const turnSpans = spans.filter(isAgentTurnSpan)
    if (turnSpans.length > 0) {
      return turnSpans[turnSpans.length - 1]
    }

    // Otherwise fall back to root span
    return spans.find(
      (s) => !s.parentSpanId || !spans.find((p) => p.spanId === s.parentSpanId),
    )
  }

  // Find the deepest provider span (one with no provider children)
  const deepestProviderSpan = providerSpans.find((span) => {
    const children = spans.filter((s) => s.parentSpanId === span.spanId)
    const hasProviderChildren = children.some(isProviderSpan)
    return !hasProviderChildren
  })

  return deepestProviderSpan || providerSpans[0]
}

/**
 * Aggregate metrics from all spans in a trace
 */
export function aggregateTraceMetrics(spans: Span[]): TraceSummaryMetrics {
  let inputTokens = 0
  let outputTokens = 0
  let totalTokens = 0
  let totalCost = 0
  let maxEndTime = BigInt(0)
  let minStartTime = BigInt(Number.MAX_SAFE_INTEGER)
  // Prefer provider spans for token/cost aggregation. When none are
  // present (provider hang, sampler drop, tracing config missing), fall
  // back to agent turn spans which now mirror the same llm.tokens.* /
  // llm.cost.* attributes (see loop.go). Never combine the two — agent
  // turn spans are parents of provider spans and the values would
  // double-count.
  let tokenSpans = spans.filter(isProviderSpan)
  if (tokenSpans.length === 0) {
    tokenSpans = spans.filter(isAgentTurnSpan)
  }

  for (const span of tokenSpans) {
    // Coalesce token/cost across vocabularies (gateway, OTel GenAI, coding
    // agents). getSpanTokens.total already folds in cache tokens and falls back
    // to input+output when no explicit total key exists.
    const t = getSpanTokens(span)
    inputTokens += t.input
    outputTokens += t.output
    totalTokens += t.total
    totalCost += getSpanCostUSD(span)
  }

  for (const span of spans) {
    if (!span.timestamp) continue

    const startTime =
      (typeof span.timestamp.seconds === 'bigint'
        ? span.timestamp.seconds
        : BigInt(span.timestamp.seconds || 0)) *
        BigInt(1_000_000_000) +
      (typeof span.timestamp.nanos === 'bigint'
        ? span.timestamp.nanos
        : BigInt(span.timestamp.nanos || 0))
    const endTime =
      startTime +
      (typeof span.duration === 'bigint'
        ? span.duration
        : BigInt(span.duration || 0))

    if (startTime < minStartTime) minStartTime = startTime
    if (endTime > maxEndTime) maxEndTime = endTime
  }

  // Calculate total latency in nanoseconds
  const latency =
    minStartTime !== BigInt(Number.MAX_SAFE_INTEGER)
      ? Number(maxEndTime - minStartTime)
      : 0

  return {
    inputTokens,
    outputTokens,
    totalTokens: totalTokens > 0 ? totalTokens : inputTokens + outputTokens,
    totalCost,
    latency,
  }
}

/**
 * Extract a flattened trace summary from spans for user-facing display
 */
export function extractTraceSummary(spans: Span[]): TraceSummary | null {
  if (!spans || spans.length === 0) return null

  const primarySpan = findPrimaryProviderSpan(spans)
  if (!primarySpan) return null

  const metrics = aggregateTraceMetrics(spans)

  // Extract provider and model info from primary span
  const provider = getProviderName(primarySpan) || 'unknown'
  const model =
    getAttr(primarySpan, 'llm.response.model') ||
    getAttr(primarySpan, 'llm.request.model') ||
    getAttr(primarySpan, 'model.resolved') ||
    getAttr(primarySpan, 'gen_ai.request.model') ||
    getAttr(primarySpan, 'gen_ai.response.model') ||
    getAttr(primarySpan, 'model') || // coding-agent (Claude Code) bare key
    'unknown'

  // Extract status
  const status = (primarySpan.statusCode || 'UNSET').toUpperCase()

  // Extract full output (no truncation)
  let output: string | undefined
  const responseChoices = getAttr(primarySpan, 'llm.response.choices')
  if (responseChoices) {
    try {
      const choices =
        typeof responseChoices === 'string'
          ? JSON.parse(responseChoices)
          : responseChoices

      if (Array.isArray(choices) && choices.length > 0) {
        const firstChoice = choices[0]

        // Handle different message content formats
        let messageContent =
          firstChoice.message?.content || firstChoice.text || ''

        // If content is an array of parts (e.g., [{type: "text", text: "..."}]), extract text
        if (Array.isArray(messageContent)) {
          messageContent = messageContent
            .map((part: any) => {
              if (typeof part === 'string') return part
              if (part.text) return part.text
              if (part.value) return part.value
              return ''
            })
            .filter(Boolean)
            .join('\n')
        }
        // If content is an object (e.g., {type: "text", text: "..."}), extract the text
        else if (
          typeof messageContent === 'object' &&
          messageContent !== null
        ) {
          messageContent =
            messageContent.text ||
            messageContent.value ||
            JSON.stringify(messageContent)
        }

        // Store full output without truncation
        output = String(messageContent)
      }
    } catch (e) {
      // Ignore parse errors
    }
  }

  // Extract correlation ID
  const correlationId =
    getAttr(primarySpan, 'correlation.id') ||
    getAttr(primarySpan, 'correlation_id')

  return {
    provider,
    model,
    status,
    latency: metrics.latency,
    cost: metrics.totalCost,
    tokens: {
      inputTokens: metrics.inputTokens,
      outputTokens: metrics.outputTokens,
      totalTokens: metrics.totalTokens,
      totalCost: metrics.totalCost,
      latency: metrics.latency,
    },
    output,
    correlationId,
  }
}
