import { Route } from '@/routes/observability/traces'
import {
  getSpanDisplayConfig,
  humanizeSpanName,
} from '@/utils/span-title-name-map'
import { SpanTypeSummaryCard } from './span-type-summary-card'
import {
  categoryIcons,
  categoryColors,
  categoryLabels,
} from '@/utils/span-display-helpers'
import { extractTraceSummary } from '@/utils/trace-summary'
import {
  getAttr,
  getSpanInput,
  getSpanInputPayload,
  getSpanOutput,
  getSpanOutputPayload,
  type SpanIOPayload,
} from '@/utils/traces-common'
import { getSpanCostUSD, getSpanTokens } from '@/utils/span-metrics'
import {
  summarizeGuardrails,
  type GuardrailCheck,
  type RawSpanEvent,
} from '@/utils/guardrail-events'
import type {
  Span,
  Trace,
} from '@everstack/proto/everstack/traces/v1/traces_pb'
import { ui, Iconify } from '@everstack/ui'
import { capitalize } from '@everstack/utils/functions/capitalize'
import { cn } from '@everstack/utils/functions/cn'
import dayjs from 'dayjs'
import {
  Activity,
  ArrowLeft,
  CheckCircle2,
  Clock,
  Code,
  Database,
  DollarSign,
  ExternalLink,
  Info,
  Layers,
  MessageSquare,
  Server,
  Shield,
  Sparkles,
  Target,
  Zap,
  AlertCircle,
  AlertTriangle,
  Play,
  ChevronRight,
  Search,
  XCircle,
  RotateCcw,
  ArrowRight,
  Gauge,
  Tag,
  Hash,
  Radio,
  GitCompare,
} from 'lucide-react'
import { useState } from 'react'
import { Link } from '@tanstack/react-router'

import { JsonViewer } from '@/ui/json-viewer'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { MARKDOWN_COMPONENTS } from './markdown-code'
import { ProviderDisplay } from '../providers/provider-icon'
import {
  formatDuration,
  formatCost,
  formatTokens,
  parseTokenCount,
  safeJsonParse,
  calculatePercentage,
  safeBigIntToNumber,
} from '@/utils/trace-formatters'
import { AttributeDisplay } from './attribute-display'
import {
  AddToDatasetDialog,
  type AddToDatasetPayload,
} from '@/components/evaluations/add-to-dataset-dialog'
import type { JsonObject } from '@/server/datasets'
import { ScoresPanel } from './scores-panel'
import { AnnotationsPanel } from './annotations-panel'
import { TraceCostBreakdown } from './trace-cost-breakdown'
import {
  tokenTint,
  vizTrack,
  statusTint,
  statusBadge,
  roleBadge,
  costBadgeCls,
} from './trace-viz'
import { GenerationPlayback, isPlaybackable } from './generation-playback'
import { TokenHighlight, isTokenHighlightable } from './token-highlight'
import {
  ConversationView,
  hasStructuredConversation,
} from './conversation-view'
import { AgentRunSummary } from './agent-run-summary'
import {
  formatAttributeName,
  groupAttributes as groupAttributesByPrefix,
  getGroupDescription,
  sortAttributeKeys,
} from '@/utils/attribute-formatter'

// GFM (tables, strikethrough, task lists, autolinks) — bare react-markdown is
// CommonMark only. Module-level so the array reference stays stable across renders.
const REMARK_PLUGINS = [remarkGfm]

const {
  Badge,
  Tabs,
  TabsContent,
  TabsList,
  TabsTrigger,
  Button,
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
  TooltipProvider,
  Tooltip,
} = ui

interface TraceOverviewProps {
  trace: Trace
  selectedSpan?: Span
  allSpans?: Span[]
}

type IOViewMode = 'formatted' | 'json'
type TraceSummary = NonNullable<ReturnType<typeof extractTraceSummary>>

// --- Formatters ---

function timestampToMs(timestamp: any): number {
  if (!timestamp) return 0
  if (timestamp.seconds !== undefined) {
    const seconds =
      typeof timestamp.seconds === 'bigint'
        ? safeBigIntToNumber(timestamp.seconds)
        : Number(timestamp.seconds || 0)
    const nanos =
      typeof timestamp.nanos === 'bigint'
        ? safeBigIntToNumber(timestamp.nanos)
        : Number(timestamp.nanos || 0)
    return seconds * 1000 + nanos / 1_000_000
  }
  // Fallback for other timestamp formats
  if (timestamp instanceof Date) {
    return timestamp.getTime()
  }
  if (typeof timestamp === 'number') {
    return timestamp
  }
  return new Date(timestamp).getTime()
}

function formatTimestamp(timestamp: any): string {
  if (!timestamp) return 'N/A'
  // Handle protobuf Timestamp with seconds property
  if (timestamp.seconds !== undefined) {
    const seconds =
      typeof timestamp.seconds === 'bigint'
        ? safeBigIntToNumber(timestamp.seconds)
        : Number(timestamp.seconds || 0)
    const date = new Date(seconds * 1000)
    return dayjs(date).format('MMM D, HH:mm:ss.SSS')
  }
  // Fallback for Date objects or other formats
  if (timestamp instanceof Date) {
    return dayjs(timestamp).format('MMM D, HH:mm:ss.SSS')
  }
  // Try to parse as date string or number
  return dayjs(timestamp).format('MMM D, HH:mm:ss.SSS')
}

function safeToString(value: any): string {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean')
    return String(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return String(value)
}

function isRootLikeSpan(span: Span): boolean {
  const observationType = String(
    getAttr(span, 'observation.type') || '',
  ).toUpperCase()
  return (
    !span.parentSpanId ||
    observationType === 'TRACE' ||
    observationType === 'ROOT'
  )
}

function getDisplayIOPayloads(span: Span) {
  const includeTraceFallback = isRootLikeSpan(span)
  const input = getSpanInputPayload(span, { includeTraceFallback })
  const output = getSpanOutputPayload(span, { includeTraceFallback })
  const rawInput = getSpanInputPayload(span, { includeTraceFallback: true })
  const rawOutput = getSpanOutputPayload(span, { includeTraceFallback: true })

  return {
    input,
    output,
    hasSuppressedTracePayload:
      (!input && rawInput?.scope === 'trace') ||
      (!output && rawOutput?.scope === 'trace'),
  }
}

function sourceLabel(source?: SpanIOPayload): string | undefined {
  if (!source) return undefined
  return source.scope === 'trace' ? 'trace payload' : 'span payload'
}

// --- Components ---

// Shared legend row for the token breakdown bars: swatch · label · value · pct.
function TokenLegend({
  tint,
  label,
  value,
  pct,
}: {
  tint: { dot: string; text: string }
  label: string
  value: string
  pct?: string
}) {
  return (
    <div className="flex items-center gap-2">
      <span className={cn('w-2.5 h-2.5 rounded-sm shrink-0', tint.dot)} />
      <span className="text-zinc-400">{label}:</span>
      <span className={cn('', tint.text)}>{value}</span>
      {pct && <span className="text-zinc-500">({pct})</span>}
    </div>
  )
}

// Token breakdown visualization component
function TokenBreakdownSection({
  spans,
  inputTokens,
  outputTokens,
}: {
  spans: Span[]
  inputTokens: number
  outputTokens: number
}) {
  // Use the aggregated token counts passed from parent (from summary)
  const totalInput = inputTokens
  const totalOutput = outputTokens
  const totalTokens = totalInput + totalOutput

  // Find the provider span for detailed breakdown data
  const providerSpan = spans.find(
    (span) =>
      span.spanName.startsWith('provider.') ||
      getAttr(span, 'observation.type') === 'GENERATION',
  )

  // Get token breakdown attributes from provider span
  const cachedTokens = providerSpan
    ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.cached'))
    : 0
  const reasoningTokens = providerSpan
    ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.reasoning'))
    : 0
  const audioTokens = providerSpan
    ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.audio'))
    : 0
  const textTokens = providerSpan
    ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.text'))
    : 0

  // Get detailed breakdown JSON
  const promptDetailsRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.prompt_details')
    : null
  const completionDetailsRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.completion_details')
    : null
  const perMessageTokensRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.per_message')
    : null

  // Parse JSON data
  const promptDetails = safeJsonParse<any>(promptDetailsRaw, null)
  const completionDetails = safeJsonParse<any>(completionDetailsRaw, null)
  const perMessageTokens = safeJsonParse<number[]>(perMessageTokensRaw, [])

  const hasDetailedBreakdown =
    cachedTokens > 0 ||
    reasoningTokens > 0 ||
    audioTokens > 0 ||
    textTokens > 0 ||
    promptDetails ||
    completionDetails ||
    perMessageTokens.length > 0

  // Get model info to show appropriate message
  const model = providerSpan
    ? getAttr(providerSpan, 'llm.response.model') ||
      getAttr(providerSpan, 'llm.request.model') ||
      ''
    : ''

  return (
    <div className="space-y-4">
      {/* Always show input/output distribution */}
      {totalTokens > 0 && (
        <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
          <div className="flex items-center gap-2 mb-3">
            <Zap className="h-4 w-4 text-brand-main-400" />
            <span className="font-medium text-sm text-zinc-200">
              Token Distribution
            </span>
          </div>

          {/* Proportional bar — labels live in the legend so nothing overflows */}
          <div
            className={cn(
              'h-2.5 rounded-full overflow-hidden flex gap-px mb-3',
              vizTrack,
            )}
          >
            {cachedTokens > 0 && (
              <div
                className={cn('h-full', tokenTint.cached.bar)}
                style={{ width: `${(cachedTokens / totalTokens) * 100}%` }}
                title={`Cached: ${cachedTokens} tokens (charged at provider's cache rate)`}
              />
            )}
            <div
              className={cn('h-full', tokenTint.input.bar)}
              style={{
                width: `${((totalInput - cachedTokens) / totalTokens) * 100}%`,
              }}
              title={`${cachedTokens > 0 ? 'Fresh input' : 'Input'}: ${totalInput - cachedTokens} tokens`}
            />
            <div
              className={cn('h-full', tokenTint.output.bar)}
              style={{ width: `${(totalOutput / totalTokens) * 100}%` }}
              title={`Output: ${totalOutput} tokens`}
            />
          </div>

          <div
            className={`grid ${cachedTokens > 0 ? 'grid-cols-4' : 'grid-cols-3'} gap-3 text-xs`}
          >
            {cachedTokens > 0 && (
              <TokenLegend
                tint={tokenTint.cached}
                label="Cached"
                value={formatTokens(cachedTokens)}
                pct={calculatePercentage(cachedTokens, totalTokens, 0)}
              />
            )}
            <TokenLegend
              tint={tokenTint.input}
              label={cachedTokens > 0 ? 'Fresh' : 'Input'}
              value={formatTokens(totalInput - cachedTokens)}
              pct={calculatePercentage(
                totalInput - cachedTokens,
                totalTokens,
                0,
              )}
            />
            <TokenLegend
              tint={tokenTint.output}
              label="Output"
              value={formatTokens(totalOutput)}
              pct={calculatePercentage(totalOutput, totalTokens, 0)}
            />
            <div className="flex items-center gap-2">
              <span className="text-zinc-400">Ratio:</span>
              <span className="text-zinc-200">
                1:{(totalOutput / Math.max(totalInput, 1)).toFixed(2)}
              </span>
            </div>
          </div>
        </div>
      )}

      {/* Detailed breakdown section */}
      {hasDetailedBreakdown ? (
        <>
          {/* Prompt Token Breakdown */}
          {(cachedTokens > 0 || promptDetails) && (
            <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
              <div className="flex items-center gap-2 mb-3">
                <Target className={cn('h-4 w-4', tokenTint.input.text)} />
                <span className="font-medium text-sm text-zinc-200">
                  Input Token Breakdown
                </span>
              </div>

              <div
                className={cn(
                  'h-2 rounded-full overflow-hidden flex gap-px mb-3',
                  vizTrack,
                )}
              >
                {cachedTokens > 0 && totalInput > 0 && (
                  <div
                    className={cn('h-full', tokenTint.cached.bar)}
                    style={{ width: `${(cachedTokens / totalInput) * 100}%` }}
                    title={`Cached: ${cachedTokens} tokens`}
                  />
                )}
                <div
                  className={cn('h-full flex-1', tokenTint.input.bar)}
                  title={`Computed: ${totalInput - cachedTokens} tokens`}
                />
              </div>

              <div className="grid grid-cols-2 gap-3 text-xs">
                {cachedTokens > 0 && (
                  <TokenLegend
                    tint={tokenTint.cached}
                    label="Cached"
                    value={formatTokens(cachedTokens)}
                    pct={calculatePercentage(cachedTokens, totalInput)}
                  />
                )}
                <TokenLegend
                  tint={tokenTint.input}
                  label="Computed"
                  value={formatTokens(totalInput - cachedTokens)}
                />
              </div>
            </div>
          )}

          {/* Completion Token Breakdown */}
          {(reasoningTokens > 0 || completionDetails) && (
            <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
              <div className="flex items-center gap-2 mb-3">
                <Sparkles className={cn('h-4 w-4', tokenTint.reasoning.text)} />
                <span className="font-medium text-sm text-zinc-200">
                  Output Token Breakdown
                </span>
              </div>

              <div
                className={cn(
                  'h-2 rounded-full overflow-hidden flex gap-px mb-3',
                  vizTrack,
                )}
              >
                {reasoningTokens > 0 && totalOutput > 0 && (
                  <div
                    className={cn('h-full', tokenTint.reasoning.bar)}
                    style={{
                      width: `${(reasoningTokens / totalOutput) * 100}%`,
                    }}
                    title={`Reasoning: ${reasoningTokens} tokens`}
                  />
                )}
                {audioTokens > 0 && totalOutput > 0 && (
                  <div
                    className={cn('h-full', tokenTint.audio.bar)}
                    style={{ width: `${(audioTokens / totalOutput) * 100}%` }}
                    title={`Audio: ${audioTokens} tokens`}
                  />
                )}
                <div
                  className={cn('h-full flex-1', tokenTint.output.bar)}
                  title={`Text: ${totalOutput - reasoningTokens - audioTokens} tokens`}
                />
              </div>

              <div className="grid grid-cols-3 gap-3 text-xs">
                {reasoningTokens > 0 && (
                  <TokenLegend
                    tint={tokenTint.reasoning}
                    label="Reasoning"
                    value={formatTokens(reasoningTokens)}
                  />
                )}
                {audioTokens > 0 && (
                  <TokenLegend
                    tint={tokenTint.audio}
                    label="Audio"
                    value={formatTokens(audioTokens)}
                  />
                )}
                <TokenLegend
                  tint={tokenTint.output}
                  label="Text"
                  value={formatTokens(
                    totalOutput - reasoningTokens - audioTokens,
                  )}
                />
              </div>
            </div>
          )}

          {/* Per-Message Token Breakdown */}
          {perMessageTokens.length > 0 && (
            <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
              <div className="flex items-center gap-2 mb-3">
                <MessageSquare
                  className={cn('h-4 w-4', tokenTint.input.text)}
                />
                <span className="font-medium text-sm text-zinc-200">
                  Per-Message Token Estimates
                </span>
                <Badge
                  variant="outline"
                  className="text-[10px] text-zinc-400 border-brand-main-500"
                >
                  Estimated
                </Badge>
              </div>

              <div className="space-y-2">
                {perMessageTokens.map((tokens, idx) => (
                  <div key={idx} className="flex items-center gap-3">
                    <span className="text-xs text-zinc-400 w-20">
                      Message {idx + 1}
                    </span>
                    <div
                      className={cn(
                        'flex-1 h-2.5 rounded-full overflow-hidden',
                        vizTrack,
                      )}
                    >
                      <div
                        className={cn(
                          'h-full rounded-full',
                          tokenTint.input.bar,
                        )}
                        style={{
                          width: `${Math.min((tokens / Math.max(...perMessageTokens)) * 100, 100)}%`,
                        }}
                      />
                    </div>
                    <span className="text-xs text-zinc-200 w-16 text-right">
                      {formatTokens(tokens)}
                    </span>
                  </div>
                ))}
              </div>
            </div>
          )}
        </>
      ) : (
        <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
          <div className="flex items-center gap-2 text-zinc-400">
            <Info className="h-4 w-4" />
            <span className="text-sm">
              No detailed token breakdown available for this model.
            </span>
          </div>
          <p className="text-xs text-zinc-500 mt-2">
            {model && model.includes('gpt-4-0613') ? (
              <>
                The <code className="text-zinc-400">gpt-4-0613</code> model does
                not return detailed token breakdown. Try using newer models like{' '}
                <code className="text-zinc-400">gpt-4-turbo</code>,{' '}
                <code className="text-zinc-400">gpt-4o</code>, or{' '}
                <code className="text-zinc-400">o1</code> for cached/reasoning
                token details.
              </>
            ) : model &&
              (model.includes('gpt-3.5') || model.includes('gpt-4-0314')) ? (
              <>
                Older OpenAI models do not return detailed token breakdown. Use{' '}
                <code className="text-zinc-400">gpt-4-turbo</code>,{' '}
                <code className="text-zinc-400">gpt-4o</code>, or newer for
                detailed breakdowns.
              </>
            ) : (
              <>
                Detailed breakdown (cached tokens, reasoning tokens, etc.) is
                available for newer models with prompt caching or reasoning
                capabilities.
              </>
            )}
          </p>
        </div>
      )}
    </div>
  )
}

// Helper to extract text from a content part (handles {type, text} format)
function extractTextFromContentPart(part: any): string {
  if (typeof part === 'string') return part
  if (part.text) return part.text
  if (part.value) return part.value
  if (part.content && typeof part.content === 'string') return part.content
  return ''
}

// Helper to extract text content from various data structures
function extractFormattedContent(
  data: any,
  options: { includeJsonFallback?: boolean } = {},
): { role?: string; text: string }[] {
  if (!data) return []
  const includeJsonFallback = options.includeJsonFallback ?? true

  let parsed = data
  if (typeof data === 'string') {
    const parsedData = safeJsonParse<any>(data, null)
    if (parsedData === null) {
      // Plain text, return as-is
      return [{ text: data }]
    }
    parsed = parsedData
  }

  // Handle array of content parts directly (e.g., [{"type":"text","text":"..."}])
  if (Array.isArray(parsed)) {
    // Check if this is an array of content parts (has type/text structure)
    const isContentPartsArray = parsed.every(
      (item: any) => typeof item === 'object' && (item.type || item.text),
    )

    if (isContentPartsArray) {
      const texts = parsed
        .map((part: any) => extractTextFromContentPart(part))
        .filter(Boolean)
      if (texts.length > 0) {
        return [{ text: texts.join('\n') }]
      }
    }

    // Handle array of messages (chat format with role/content)
    return parsed
      .flatMap((msg: any) => {
        if (typeof msg === 'string') return [{ text: msg }]

        // Handle nested message structure (e.g., { index: 0, message: { role, content } })
        if (msg.message !== undefined) {
          const innerMsg = msg.message
          if (typeof innerMsg.content === 'string') {
            return [{ role: innerMsg.role, text: innerMsg.content }]
          }
          if (Array.isArray(innerMsg.content)) {
            const texts = innerMsg.content
              .map((c: any) => extractTextFromContentPart(c))
              .filter(Boolean)
            if (texts.length > 0) {
              return [{ role: innerMsg.role, text: texts.join('\n') }]
            }
          }
        }

        // Message with content field
        if (msg.content !== undefined) {
          if (typeof msg.content === 'string') {
            return [{ role: msg.role, text: msg.content }]
          }
          if (Array.isArray(msg.content)) {
            const texts = msg.content
              .map((c: any) => extractTextFromContentPart(c))
              .filter(Boolean)
            if (texts.length > 0) {
              return [{ role: msg.role, text: texts.join('\n') }]
            }
          }
        }

        // Direct text field
        if (msg.text) return [{ text: msg.text }]

        return []
      })
      .filter((item: any) => item.text)
  }

  // Handle single message object
  if (typeof parsed === 'object' && parsed !== null) {
    // Content field (string or array)
    if (parsed.content !== undefined) {
      if (typeof parsed.content === 'string') {
        return [{ role: parsed.role, text: parsed.content }]
      }
      if (Array.isArray(parsed.content)) {
        const texts = parsed.content
          .map((c: any) => extractTextFromContentPart(c))
          .filter(Boolean)
        if (texts.length > 0) {
          return [{ role: parsed.role, text: texts.join('\n') }]
        }
      }
    }

    // Direct text field
    if (parsed.text) return [{ text: parsed.text }]

    // Message field
    if (parsed.message) {
      if (typeof parsed.message === 'string') return [{ text: parsed.message }]
      if (parsed.message.content)
        return [{ role: parsed.message.role, text: parsed.message.content }]
    }

    // OpenAI completion response format
    if (parsed.choices && Array.isArray(parsed.choices)) {
      return parsed.choices
        .flatMap((choice: any) => {
          if (choice.message?.content) {
            if (typeof choice.message.content === 'string') {
              return [
                { role: choice.message.role, text: choice.message.content },
              ]
            }
            if (Array.isArray(choice.message.content)) {
              const texts = choice.message.content
                .map((c: any) => extractTextFromContentPart(c))
                .filter(Boolean)
              if (texts.length > 0) {
                return [{ role: choice.message.role, text: texts.join('\n') }]
              }
            }
          }
          if (choice.text) return [{ text: choice.text }]
          if (choice.delta?.content)
            return [{ role: choice.delta.role, text: choice.delta.content }]
          return []
        })
        .filter((item: any) => item.text)
    }
  }

  // Fallback: return as plain text
  if (!includeJsonFallback && typeof parsed !== 'string') return []

  return [
    {
      text:
        typeof parsed === 'string' ? parsed : JSON.stringify(parsed, null, 2),
    },
  ]
}

function parsePayload(rawData: unknown): unknown {
  if (typeof rawData !== 'string') return rawData
  const trimmed = rawData.trim()
  if (!trimmed) return rawData
  if (trimmed[0] !== '{' && trimmed[0] !== '[') return rawData
  return safeJsonParse<unknown>(rawData, rawData)
}

function parseOptionalPayload(rawData: unknown): unknown {
  if (rawData === null || rawData === undefined || rawData === '') return null
  return parsePayload(rawData)
}

function timestampJson(timestamp: any) {
  if (!timestamp) return null
  return {
    display: formatTimestamp(timestamp),
    epochMs: timestampToMs(timestamp),
    seconds:
      timestamp.seconds === undefined
        ? undefined
        : typeof timestamp.seconds === 'bigint'
          ? safeBigIntToNumber(timestamp.seconds)
          : Number(timestamp.seconds || 0),
    nanos:
      timestamp.nanos === undefined
        ? undefined
        : typeof timestamp.nanos === 'bigint'
          ? safeBigIntToNumber(timestamp.nanos)
          : Number(timestamp.nanos || 0),
  }
}

function durationNumber(value: unknown): number {
  if (typeof value === 'bigint') return safeBigIntToNumber(value)
  return Number(value || 0)
}

function spanJson(span?: Span | null) {
  if (!span) return null
  return {
    spanId: span.spanId,
    parentSpanId: span.parentSpanId || null,
    traceId: span.traceId,
    name: span.spanName,
    kind: span.spanKind,
    status: span.statusCode,
    statusMessage: span.statusMessage || null,
    serviceName: span.serviceName,
    timestamp: timestampJson(span.timestamp),
    durationNs: durationNumber(span.duration),
    attributes: span.spanAttributes || {},
    resourceAttributes: span.resourceAttributes || {},
  }
}

function traceOverviewJson({
  summary,
  spans,
  rootSpan,
  primarySpan,
  inputRaw,
  outputRaw,
  isEmbedding,
}: {
  summary: TraceSummary
  spans: Span[]
  rootSpan?: Span
  primarySpan?: Span
  inputRaw: unknown
  outputRaw: unknown
  isEmbedding: boolean
}) {
  return {
    summary: {
      provider: summary.provider,
      model: summary.model,
      status: summary.status,
      correlationId: summary.correlationId || null,
      latencyNs: durationNumber(summary.latency),
      cost: summary.cost,
      tokens: summary.tokens,
      isEmbedding,
    },
    rootSpan: spanJson(rootSpan),
    primarySpan: spanJson(primarySpan),
    input: parseOptionalPayload(inputRaw),
    output: parseOptionalPayload(outputRaw),
    spanCount: spans.length,
    spans: spans.map(spanJson),
  }
}

function traceMetadataJson(summary: TraceSummary, spans: Span[]) {
  const root = spans[0]
  if (!root) return {}

  const requestedModel =
    getAttr(root, 'llm.request.model') || getAttr(root, 'model.requested')
  const servedModel =
    getAttr(root, 'llm.response.model') ||
    getAttr(root, 'model.resolved') ||
    getAttr(root, 'model.served')
  const tagsRaw = getAttr(root, 'trace.tags')
  const paramsRaw =
    getAttr(root, 'llm.request.model_parameters') ||
    getAttr(root, 'modelParameters')

  return {
    trace: {
      traceId: root.traceId,
      logId: summary.correlationId || null,
      userId:
        getAttr(root, 'user.id') || getAttr(root, 'trace.user_id') || null,
      sessionId:
        getAttr(root, 'session.id') ||
        getAttr(root, 'trace.session_id') ||
        null,
      tenantId: getAttr(root, 'tenant.id') || null,
      startTime: timestampJson(root.timestamp),
      durationNs: durationNumber(summary.latency),
      spanCount: spans.length,
      errorCount: spans.filter((s) => s.statusCode?.toUpperCase() === 'ERROR')
        .length,
    },
    modelResolution: {
      requestedModel: requestedModel || null,
      servedModel: servedModel || null,
      strategy:
        getAttr(root, 'resolution.strategy') ||
        getAttr(root, 'model.resolution_strategy') ||
        null,
      duration: getAttr(root, 'resolution.duration') || null,
      fallbackTriggered: getAttr(root, 'fallback.triggered') === 'true',
      fallbackAttempt: getAttr(root, 'fallback.attempt') || null,
      fallbackReason: getAttr(root, 'fallback.reason') || null,
    },
    environment: {
      deploymentEnvironment:
        getAttr(root, 'deployment.environment') ||
        root.resourceAttributes?.['deployment.environment'] ||
        null,
      serviceName:
        root.serviceName || root.resourceAttributes?.['service.name'] || null,
      tags: safeJsonParse<string[]>(tagsRaw, []),
    },
    customMetadata: parseOptionalPayload(getAttr(root, 'trace.metadata')),
    modelParameters: parseOptionalPayload(paramsRaw),
    rootSpan: spanJson(root),
  }
}

function traceTokensJson(summary: TraceSummary, spans: Span[]) {
  const providerSpan = spans.find(
    (span) =>
      span.spanName.startsWith('provider.') ||
      getAttr(span, 'observation.type') === 'GENERATION',
  )

  const promptDetailsRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.prompt_details')
    : null
  const completionDetailsRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.completion_details')
    : null
  const perMessageTokensRaw = providerSpan
    ? getAttr(providerSpan, 'llm.tokens.per_message')
    : null

  return {
    summary: summary.tokens,
    providerSpan: spanJson(providerSpan),
    breakdown: {
      cachedTokens: providerSpan
        ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.cached'))
        : 0,
      reasoningTokens: providerSpan
        ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.reasoning'))
        : 0,
      audioTokens: providerSpan
        ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.audio'))
        : 0,
      textTokens: providerSpan
        ? parseTokenCount(getAttr(providerSpan, 'llm.tokens.text'))
        : 0,
      promptDetails: parseOptionalPayload(promptDetailsRaw),
      completionDetails: parseOptionalPayload(completionDetailsRaw),
      perMessageTokens: safeJsonParse<number[]>(perMessageTokensRaw, []),
    },
  }
}

function traceCostJson(spans: Span[]) {
  const rows = spans
    .map((span) => {
      const cost = getSpanCostUSD(span)
      return {
        spanId: span.spanId,
        name: span.spanName,
        observationType: getAttr(span, 'observation.type') || null,
        cost,
      }
    })
    .filter((row) => row.cost > 0)
    .sort((a, b) => b.cost - a.cost)

  return {
    totalCost: rows.reduce((sum, row) => sum + row.cost, 0),
    rows,
  }
}

function spanIOJson({
  input,
  output,
  inputPayload,
  outputPayload,
  hasSuppressedTracePayload,
}: {
  input: unknown
  output: unknown
  inputPayload?: SpanIOPayload
  outputPayload?: SpanIOPayload
  hasSuppressedTracePayload: boolean
}) {
  return {
    input: inputPayload
      ? {
          key: inputPayload.key,
          scope: inputPayload.scope,
          value: parseOptionalPayload(input),
        }
      : null,
    output: outputPayload
      ? {
          key: outputPayload.key,
          scope: outputPayload.scope,
          value: parseOptionalPayload(output),
        }
      : null,
    hasSuppressedTracePayload,
  }
}

function spanEventsJson(
  events: Array<{
    name: string
    timestamp: any
    attributes?: Record<string, any>
  }>,
  guardrails: ReturnType<typeof summarizeGuardrails>,
) {
  return {
    guardrails,
    events: events.map((event) => ({
      ...event,
      timestamp: timestampJson(event.timestamp),
    })),
  }
}

function spanMetricsJson(span: Span, groupedAttributes: Record<string, any>) {
  const metricGroups = getSpanMetricGroups(span, groupedAttributes)

  return {
    summary: getSpanSummaryMetrics(span),
    streaming: getSpanStreamingMetrics(span),
    groups: Object.fromEntries(
      metricGroups
        .filter((group) => group.id !== 'summary')
        .map((group) => [group.id, group.metrics]),
    ),
  }
}

function getSpanStreamingMetrics(span: Span): Record<string, number> {
  const metrics = {
    chunkCount: Number(getAttr(span, 'llm.stream.chunk_count') || 0),
    timeToFirstTokenNs: Number(
      getAttr(span, 'llm.stream.time_to_first_token') || 0,
    ),
    tokensPerSecond: Number(getAttr(span, 'llm.stream.tokens_per_second') || 0),
    avgChunkSize: Number(getAttr(span, 'llm.stream.avg_chunk_size') || 0),
  }

  return Object.fromEntries(
    Object.entries(metrics).filter(([, value]) => value > 0),
  )
}

function getSpanSummaryMetrics(span: Span): Record<string, unknown> {
  const tokens = getSpanTokens(span)
  const costUsd = getSpanCostUSD(span)
  const summary: Record<string, unknown> = {
    'span.duration': span.duration,
  }

  if (tokens.input > 0) summary['tokens.input'] = tokens.input
  if (tokens.output > 0) summary['tokens.output'] = tokens.output
  if (tokens.cacheRead > 0) summary['tokens.cache_read'] = tokens.cacheRead
  if (tokens.cacheWrite > 0) {
    summary['tokens.cache_creation'] = tokens.cacheWrite
  }
  if (tokens.total > 0) summary['tokens.total'] = tokens.total
  if (costUsd > 0) summary['cost.total_usd'] = costUsd

  return summary
}

const SUMMARY_METRIC_PATTERNS = [
  /^llm\.tokens\./,
  /^gen_ai\.usage\./,
  /^llm\.token_count\./,
  /^(input|output|cache_read|cache_creation)_tokens$/,
  /^llm\.cost\.total$/,
  /^cost\.estimated_usd$/,
  /^cost_usd$/,
] as const

const METRIC_KEY_PATTERNS = [
  /cost|price|usd|spend/,
  /duration|latency|elapsed|ttft|time_to_first_token/,
  /tokens_per_second|throughput/,
  /chunk_count|avg_chunk_size/,
  /hit_rate|miss_rate|ratio|percent|percentage|rate$/,
  /count|total|used|remaining|limit|retry|attempt/,
  /cache\.(hit|miss|score|savings|ttl)/,
  /validation\.(passed|failed|errors|warnings)/,
] as const

const NON_METRIC_KEY_PATTERNS = [
  /model|provider|vendor|endpoint|url|method/,
  /prompt|message|messages|content|body|text|input|output/,
  /schema|metadata|name|id|type|status_message/,
] as const

type SpanMetricGroup = {
  id: string
  title: string
  icon: React.ElementType
  metrics: Record<string, unknown>
}

const SPAN_METRIC_GROUPS = [
  { id: 'llm', title: 'LLM Metrics', icon: Activity },
  { id: 'cache', title: 'Cache Metrics', icon: Database },
  { id: 'performance', title: 'Performance Metrics', icon: Zap },
  { id: 'cost', title: 'Cost Metrics', icon: DollarSign },
  { id: 'ratelimit', title: 'Rate Limit Metrics', icon: Gauge },
  { id: 'business', title: 'Business Metrics', icon: DollarSign },
  { id: 'validation', title: 'Validation Metrics', icon: Shield },
] as const

function getSpanMetricGroups(
  span: Span,
  groupedAttributes: Record<string, Record<string, unknown>>,
): SpanMetricGroup[] {
  const groups: SpanMetricGroup[] = [
    {
      id: 'summary',
      title: 'Span Summary',
      icon: Gauge,
      metrics: getSpanSummaryMetrics(span),
    },
  ]

  for (const group of SPAN_METRIC_GROUPS) {
    const metrics = filterMetricAttributes(groupedAttributes[group.id] || {})
    if (Object.keys(metrics).length > 0) {
      groups.push({
        ...group,
        metrics,
      })
    }
  }

  return groups
}

function filterMetricAttributes(
  attributes: Record<string, unknown>,
): Record<string, unknown> {
  return Object.fromEntries(
    sortAttributeKeys(Object.keys(attributes))
      .filter((key) => isMetricAttribute(key, attributes[key]))
      .map((key) => [key, attributes[key]]),
  )
}

function isMetricAttribute(key: string, value: unknown): boolean {
  const lowerKey = key.toLowerCase()
  if (lowerKey.startsWith('llm.stream.')) return false
  if (SUMMARY_METRIC_PATTERNS.some((pattern) => pattern.test(lowerKey))) {
    return false
  }

  const hasMetricName = METRIC_KEY_PATTERNS.some((pattern) =>
    pattern.test(lowerKey),
  )
  if (!hasMetricName) return false

  const lowerValue = typeof value === 'string' ? value.toLowerCase() : value
  const isBooleanLike =
    typeof value === 'boolean' ||
    lowerValue === 'true' ||
    lowerValue === 'false'
  const isNumberLike = metricNumberValue(value) !== null

  if (isBooleanLike || isNumberLike) return true

  return !NON_METRIC_KEY_PATTERNS.some((pattern) => pattern.test(lowerKey))
}

function spanResourcesJson(span: Span) {
  return {
    identity: {
      spanId: span.spanId,
      parentSpanId: span.parentSpanId || null,
      traceId: span.traceId,
      serviceName: span.serviceName,
      spanKind: span.spanKind,
    },
    resourceAttributes: span.resourceAttributes || {},
  }
}

function JsonTabPanel({
  data,
  className,
}: {
  data: unknown
  className?: string
}) {
  return (
    <div
      data-slot="trace-details-tab-panel"
      className={cn('h-full overflow-auto px-4 py-3 pb-6', className)}
    >
      <JsonViewer data={data} />
    </div>
  )
}

function shouldUsePromptBlock(text: string): boolean {
  return (
    text.length > 500 ||
    text.includes('\n') ||
    /<\/?[a-z][\w:-]*>/i.test(text) ||
    /^\s*[{[]/.test(text)
  )
}

function promptLineTone(line: string): string {
  const trimmed = line.trim()
  if (!trimmed) return 'text-brand-main-50 light:text-black'
  if (/^<\/?[a-z][\w:-]*(\s|>|$)/i.test(trimmed))
    return 'text-sky-300 light:text-sky-700'
  if (/^[}\]],?$/.test(trimmed) || /^[{[]/.test(trimmed))
    return 'text-violet-200 light:text-violet-700'
  if (/^"[^"]+"\s*:/.test(trimmed) || /^[a-zA-Z0-9_.-]+\s*:/.test(trimmed)) {
    return 'text-amber-200 light:text-amber-700'
  }
  if (/^[-*]\s+/.test(trimmed) || /^\d+\.\s+/.test(trimmed))
    return 'text-emerald-200 light:text-emerald-700'
  return 'text-zinc-200 light:text-zinc-800'
}

function PromptTextBlock({ text }: { text: string }) {
  const lines = text.split(/\r?\n/)
  const lineNumberWidth = Math.max(2, String(lines.length).length)

  return (
    <div className="overflow-hidden rounded border border-brand-main-600 bg-black/35 light:bg-white/60">
      <div className="max-h-[560px] overflow-auto p-0 text-[12px] leading-5">
        {lines.map((line, index) => (
          <div key={index} className="grid grid-cols-[auto_1fr] ">
            <span
              className="select-none border-r border-brand-main-700/80 bg-brand-main-950/60 px-2 text-right text-[10px] text-brand-main-50 light:bg-white/40 light:text-black"
              style={{ minWidth: `${lineNumberWidth + 2}ch` }}
            >
              {index + 1}
            </span>
            <span
              className={cn(
                'min-w-0 whitespace-pre-wrap break-words px-3',
                promptLineTone(line),
              )}
            >
              {line || ' '}
            </span>
          </div>
        ))}
      </div>
    </div>
  )
}

function IOViewer({
  title,
  icon: Icon,
  rawData,
  formattedText,
  viewMode,
  isEmbedding = false,
  allSpans,
  onSelectSpan,
  source,
}: {
  title: string
  icon: any
  rawData: any
  formattedText?: string
  viewMode: IOViewMode
  isEmbedding?: boolean
  allSpans?: Span[]
  onSelectSpan?: (spanId: string) => void
  source?: SpanIOPayload
}) {
  // For formatted view, use pre-extracted text or extract from raw data
  const formattedContent = formattedText
    ? [{ text: formattedText }]
    : extractFormattedContent(rawData, { includeJsonFallback: false })
  const hasContent =
    formattedContent.length > 0 && formattedContent.some((c) => c.text)
  const role = formattedContent.find((c) => c.role)?.role
  const roleColor = roleBadge(role)

  const parsedJson = parsePayload(rawData)

  // Helper to extract embedding summary from data
  const extractEmbeddingSummary = (data: any) => {
    if (!data) return null

    let parsed = data
    if (typeof data === 'string') {
      const parsedData = safeJsonParse<any>(data, null)
      if (parsedData === null) {
        // If it's already a formatted embedding summary string, just display it
        if (data.includes('dim') || data.includes('Embedding')) {
          return { formatted: data }
        }
        return null
      }
      parsed = parsedData
    }

    // Check if this is a formatted embedding summary string
    if (
      typeof parsed === 'string' &&
      (parsed.includes('dim') || parsed.includes('Embedding'))
    ) {
      return { formatted: parsed }
    }

    // Extract embedding metadata from response structure
    if (parsed.dimension || parsed.magnitude || parsed.hash) {
      return {
        dimension: parsed.dimension,
        magnitude: parsed.magnitude,
        isNormalized: parsed.is_normalized || parsed.isNormalized,
        hash: parsed.hash,
        minValue: parsed.min_value || parsed.minValue,
        maxValue: parsed.max_value || parsed.maxValue,
        meanValue: parsed.mean_value || parsed.meanValue,
      }
    }

    return null
  }

  const renderEmbeddingFormatted = () => {
    const summary = extractEmbeddingSummary(rawData)

    if (summary?.formatted) {
      return (
        <div className="text-sm text-zinc-200 bg-zinc-800/50 p-3 rounded">
          {summary.formatted}
        </div>
      )
    }

    if (summary) {
      return (
        <div className="space-y-2">
          <div className="grid grid-cols-2 gap-2 text-xs">
            <div className="bg-zinc-800/50 p-2 rounded">
              <span className="text-zinc-400">Dimension:</span>
              <span className="ml-2 text-zinc-200 ">
                {summary.dimension || 'N/A'}
              </span>
            </div>
            <div className="bg-zinc-800/50 p-2 rounded">
              <span className="text-zinc-400">Magnitude:</span>
              <span className="ml-2 text-zinc-200 ">
                {summary.magnitude || 'N/A'}
              </span>
            </div>
            {summary.isNormalized !== undefined && (
              <div className="bg-zinc-800/50 p-2 rounded">
                <span className="text-zinc-400">Normalized:</span>
                <span
                  className={cn(
                    'ml-2 ',
                    summary.isNormalized
                      ? statusTint.success.text
                      : statusTint.warning.text,
                  )}
                >
                  {summary.isNormalized ? 'Yes' : 'No'}
                </span>
              </div>
            )}
            {summary.hash && (
              <div className="bg-zinc-800/50 p-2 rounded">
                <span className="text-zinc-400">Hash:</span>
                <span className="ml-2 text-zinc-200 text-[10px]">
                  {summary.hash}
                </span>
              </div>
            )}
          </div>
          {(summary.minValue !== undefined ||
            summary.maxValue !== undefined) && (
            <div className="bg-zinc-800/30 p-2 rounded text-xs">
              <span className="text-zinc-400">Range:</span>
              <span className="ml-2 text-zinc-200 ">
                [{summary.minValue?.toFixed(4) ?? 'N/A'},{' '}
                {summary.maxValue?.toFixed(4) ?? 'N/A'}]
              </span>
              {summary.meanValue !== undefined && (
                <>
                  <span className="ml-4 text-zinc-400">Mean:</span>
                  <span className="ml-2 text-zinc-200 ">
                    {summary.meanValue?.toFixed(4)}
                  </span>
                </>
              )}
            </div>
          )}
        </div>
      )
    }

    // Fallback to displaying the raw content as text
    if (typeof rawData === 'string') {
      return (
        <div className="text-sm text-zinc-200 bg-zinc-800/50 p-3 rounded whitespace-pre-wrap">
          {rawData}
        </div>
      )
    }

    return (
      <span className="text-xs text-zinc-500">No embedding data captured</span>
    )
  }

  const renderFormatted = () => {
    // For embedding outputs, use special formatting
    if (isEmbedding && title.includes('Output')) {
      return renderEmbeddingFormatted()
    }

    // Structured conversation view (chat roles, tool calls linked to their
    // results, multimodal images) when the payload is message-shaped. Falls
    // back to the flat text view below.
    if (!formattedText && !isEmbedding && hasStructuredConversation(rawData)) {
      return (
        <ConversationView
          rawData={rawData}
          allSpans={allSpans}
          onSelectSpan={onSelectSpan}
        />
      )
    }

    if (hasContent) {
      return (
        <div className="space-y-3 h-full w-full">
          {formattedContent.map((item, idx) => (
            <div key={idx}>
              {shouldUsePromptBlock(item.text) ? (
                <PromptTextBlock text={item.text} />
              ) : (
                <div className="text-xs prose prose-sm w-full max-w-none prose-invert prose-p:m-0 prose-strong:text-brand-main-50 text-brand-main-50 light:text-brand-main-50">
                  <Markdown
                    remarkPlugins={REMARK_PLUGINS}
                    components={MARKDOWN_COMPONENTS}
                  >
                    {item.text}
                  </Markdown>
                </div>
              )}
            </div>
          ))}
        </div>
      )
    }

    if (
      !formattedText &&
      typeof parsedJson === 'object' &&
      parsedJson !== null
    ) {
      return <JsonViewer data={parsedJson} />
    }

    if (!hasContent) {
      return <span className="text-xs text-zinc-500">No content captured</span>
    }
  }

  const renderJson = () => {
    if (!rawData) {
      return <span className="text-xs text-zinc-500">No data captured</span>
    }

    // If it's an object, use JsonViewer for interactive display
    if (typeof parsedJson === 'object' && parsedJson !== null) {
      return <JsonViewer data={parsedJson} />
    }

    // Fallback for non-object data
    return (
      <pre className="text-xs prose text-zinc-300 whitespace-pre-wrap break-all">
        {String(rawData)}
      </pre>
    )
  }

  const sourceBadge = sourceLabel(source)

  return (
    <div
      data-slot="trace-details-frame"
      className="overflow-hidden rounded border border-brand-main-500 bg-brand-main-900/35 light:bg-white/70"
    >
      <div
        data-slot="trace-details-card-header"
        className="flex min-h-10 items-center gap-2 border-b border-brand-main-600 bg-brand-main-800/45 px-3 light:bg-white/60"
      >
        <Icon className="h-4 w-4 text-brand-main-200" />
        <span className="font-medium text-sm text-zinc-200 light:text-brand-main-50">
          {title}
        </span>
        {isEmbedding && title.includes('Output') && (
          <Badge
            variant="outline"
            className={cn(
              'text-[10px] uppercase tracking-wider px-1.5 py-0',
              statusBadge('neutral'),
            )}
          >
            vector
          </Badge>
        )}
        {role && !isEmbedding && (
          <Badge
            variant="outline"
            className={cn(
              'text-[10px] uppercase tracking-wider px-1.5 py-0',
              roleColor,
            )}
          >
            {role}
          </Badge>
        )}
        {sourceBadge && (
          <div className="ml-auto flex min-w-0 items-center gap-1.5">
            <Badge
              variant="outline"
              className={cn(
                'shrink-0 text-[10px] uppercase tracking-wider px-1.5 py-0',
                source?.scope === 'trace'
                  ? statusBadge('warning')
                  : statusBadge('neutral'),
              )}
            >
              {sourceBadge}
            </Badge>
            <span
              className="min-w-0 truncate text-[10px] text-brand-main-50 light:text-black"
              title={source?.key}
            >
              {source?.key}
            </span>
          </div>
        )}
      </div>
      <div data-slot="trace-details-card-body" className="overflow-auto p-3">
        {viewMode === 'formatted' ? renderFormatted() : renderJson()}
      </div>
    </div>
  )
}

// --- Error Panel ---

function ErrorDeepDivePanel({ span }: { span: Span }) {
  const statusMessage = span.statusMessage || getAttr(span, 'error.message')
  const errorType = getAttr(span, 'error.type')
  const errorRetryable = getAttr(span, 'error.retryable')
  const errorProvider = getAttr(span, 'error.provider')
  const errorCode =
    getAttr(span, 'error.code') || getAttr(span, 'http.status_code')

  if (span.statusCode !== 'ERROR' && span.statusCode?.toUpperCase() !== 'ERROR')
    return null
  if (!statusMessage && !errorType) return null

  // Error categories collapse to two severities — hard failures vs. transient
  // (retryable) — so colour signals "can this recover?" instead of painting a
  // rainbow of nearly-identical reds. The category name still shows in the label.
  const errorTypeStatus: Record<string, 'error' | 'warning' | 'info'> = {
    network: 'warning',
    auth: 'error',
    authentication: 'error',
    rate_limit: 'warning',
    'rate-limit': 'warning',
    timeout: 'warning',
    validation: 'info',
    server: 'error',
    client: 'warning',
  }

  const typeColorClass = errorType
    ? statusBadge(errorTypeStatus[errorType.toLowerCase()] ?? 'error')
    : ''

  return (
    <div
      className={cn(
        'rounded p-4 border',
        statusTint.error.bg,
        statusTint.error.border,
      )}
    >
      <div className="flex items-center gap-2 mb-3">
        <XCircle className={cn('h-4 w-4', statusTint.error.text)} />
        <span className={cn('font-medium text-sm', statusTint.error.text)}>
          Error Details
        </span>
      </div>
      {statusMessage && (
        <div
          className={cn(
            'rounded p-3 mb-3 border',
            statusTint.error.bg,
            statusTint.error.border,
          )}
        >
          <pre
            className={cn(
              'text-xs whitespace-pre-wrap break-all',
              statusTint.error.text,
            )}
          >
            {statusMessage}
          </pre>
        </div>
      )}
      <div className="flex flex-wrap items-center gap-2">
        {errorType && (
          <Badge
            variant="outline"
            className={cn(
              'text-[10px] uppercase tracking-wider gap-1',
              typeColorClass,
            )}
          >
            <AlertCircle className="h-3 w-3" />
            {errorType}
          </Badge>
        )}
        {errorCode && (
          <Badge
            variant="outline"
            className={cn('text-[10px] gap-1', statusBadge('error'))}
          >
            <Hash className="h-3 w-3" />
            {errorCode}
          </Badge>
        )}
        {errorRetryable !== undefined && errorRetryable !== null && (
          <Badge
            variant="outline"
            className={cn(
              'text-[10px] gap-1',
              errorRetryable === 'true'
                ? statusBadge('warning')
                : statusBadge('neutral'),
            )}
          >
            <RotateCcw className="h-3 w-3" />
            {errorRetryable === 'true' ? 'Retryable' : 'Non-retryable'}
          </Badge>
        )}
        {errorProvider && (
          <Badge
            variant="outline"
            className={cn('text-[10px] gap-1', statusBadge('neutral'))}
          >
            <Server className="h-3 w-3" />
            {errorProvider}
          </Badge>
        )}
      </div>
    </div>
  )
}

// --- Per-Span Cost Badge ---

function SpanCostBadges({ span }: { span: Span }) {
  const costInput = Number(getAttr(span, 'llm.cost.input') || 0)
  const costOutput = Number(getAttr(span, 'llm.cost.output') || 0)
  const costTotal = Number(getAttr(span, 'llm.cost.total') || 0)

  if (costTotal === 0 && costInput === 0 && costOutput === 0) return null

  return (
    <div className="flex items-center gap-2">
      {costTotal > 0 && (
        <Badge
          variant="outline"
          className={cn('text-[10px] gap-1', costBadgeCls)}
        >
          <DollarSign className="h-3 w-3" />
          {formatCost(costTotal)}
        </Badge>
      )}
      {costInput > 0 && (
        <Badge
          variant="outline"
          className={cn('text-[10px] gap-1', costBadgeCls)}
        >
          Input {formatCost(costInput)}
        </Badge>
      )}
      {costOutput > 0 && (
        <Badge
          variant="outline"
          className={cn('text-[10px] gap-1', costBadgeCls)}
        >
          Output {formatCost(costOutput)}
        </Badge>
      )}
    </div>
  )
}

// --- Streaming Metrics Card ---

function StreamingMetricsCard({ span }: { span: Span }) {
  const chunkCount = Number(getAttr(span, 'llm.stream.chunk_count') || 0)
  if (chunkCount === 0) return null

  const ttft = Number(getAttr(span, 'llm.stream.time_to_first_token') || 0)
  const tokensPerSec = Number(
    getAttr(span, 'llm.stream.tokens_per_second') || 0,
  )
  const avgChunkSize = Number(getAttr(span, 'llm.stream.avg_chunk_size') || 0)

  const ttftMs = ttft / 1_000_000 // convert ns to ms
  const ttftColor =
    ttftMs < 200
      ? statusTint.success.text
      : ttftMs < 500
        ? statusTint.warning.text
        : statusTint.error.text
  const ttftBg =
    ttftMs < 200
      ? statusTint.success.bar
      : ttftMs < 500
        ? statusTint.warning.bar
        : statusTint.error.bar

  return (
    <div className="bg-brand-main-600/20 rounded p-4 border border-brand-main-500">
      <div className="flex items-center gap-2 mb-3">
        <Radio className="h-4 w-4 text-brand-main-200" />
        <span className="font-medium text-sm text-zinc-200">
          Streaming Metrics
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3">
        {ttft > 0 && (
          <div>
            <span className="text-xs text-zinc-400">Time to First Token</span>
            <div className="flex items-center gap-2 mt-1">
              <div className="flex-1 h-2 bg-brand-main-700 rounded-full overflow-hidden">
                <div
                  className={cn('h-full rounded-full', ttftBg)}
                  style={{ width: `${Math.min((ttftMs / 1000) * 100, 100)}%` }}
                />
              </div>
              <span className={cn('text-xs ', ttftColor)}>
                {ttftMs.toFixed(0)}ms
              </span>
            </div>
          </div>
        )}
        {tokensPerSec > 0 && (
          <div>
            <span className="text-xs text-zinc-400">Throughput</span>
            <p className="text-sm text-zinc-200 mt-1">
              {tokensPerSec.toFixed(1)} tok/s
            </p>
          </div>
        )}
        <div>
          <span className="text-xs text-zinc-400">Chunks</span>
          <p className="text-sm text-zinc-200 mt-1">
            {chunkCount.toLocaleString()}
          </p>
        </div>
        {avgChunkSize > 0 && (
          <div>
            <span className="text-xs text-zinc-400">Avg Chunk Size</span>
            <p className="text-sm text-zinc-200 mt-1">
              {avgChunkSize.toFixed(1)} tokens
            </p>
          </div>
        )}
      </div>
    </div>
  )
}

// --- Cache Summary Card ---

function CacheSummaryCard({ spans }: { spans: Span[] }) {
  const cacheSpans = spans.filter(
    (s) =>
      s.spanName.startsWith('cache.') || getAttr(s, 'cache.hit') !== undefined,
  )
  if (cacheSpans.length === 0) return null

  const cacheHitSpan = cacheSpans.find(
    (s) => getAttr(s, 'cache.hit') === 'true',
  )
  const isHit = !!cacheHitSpan
  const cacheSpan = cacheHitSpan || cacheSpans[0]

  const cacheType =
    getAttr(cacheSpan, 'cache.type') ||
    getAttr(cacheSpan, 'cache.backend') ||
    'unknown'
  const latency = cacheSpan.duration
  const similarityScore = Number(
    getAttr(cacheSpan, 'cache.similarity_score') || 0,
  )
  const savings = Number(getAttr(cacheSpan, 'cache.savings') || 0)

  return (
    <div className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
      <div className="flex items-center gap-2 mb-3">
        <Database className="h-4 w-4 text-brand-main-200" />
        <span className="font-medium text-sm text-zinc-200">
          Cache Performance
        </span>
      </div>
      <div className="grid grid-cols-2 gap-3 text-xs">
        <div className="flex items-center gap-2">
          <span className="text-zinc-400">Status:</span>
          <Badge
            variant="outline"
            className={cn(
              'text-[10px] gap-1',
              isHit ? statusBadge('success') : statusBadge('warning'),
            )}
          >
            {isHit ? 'HIT' : 'MISS'}
          </Badge>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-zinc-400">Type:</span>
          <span className="text-zinc-200">{cacheType}</span>
        </div>
        <div className="flex items-center gap-2">
          <span className="text-zinc-400">Latency:</span>
          <span className="text-zinc-200">
            {formatDuration(latency)}
          </span>
        </div>
        {similarityScore > 0 && (
          <div className="flex items-center gap-2">
            <span className="text-zinc-400">Similarity:</span>
            <span className="text-zinc-200">
              {(similarityScore * 100).toFixed(1)}%
            </span>
          </div>
        )}
        {savings > 0 && (
          <div className="flex items-center gap-2">
            <span className="text-zinc-400">Savings:</span>
            <span className={cn('', statusTint.success.text)}>
              {formatCost(savings)}
            </span>
          </div>
        )}
      </div>
    </div>
  )
}

// --- Views ---

/**
 * Segmented Formatted / JSON view toggle, shared by the trace-level and
 * per-span I/O panels so the control is identical everywhere it appears.
 */
function IOViewModeToggle({
  mode,
  onChange,
}: {
  mode: IOViewMode
  onChange: (m: IOViewMode) => void
}) {
  return (
    <div className="flex shrink-0 items-center rounded border border-brand-main-600 bg-brand-main-800/70 p-0.5">
      {(['formatted', 'json'] as const).map((m) => (
        <button
          key={m}
          type="button"
          onClick={() => onChange(m)}
          className={cn(
            'rounded px-2 py-1 text-[11px] font-medium transition-colors',
            mode === m
              ? 'border border-brand-secondary-500/40 bg-brand-secondary-500/15 text-brand-secondary-200 light:text-brand-main-50'
              : 'border border-transparent text-brand-main-50 hover:bg-white/5 hover:text-brand-main-50 light:text-black light:hover:bg-black/5 light:hover:text-brand-main-50',
          )}
        >
          {m === 'json' ? 'JSON' : 'Formatted'}
        </button>
      ))}
    </div>
  )
}

function TraceExecutionSummary({
  spans,
  ioViewMode,
  onIOViewModeChange,
}: {
  spans: Span[]
  ioViewMode: IOViewMode
  onIOViewModeChange: (mode: IOViewMode) => void
}) {
  const summary = extractTraceSummary(spans)
  const navigate = Route.useNavigate()
  const traceId = spans[0]?.traceId ?? ''

  if (!summary) {
    return (
      <div className="flex flex-col items-center justify-center h-64 text-zinc-500 gap-2">
        <Activity className="size-8 opacity-20" />
        <span className="text-xs">No trace summary available</span>
      </div>
    )
  }

  const isError = summary.status?.toUpperCase() === 'ERROR'

  // Detect if this is an embedding trace (the main request, not internal cache operations)
  // Only check the root span (gateway.embeddings) to distinguish from chat completions that use semantic cache
  const rootSpan = spans.find(
    (s) => !s.parentSpanId || !spans.find((p) => p.spanId === s.parentSpanId),
  )
  const isEmbedding =
    rootSpan?.spanName === 'gateway.embeddings' ||
    (rootSpan && getAttr(rootSpan, 'gateway.command.type') === 'Embeddings')

  // Find the primary span for I/O extraction. Tool-call spans are GENERATION-
  // typed too but carry no message payload, so prefer the LAST generation span
  // that actually has I/O (the trace's real, final LLM call), then any provider
  // span, then the root. A primary span that resolved to a tool call used to
  // leave the output blank.
  const genSpans = spans.filter(
    (span) =>
      span.spanName.startsWith('provider.') ||
      getAttr(span, 'observation.type') === 'GENERATION',
  )
  const primarySpan =
    genSpans.filter((s) => getSpanOutput(s) || getSpanInput(s)).at(-1) ||
    genSpans.find((s) => s.spanName.startsWith('provider.')) ||
    genSpans[0] ||
    spans[0]

  // Prefer the full normalized message array (chat roles + tool calls) so the
  // conversation view can render it structurally; fall back to the root span so
  // roots that only carry the assembled trace.input/output still render.
  const inputRaw = getSpanInput(primarySpan) || getSpanInput(spans[0])

  // For embeddings, try the embedding output format first.
  const embeddingOutput = isEmbedding
    ? getAttr(primarySpan, 'embedding.output') ||
      getAttr(primarySpan, 'trace.output') ||
      getAttr(primarySpan, 'output')
    : null

  const outputRawJson =
    embeddingOutput || getSpanOutput(primarySpan) || getSpanOutput(spans[0])

  return (
    <TooltipProvider>
      <div
        data-slot="trace-details-view"
        className="w-full overflow-hidden flex flex-col h-full scrollbar-macos"
      >
        {/* Header */}
        <div
          data-slot="trace-details-header"
          className="shrink-0 border-b border-brand-main-500 px-4 py-3"
        >
          <div className="flex items-start justify-between gap-3">
            <div className="flex items-center gap-3">
              <div
                data-slot="trace-details-icon"
                className="flex items-center justify-center size-10 rounded bg-brand-main-600 border border-brand-main-500"
              >
                <ProviderDisplay
                  isActive={!isError}
                  providerName={summary.provider}
                />
              </div>
              <div>
                <h2
                  data-slot="trace-details-title"
                  className="text-lg font-semibold text-brand-main-50 flex items-center light:text-brand-main-50"
                >
                  {capitalize(summary.provider)}
                </h2>
                <p className="text-xs text-zinc-400 ">
                  {summary.model}
                </p>
              </div>
            </div>
            <div className="flex flex-wrap items-center justify-end gap-2">
              <IOViewModeToggle
                mode={ioViewMode}
                onChange={onIOViewModeChange}
              />
              {isEmbedding && (
                <Badge
                  variant="outline"
                  className={cn('gap-1', statusBadge('neutral'))}
                >
                  <Database className="h-3 w-3" />
                  Embedding
                </Badge>
              )}
              <Badge
                variant={isError ? 'destructive' : 'default'}
                className="gap-1"
              >
                <CheckCircle2 className="h-3 w-3" />
                {summary.status}
              </Badge>
              <Tooltip content="Compare with another trace">
                <Button asChild variant="outline">
                  <Link to="/observability/traces/compare" search={{ a: traceId }}>
                    <GitCompare className="h-4 w-4" />
                    Compare
                  </Link>
                </Button>
              </Tooltip>
            </div>
          </div>

          <div
            data-slot="trace-details-metric-grid"
            className="mt-3 grid grid-cols-2 gap-2 rounded border border-brand-main-500 bg-brand-main-600/10 p-3 md:grid-cols-4"
          >
            <div>
              <p className="text-xs text-zinc-400">Duration</p>
              <p className="text-sm text-zinc-200 flex items-center gap-1">
                <Clock className="h-3.5 w-3.5 text-brand-main-200" />
                {formatDuration(summary.latency)}
              </p>
            </div>
            <div>
              <p className="text-xs text-zinc-400">Total Tokens</p>
              <p className="text-sm text-zinc-200 flex items-center gap-1">
                <Zap className="h-3.5 w-3.5 text-brand-main-200" />
                {formatTokens(summary.tokens.totalTokens)}
              </p>
            </div>
            <div>
              <p className="text-xs text-zinc-400">Cost</p>
              <p className="text-sm text-zinc-200 flex items-center gap-1">
                <DollarSign className="h-3.5 w-3.5 text-brand-main-200" />
                {formatCost(summary.cost)}
              </p>
            </div>
            <div>
              <p className="text-xs text-zinc-400">Spans</p>
              <p className="text-sm text-zinc-200 flex items-center gap-1">
                <Layers className="h-3.5 w-3.5 text-brand-main-200" />
                {spans.length}
              </p>
            </div>
          </div>
        </div>

        {/* Tabs */}
        <Tabs
          defaultValue="io"
          className="flex-1 flex flex-col overflow-hidden"
        >
          <div
            data-slot="trace-details-tabs-bar"
            className="flex items-center justify-between px-3 border-b border-brand-main-500 py-2"
          >
            <TabsList className={TAB_LIST_CLASS}>
              {(
                [
                  { value: 'io', label: 'Overview', Icon: Layers },
                  { value: 'metadata', label: 'Metadata', Icon: Info },
                  { value: 'tokens', label: 'Token Details', Icon: Hash },
                  { value: 'cost', label: 'Cost', Icon: DollarSign },
                  { value: 'scores', label: 'Scores', Icon: Target },
                ] as const
              ).map(({ value, label, Icon }) => (
                <TabsTrigger
                  key={value}
                  value={value}
                  className={TAB_TRIGGER_CLASS}
                >
                  <Icon className="size-3.5" />
                  {label}
                </TabsTrigger>
              ))}
            </TabsList>
          </div>

          <div className="flex-1 overflow-hidden">
            <TabsContent value="io" className="m-0 h-full overflow-hidden">
              {ioViewMode === 'json' ? (
                <JsonTabPanel
                  className="px-3"
                  data={traceOverviewJson({
                    summary,
                    spans,
                    rootSpan,
                    primarySpan,
                    inputRaw,
                    outputRaw: outputRawJson,
                    isEmbedding: Boolean(isEmbedding),
                  })}
                />
              ) : (
                <div
                  data-slot="trace-details-tab-panel"
                  className="h-full space-y-3 overflow-auto px-3 py-3 pb-6"
                >
                  {isError && rootSpan && (
                    <ErrorDeepDivePanel span={rootSpan} />
                  )}
                  <IOViewer
                    title={isEmbedding ? 'Input Text' : 'Input'}
                    icon={MessageSquare}
                    rawData={inputRaw}
                    viewMode={ioViewMode}
                    allSpans={spans}
                    onSelectSpan={(spanId) =>
                      navigate({
                        search: (prev) => ({ ...prev, span: spanId }),
                      })
                    }
                  />
                  <IOViewer
                    title={isEmbedding ? 'Embedding Output' : 'Output'}
                    icon={isEmbedding ? Database : Sparkles}
                    rawData={outputRawJson}
                    viewMode={ioViewMode}
                    isEmbedding={isEmbedding}
                    allSpans={spans}
                    onSelectSpan={(spanId) =>
                      navigate({
                        search: (prev) => ({ ...prev, span: spanId }),
                      })
                    }
                  />
                  <AgentRunSummary spans={spans} />
                  <CacheSummaryCard spans={spans} />
                </div>
              )}
            </TabsContent>

            <TabsContent value="metadata" className="m-0 h-full overflow-auto">
              {ioViewMode === 'json' ? (
                <JsonTabPanel
                  className="px-3"
                  data={traceMetadataJson(summary, spans)}
                />
              ) : (
                <div
                  data-slot="trace-details-tab-panel"
                  className="space-y-3 px-3 py-3 pb-6"
                >
                  {/* Trace Info */}
                  <div className="bg-brand-main-600/10 rounded p-2 border border-brand-main-500">
                    <div className="flex items-center gap-2">
                      <Info className="h-4 w-4 text-brand-main-200" />
                      <span className="font-medium text-sm text-zinc-200">
                        Trace Information
                      </span>
                    </div>
                    <div className="space-y-0">
                      <AttributeRow
                        label="Trace ID"
                        value={spans[0]?.traceId || 'N/A'}
                      />
                      <AttributeRow
                        label="Log ID"
                        value={summary.correlationId || 'N/A'}
                      >
                        <Tooltip content="View Log">
                          <Button
                            variant="ghost"
                            size="icon"
                            className="p-0.5 size-6 hover:bg-brand-main-600 cursor-pointer"
                            onClick={() => {
                              navigate({
                                to: '/observability/logs',
                                search: (prev) => ({
                                  ...prev,
                                  log: summary.correlationId,
                                }),
                              })
                            }}
                          >
                            <ExternalLink size={10} className=" size-3" />
                          </Button>
                        </Tooltip>
                      </AttributeRow>
                      <AttributeRow
                        label="User ID"
                        value={
                          getAttr(spans[0], 'user.id') ||
                          getAttr(spans[0], 'trace.user_id') ||
                          'N/A'
                        }
                      />
                      <AttributeRow
                        label="Session ID"
                        value={
                          getAttr(spans[0], 'session.id') ||
                          getAttr(spans[0], 'trace.session_id') ||
                          'N/A'
                        }
                      />
                      <AttributeRow
                        label="Tenant ID"
                        value={getAttr(spans[0], 'tenant.id') || 'N/A'}
                      />
                      <AttributeRow
                        label="Start Time"
                        value={dayjs(
                          spans[0]?.timestamp
                            ? new Date(
                                (typeof spans[0].timestamp.seconds === 'bigint'
                                  ? safeBigIntToNumber(
                                      spans[0].timestamp.seconds,
                                    )
                                  : Number(spans[0].timestamp.seconds)) * 1000,
                              )
                            : new Date(),
                        ).format('MMM D, YYYY HH:mm:ss.SSS')}
                      />
                      {(() => {
                        // Calculate end time from max span end
                        const lastSpan = spans.reduce((latest, s) => {
                          if (!s.timestamp) return latest
                          const sEnd =
                            (typeof s.timestamp.seconds === 'bigint'
                              ? safeBigIntToNumber(s.timestamp.seconds)
                              : Number(s.timestamp.seconds || 0)) *
                              1000 +
                            Number(s.duration || 0) / 1_000_000
                          const latestEnd = latest
                            ? (typeof latest.timestamp!.seconds === 'bigint'
                                ? safeBigIntToNumber(latest.timestamp!.seconds)
                                : Number(latest.timestamp!.seconds || 0)) *
                                1000 +
                              Number(latest.duration || 0) / 1_000_000
                            : 0
                          return sEnd > latestEnd ? s : latest
                        }, spans[0])
                        if (lastSpan?.timestamp) {
                          const endMs =
                            (typeof lastSpan.timestamp.seconds === 'bigint'
                              ? safeBigIntToNumber(lastSpan.timestamp.seconds)
                              : Number(lastSpan.timestamp.seconds || 0)) *
                              1000 +
                            Number(lastSpan.duration || 0) / 1_000_000
                          return (
                            <AttributeRow
                              label="End Time"
                              value={dayjs(new Date(endMs)).format(
                                'MMM D, YYYY HH:mm:ss.SSS',
                              )}
                            />
                          )
                        }
                        return null
                      })()}
                      <AttributeRow
                        label="Duration"
                        value={formatDuration(summary.latency)}
                      />
                      <AttributeRow
                        label="Span Count"
                        value={String(spans.length)}
                      />
                      <AttributeRow
                        label="Error Count"
                        value={String(
                          spans.filter(
                            (s) => s.statusCode?.toUpperCase() === 'ERROR',
                          ).length,
                        )}
                      />
                    </div>
                  </div>

                  {/* Model Resolution */}
                  {(() => {
                    const root = spans[0]
                    const requestedModel =
                      getAttr(root, 'llm.request.model') ||
                      getAttr(root, 'model.requested')
                    const servedModel =
                      getAttr(root, 'llm.response.model') ||
                      getAttr(root, 'model.resolved') ||
                      getAttr(root, 'model.served')
                    const strategy =
                      getAttr(root, 'resolution.strategy') ||
                      getAttr(root, 'model.resolution_strategy')
                    const resolutionDuration = getAttr(
                      root,
                      'resolution.duration',
                    )
                    const fallbackTriggered =
                      getAttr(root, 'fallback.triggered') === 'true'
                    const fallbackAttempt = getAttr(root, 'fallback.attempt')
                    const fallbackReason = getAttr(root, 'fallback.reason')

                    if (!requestedModel && !servedModel) return null

                    return (
                      <div className="bg-brand-main-600/10 rounded p-2 border border-brand-main-500">
                        <div className="flex items-center gap-2">
                          <Layers className="h-4 w-4 text-brand-main-200" />
                          <span className="font-medium text-sm text-zinc-200">
                            Model Resolution
                          </span>
                        </div>
                        <div className="space-y-0">
                          {requestedModel &&
                          servedModel &&
                          requestedModel !== servedModel ? (
                            <div className="flex items-center justify-between gap-2 py-2 border-b border-brand-main-500">
                              <span className="text-xs font-medium text-zinc-400">
                                Resolution
                              </span>
                              <div className="flex items-center gap-2 text-xs ">
                                <span className={statusTint.neutral.text}>
                                  {requestedModel}
                                </span>
                                <ArrowRight className="h-3 w-3 text-zinc-500" />
                                <span className={statusTint.info.text}>
                                  {servedModel}
                                </span>
                              </div>
                            </div>
                          ) : (
                            <AttributeRow
                              label="Model"
                              value={servedModel || requestedModel || 'N/A'}
                            />
                          )}
                          {strategy && (
                            <div className="flex items-center justify-between gap-2 py-2 border-b border-brand-main-500 last:border-0">
                              <span className="text-xs font-medium text-zinc-400">
                                Strategy
                              </span>
                              <Badge
                                variant="outline"
                                className="text-[10px] text-zinc-300 border-zinc-500/30 bg-zinc-500/10"
                              >
                                {strategy}
                              </Badge>
                            </div>
                          )}
                          {resolutionDuration && (
                            <AttributeRow
                              label="Resolution Duration"
                              value={formatDuration(Number(resolutionDuration))}
                            />
                          )}
                          {fallbackTriggered && (
                            <>
                              <div className="flex items-center justify-between gap-2 py-2 border-b border-brand-main-500 last:border-0">
                                <span className="text-xs font-medium text-zinc-400">
                                  Fallback
                                </span>
                                <Badge
                                  variant="outline"
                                  className={cn(
                                    'text-[10px]',
                                    statusBadge('warning'),
                                  )}
                                >
                                  Triggered
                                </Badge>
                              </div>
                              {fallbackAttempt && (
                                <AttributeRow
                                  label="Attempt"
                                  value={fallbackAttempt}
                                />
                              )}
                              {fallbackReason && (
                                <AttributeRow
                                  label="Reason"
                                  value={fallbackReason}
                                />
                              )}
                            </>
                          )}
                        </div>
                      </div>
                    )
                  })()}

                  {/* Environment */}
                  {(() => {
                    const root = spans[0]
                    const environment =
                      getAttr(root, 'deployment.environment') ||
                      root.resourceAttributes?.['deployment.environment']
                    const serviceName =
                      root.serviceName ||
                      root.resourceAttributes?.['service.name']
                    const tagsRaw = getAttr(root, 'trace.tags')
                    const tags = safeJsonParse<string[]>(tagsRaw, [])

                    if (!environment && !serviceName && tags.length === 0)
                      return null

                    return (
                      <div className="bg-brand-main-600/10 rounded p-2 border border-brand-main-500">
                        <div className="flex items-center gap-2">
                          <Server className="h-4 w-4 text-brand-main-200" />
                          <span className="font-medium text-sm text-zinc-200">
                            Environment
                          </span>
                        </div>
                        <div className="space-y-0">
                          {environment && (
                            <AttributeRow
                              label="Environment"
                              value={environment}
                            />
                          )}
                          {serviceName && (
                            <AttributeRow label="Service" value={serviceName} />
                          )}
                          {tags.length > 0 && (
                            <div className="flex items-center justify-between gap-2 py-2 border-b border-brand-main-500 last:border-0">
                              <span className="text-xs font-medium text-zinc-400">
                                Tags
                              </span>
                              <div className="flex flex-wrap gap-1 justify-end">
                                {tags.map((tag, i) => (
                                  <Badge
                                    key={i}
                                    variant="outline"
                                    className="text-[10px] gap-1 text-zinc-300 border-zinc-500/30 bg-zinc-500/10"
                                  >
                                    <Tag className="h-2.5 w-2.5" />
                                    {tag}
                                  </Badge>
                                ))}
                              </div>
                            </div>
                          )}
                        </div>
                      </div>
                    )
                  })()}

                  {/* Custom Metadata */}
                  {(() => {
                    const root = spans[0]
                    const metadataRaw = getAttr(root, 'trace.metadata')
                    const metadata = safeJsonParse<Record<string, any>>(
                      metadataRaw,
                      {},
                    )

                    if (!metadata || Object.keys(metadata).length === 0)
                      return null

                    return (
                      <div className="bg-brand-main-600/10 rounded p-2 border border-brand-main-500">
                        <div className="flex items-center gap-2">
                          <Code className="h-4 w-4 text-brand-main-200" />
                          <span className="font-medium text-sm text-zinc-200">
                            Custom Metadata
                          </span>
                        </div>
                        <div className="space-y-0">
                          {Object.entries(metadata).map(([key, value]) => (
                            <AttributeRow
                              key={key}
                              label={key}
                              value={
                                typeof value === 'object'
                                  ? JSON.stringify(value)
                                  : String(value)
                              }
                            />
                          ))}
                        </div>
                      </div>
                    )
                  })()}

                  {/* Model Parameters */}
                  {(() => {
                    const root = spans[0]
                    const paramsRaw =
                      getAttr(root, 'llm.request.model_parameters') ||
                      getAttr(root, 'modelParameters')
                    const params = safeJsonParse<Record<string, any>>(
                      paramsRaw,
                      {},
                    )

                    if (!params || Object.keys(params).length === 0) return null

                    return (
                      <div className="bg-brand-main-600/10 rounded p-2 border border-brand-main-500">
                        <div className="flex items-center gap-2">
                          <Gauge className="h-4 w-4 text-brand-main-200" />
                          <span className="font-medium text-sm text-zinc-200">
                            Model Parameters
                          </span>
                        </div>
                        <div className="rounded py-2">
                          <JsonViewer data={params} />
                        </div>
                      </div>
                    )
                  })()}
                </div>
              )}
            </TabsContent>

            <TabsContent value="tokens" className="m-0 h-full overflow-auto">
              {ioViewMode === 'json' ? (
                <JsonTabPanel
                  className="px-3"
                  data={traceTokensJson(summary, spans)}
                />
              ) : (
                <div
                  data-slot="trace-details-tab-panel"
                  className="space-y-4 px-3 py-3 pb-6"
                >
                  {/* Token Summary */}
                  <div className="grid grid-cols-4 gap-2">
                    <div data-slot="trace-details-card" className="bg-brand-main-600/20 rounded px-4 py-2 border border-brand-main-500">
                      <span className="text-xs text-zinc-400">
                        Input Tokens
                      </span>
                      <p className="text-lg text-zinc-200">
                        {formatTokens(summary.tokens.inputTokens)}
                      </p>
                    </div>
                    <div data-slot="trace-details-card" className="bg-brand-main-600/20 rounded px-4 py-2 border border-brand-main-500">
                      <span className="text-xs text-zinc-400">
                        Output Tokens
                      </span>
                      <p className="text-lg text-zinc-200">
                        {formatTokens(summary.tokens.outputTokens)}
                      </p>
                    </div>
                    <div data-slot="trace-details-card" className="bg-brand-main-600/20 rounded px-4 py-2 border border-brand-main-500">
                      <span className="text-xs text-zinc-400">
                        Total Tokens
                      </span>
                      <p className="text-lg text-zinc-200">
                        {formatTokens(summary.tokens.totalTokens)}
                      </p>
                    </div>
                    <div data-slot="trace-details-card" className="bg-brand-main-600/20 rounded px-4 py-2 border border-brand-main-500">
                      <span className="text-xs text-zinc-400">
                        Estimated Cost
                      </span>
                      <p className="text-lg text-zinc-200 mt-1">
                        {formatCost(summary.cost)}
                      </p>
                    </div>
                  </div>

                  {/* Token Breakdown Visualization */}
                  <TokenBreakdownSection
                    spans={spans}
                    inputTokens={summary.tokens.inputTokens}
                    outputTokens={summary.tokens.outputTokens}
                  />
                </div>
              )}
            </TabsContent>

            <TabsContent value="cost" className="m-0 h-full overflow-auto">
              {ioViewMode === 'json' ? (
                <JsonTabPanel className="px-3" data={traceCostJson(spans)} />
              ) : (
                <div data-slot="trace-details-tab-panel" className="p-3 pb-6">
                  <TraceCostBreakdown
                    spans={spans}
                    onSelectSpan={(spanId) =>
                      navigate({
                        search: (prev) => ({ ...prev, span: spanId }),
                        replace: true,
                      })
                    }
                  />
                </div>
              )}
            </TabsContent>

            <TabsContent value="scores" className="m-0 h-full overflow-auto">
              <div data-slot="trace-details-tab-panel" className="p-4 pb-6">
                <ScoresPanel traceId={traceId} viewMode={ioViewMode} />
                <AnnotationsPanel traceId={traceId} viewMode={ioViewMode} />
              </div>
            </TabsContent>
          </div>
        </Tabs>
      </div>
    </TooltipProvider>
  )
}

// Helper to group attributes by prefix
function groupSpanAttributes(attributes: Record<string, any>) {
  const groups: Record<string, Record<string, any>> = {
    cache: {},
    model: {},
    request: {},
    response: {},
    llm: {},
    cost: {},
    performance: {},
    normalization: {},
    resolution: {},
    business: {},
    http: {},
    ratelimit: {},
    validation: {},
    other: {},
  }

  Object.entries(attributes || {}).forEach(([key, value]) => {
    const prefix = key.split('.')[0]
    if (groups[prefix]) {
      groups[prefix][key] = value
    } else {
      groups.other[key] = value
    }
  })

  return groups
}

function AttributeRow({
  label,
  value,
  onClick,
  children,
}: {
  label: string
  value: string
  onClick?: () => void
  children?: React.ReactNode
}) {
  const parsed =
    value.startsWith('{') || value.startsWith('[')
      ? safeJsonParse<unknown>(value, null)
      : null
  const showJson = parsed !== null && typeof parsed === 'object'

  return (
    <div
      className={cn(
        'flex gap-2 py-2 border-b border-brand-main-500 last:border-0',
        showJson ? 'items-start' : 'items-center justify-between',
      )}
      onClick={onClick}
    >
      <span className="text-xs font-medium text-zinc-400 shrink-0 pt-px">
        {label}
      </span>
      {showJson ? (
        <div className="flex-1 min-w-0 bg-brand-main-600/20 p-2 rounded overflow-x-auto">
          <JsonViewer data={parsed} collapsed showControls={false} />
        </div>
      ) : (
        <span className="text-xs whitespace-pre-wrap text-zinc-200 break-all text-right flex-1 min-w-0">
          {value}
        </span>
      )}
      {children}
    </div>
  )
}

const METRIC_NUMBER_FORMATTER = new Intl.NumberFormat(undefined, {
  maximumFractionDigits: 4,
})

function normalizeMetricKey(key: string): string {
  const parts = key.split('.')
  const displayKey = parts.length > 1 ? parts.slice(1).join('.') : key
  return formatAttributeName(displayKey)
}

function metricNumberValue(value: unknown): number | null {
  if (typeof value === 'number') {
    return Number.isFinite(value) ? value : null
  }
  if (typeof value === 'bigint') {
    return safeBigIntToNumber(value)
  }
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

function metricJsonValue(value: unknown): unknown | null {
  if (value && typeof value === 'object') return value
  if (typeof value !== 'string') return null

  const trimmed = value.trim()
  if (!trimmed.startsWith('{') && !trimmed.startsWith('[')) return null
  return safeJsonParse<unknown | null>(trimmed, null)
}

function metricBooleanValue(value: unknown): boolean | null {
  if (typeof value === 'boolean') return value
  if (typeof value !== 'string') return null

  const normalized = value.trim().toLowerCase()
  if (normalized === 'true') return true
  if (normalized === 'false') return false
  return null
}

function formatMetricNumber(key: string, value: unknown): string | null {
  const num = metricNumberValue(value)
  if (num == null) return null

  const lowerKey = key.toLowerCase()
  if (
    lowerKey.includes('cost') ||
    lowerKey.includes('price') ||
    lowerKey.includes('usd') ||
    lowerKey.includes('spend')
  ) {
    return formatCost(num)
  }

  if (
    lowerKey.includes('tokens_per_second') ||
    lowerKey.includes('throughput')
  ) {
    return `${METRIC_NUMBER_FORMATTER.format(num)} tok/s`
  }

  if (lowerKey.includes('token')) {
    return formatTokens(num)
  }

  if (
    lowerKey.includes('percent') ||
    lowerKey.includes('percentage') ||
    lowerKey.includes('ratio') ||
    lowerKey.includes('hit_rate')
  ) {
    const percent = Math.abs(num) <= 1 ? num * 100 : num
    return `${METRIC_NUMBER_FORMATTER.format(percent)}%`
  }

  if (
    lowerKey.includes('duration') ||
    lowerKey.includes('latency') ||
    lowerKey.includes('elapsed') ||
    lowerKey.includes('ttft') ||
    lowerKey.includes('time_to_first_token')
  ) {
    if (lowerKey.endsWith('_ms') || lowerKey.endsWith('.ms')) {
      return `${METRIC_NUMBER_FORMATTER.format(num)}ms`
    }
    if (lowerKey.endsWith('_s') || lowerKey.endsWith('.s')) {
      return `${METRIC_NUMBER_FORMATTER.format(num)}s`
    }
    return formatDuration(num)
  }

  return METRIC_NUMBER_FORMATTER.format(num)
}

function MetricValue({ attrKey, value }: { attrKey: string; value: unknown }) {
  const jsonValue = metricJsonValue(value)
  if (jsonValue !== null) {
    return (
      <div className="min-w-0 rounded border border-brand-main-500 bg-brand-main-900/40 p-2">
        <JsonViewer data={jsonValue} collapsed showControls={false} />
      </div>
    )
  }

  const booleanValue = metricBooleanValue(value)
  if (booleanValue !== null) {
    return (
      <span
        className={cn(
          'inline-flex items-center gap-1.5 rounded border px-2 py-1 text-[11px] font-medium',
          booleanValue
            ? 'border-emerald-500/25 bg-emerald-500/10 text-emerald-300'
            : 'border-zinc-500/25 bg-zinc-500/10 text-zinc-300',
        )}
      >
        {booleanValue ? (
          <CheckCircle2 className="size-3" />
        ) : (
          <XCircle className="size-3" />
        )}
        {booleanValue ? 'True' : 'False'}
      </span>
    )
  }

  if (value == null) {
    return (
      <span className="rounded border border-brand-main-500 bg-brand-main-600/20 px-2 py-1 text-[11px] text-zinc-500">
        null
      </span>
    )
  }

  const formattedNumber = formatMetricNumber(attrKey, value)
  if (formattedNumber) {
    return (
      <span className="inline-flex max-w-full items-center rounded border border-brand-main-400/30 bg-brand-main-500/20 px-2 py-1 text-[11px] font-medium text-zinc-100">
        {formattedNumber}
      </span>
    )
  }

  const text = String(value)
  if (text.length > 80 || text.includes('\n')) {
    return (
      <div className="max-h-32 min-w-0 overflow-auto rounded border border-brand-main-500 bg-brand-main-900/40 px-2.5 py-2 text-[11px] leading-5 text-zinc-200">
        {text}
      </div>
    )
  }

  return (
    <span className="inline-flex max-w-full rounded border border-brand-main-500 bg-brand-main-600/20 px-2 py-1 text-[11px] text-zinc-200">
      <span className="truncate">{text}</span>
    </span>
  )
}

function MetricsCard({
  title,
  icon: Icon,
  metrics,
}: {
  title: string
  icon: React.ElementType
  metrics: Record<string, unknown>
}) {
  const metricKeys = sortAttributeKeys(Object.keys(metrics))

  return (
    <div
      data-slot="trace-details-card"
      className="rounded border border-brand-main-500 bg-brand-main-600/20 p-3"
    >
      <div className="flex items-center gap-2 mb-3">
        <Icon className="h-4 w-4 text-brand-main-200" />
        <span className="font-medium text-sm text-zinc-200">{title}</span>
        <span className="rounded border border-brand-main-500 bg-brand-main-900/30 px-1.5 py-0.5 text-[10px] text-zinc-400">
          {metricKeys.length}
        </span>
      </div>
      <div className="overflow-hidden rounded border border-brand-main-500 bg-brand-main-900/25">
        {metricKeys.map((key) => (
          <div
            data-slot="trace-details-metric-row"
            key={key}
            className="grid gap-2 border-b border-brand-main-500 p-3 last:border-b-0 sm:grid-cols-[minmax(0,1fr)_minmax(220px,auto)] sm:items-start"
          >
            <div className="min-w-0">
              <div className="truncate text-xs font-medium text-zinc-200">
                {normalizeMetricKey(key)}
              </div>
              <div className="mt-0.5 truncate text-[10px] text-zinc-500">
                {key}
              </div>
            </div>
            <div className="min-w-0 sm:justify-self-end sm:text-right">
              <MetricValue attrKey={key} value={metrics[key]} />
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}

// Best-effort decode of a span attribute (JSON array/object or plain text)
// into a dataset-item field.
function attrToJsonObject(raw: string, key: string): JsonObject {
  const trimmed = raw.trim()
  if (trimmed.startsWith('[') || trimmed.startsWith('{')) {
    try {
      const parsed = JSON.parse(trimmed)
      if (Array.isArray(parsed)) return { [key]: parsed }
      if (parsed && typeof parsed === 'object') return parsed as JsonObject
    } catch {
      // fall through to plain text
    }
  }
  return { [key]: raw }
}

const GUARDRAIL_OUTCOME_STYLE: Record<
  GuardrailCheck['outcome'],
  { label: string; text: string; bg: string; border: string }
> = {
  pass: {
    label: 'Pass',
    text: 'text-emerald-300',
    bg: 'bg-emerald-500/10',
    border: 'border-emerald-500/25',
  },
  block: {
    label: 'Block',
    text: 'text-red-400',
    bg: 'bg-red-500/10',
    border: 'border-red-500/25',
  },
  flag: {
    label: 'Flag',
    text: 'text-amber-300',
    bg: 'bg-amber-500/10',
    border: 'border-amber-500/25',
  },
  unknown: {
    label: 'Ran',
    text: 'text-brand-main-50 light:text-black',
    bg: 'bg-brand-main-800/40',
    border: 'border-brand-main-600',
  },
}

/**
 * Safety summary above the raw event log: guardrail / policy / moderation
 * checks recorded as span events, shown as pass/block/flag rows with the rule
 * that fired and how long the check took.
 */
function GuardrailSummaryPanel({
  guardrails,
}: {
  guardrails: ReturnType<typeof summarizeGuardrails>
}) {
  return (
    <div
      className={cn(
        'mb-4 rounded border',
        guardrails.hasBlock
          ? 'border-red-500/30 bg-red-500/5'
          : 'border-brand-main-600 bg-brand-main-800/30',
      )}
    >
      <div className="flex items-center gap-2 px-3 py-2 border-b border-brand-main-700">
        <Shield
          className={cn(
            'h-4 w-4',
            guardrails.hasBlock ? 'text-red-400' : 'text-brand-secondary-300',
          )}
        />
        <span className="text-xs font-medium text-brand-main-50 light:text-brand-main-50">
          Guardrails
        </span>
        <span className="text-[11px] text-brand-main-50 light:text-black">
          {guardrails.passed} pass · {guardrails.blocked} block ·{' '}
          {guardrails.flagged} flag
        </span>
      </div>
      <div className="divide-y divide-brand-main-800/60">
        {guardrails.checks.map((check, i) => {
          const style = GUARDRAIL_OUTCOME_STYLE[check.outcome]
          return (
            <div key={i} className="flex items-center gap-3 px-3 py-2">
              <span
                className={cn(
                  'rounded border px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide',
                  style.text,
                  style.bg,
                  style.border,
                )}
              >
                {style.label}
              </span>
              <div className="min-w-0 flex-1">
                <div
                  className="truncate text-xs text-brand-main-50 light:text-brand-main-50"
                  title={check.name}
                >
                  {check.rule || check.name}
                </div>
                {check.detail && (
                  <div
                    className="truncate text-[11px] text-brand-main-50 light:text-black"
                    title={check.detail}
                  >
                    {check.detail}
                  </div>
                )}
              </div>
              {check.latencyMs != null && (
                <span className="text-[11px] text-brand-main-50 light:text-black">
                  {check.latencyMs}ms
                </span>
              )}
            </div>
          )
        })}
      </div>
    </div>
  )
}

// The canonical brand tab design, matching the agents sheet so tabs look the
// same across the app: a bordered segmented TabsList with brand-secondary pill
// triggers. Use these on every Tabs in this view.
const TAB_LIST_CLASS =
  'w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1'
const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 py-1 text-brand-secondary-100 transition-colors hover:text-brand-main-50 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 light:hover:text-brand-main-50'

// eventTone maps a span-event name to a semantic tint + icon. Color is a signal
// (error/warn/start/end), not decoration — everything else is neutral.
function eventTone(name: string): {
  tint: { text: string; bg: string; border: string }
  Icon: typeof Activity
} {
  const n = name.toLowerCase()
  if (n.includes('error') || n.includes('exception') || n.includes('fail'))
    return { tint: statusTint.error, Icon: AlertCircle }
  if (
    n.includes('warn') ||
    n.includes('block') ||
    n.includes('violation') ||
    n.includes('denied')
  )
    return { tint: statusTint.warning, Icon: AlertTriangle }
  if (
    n.includes('start') ||
    n.includes('begin') ||
    n.includes('issued') ||
    n.includes('request')
  )
    return { tint: statusTint.info, Icon: Play }
  if (
    n.includes('end') ||
    n.includes('complete') ||
    n.includes('received') ||
    n.includes('done') ||
    n.includes('success')
  )
    return { tint: statusTint.success, Icon: CheckCircle2 }
  return { tint: statusTint.neutral, Icon: Radio }
}

// SpanEventRow is one node on the events timeline. Self-contained so its expand
// state is a real component hook (the previous inline map() called useState per
// iteration, which violated the Rules of Hooks).
function SpanEventRow({
  event,
  spanStart,
}: {
  event: { name: string; timestamp: any; attributes?: Record<string, any> }
  spanStart: any
}) {
  const [open, setOpen] = useState(false)
  const { tint, Icon } = eventTone(event.name)
  const attrCount = event.attributes ? Object.keys(event.attributes).length : 0

  let rel = ''
  try {
    const d = timestampToMs(event.timestamp) - timestampToMs(spanStart)
    rel = d < 1000 ? `+${d.toFixed(0)}ms` : `+${(d / 1000).toFixed(2)}s`
  } catch {
    rel = ''
  }
  const abs = formatTimestamp(event.timestamp).split(' ')[1] ?? ''

  return (
    <div className="relative pl-7">
      {/* Node on the rail. The ring matches the panel background so the rail
          line reads as passing behind the node. */}
      <span
        aria-hidden
        className={cn(
          'absolute left-[7px] top-3 size-2.5 rounded-full border ring-4 ring-brand-main-900',
          tint.bg,
          tint.border,
        )}
      />
      <div
        className={cn(
          'rounded border bg-brand-main-800/40 transition-colors',
          'border-brand-main-500/70 hover:bg-brand-main-700/30',
        )}
      >
        <button
          type="button"
          disabled={attrCount === 0}
          onClick={() => setOpen((o) => !o)}
          className="flex w-full items-center gap-2.5 px-3 py-2 text-left disabled:cursor-default"
        >
          <Icon className={cn('size-3.5 shrink-0', tint.text)} />
          <span className="truncate text-[13px] font-medium text-zinc-100">
            {humanizeSpanName(event.name)}
          </span>
          <span className="ml-auto flex items-center gap-2.5 pl-2">
            {attrCount > 0 && (
              <span className="text-[10px] text-brand-main-50 light:text-black">
                {attrCount} attr
              </span>
            )}
            <span
              className={cn('text-[11px] tabular-nums', tint.text)}
            >
              {rel}
            </span>
            <span className="text-[10px] tabular-nums text-brand-main-50 light:text-black">
              {abs}
            </span>
            {attrCount > 0 && (
              <ChevronRight
                className={cn(
                  'size-3.5 text-brand-main-50 transition-transform light:text-black',
                  open && 'rotate-90',
                )}
              />
            )}
          </span>
        </button>
        {open && attrCount > 0 && (
          <div className="border-t border-brand-main-500/60 px-3 py-2">
            {Object.entries(event.attributes!).map(([k, v]) => (
              <AttributeDisplay key={k} attrKey={k} value={v} />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function SpanDetailView({
  span,
  ioViewMode,
  onIOViewModeChange,
}: {
  span: Span
  ioViewMode: IOViewMode
  onIOViewModeChange: (mode: IOViewMode) => void
}) {
  const navigate = Route.useNavigate()
  const [datasetPayload, setDatasetPayload] =
    useState<AddToDatasetPayload | null>(null)
  const [attrSearch, setAttrSearch] = useState('')

  const displayConfig = getSpanDisplayConfig(span)
  const isCached = getAttr(span, 'cache.hit') === 'true'

  // Group attributes
  const groupedAttributes = groupSpanAttributes(span.spanAttributes)
  const metricGroups = getSpanMetricGroups(span, groupedAttributes)
  const hasStreamingMetrics =
    Object.keys(getSpanStreamingMetrics(span)).length > 0

  // Extract events from span (will be available after proto regeneration)
  // For now, cast to any to access the events field that may exist
  const spanAny = span as any
  const events: Array<{
    name: string
    timestamp: any
    attributes?: Record<string, any>
  }> =
    spanAny.events?.map((e: any) => ({
      name: e.name,
      timestamp: e.timestamp,
      attributes: e.attributes,
    })) || []

  const guardrails = summarizeGuardrails(events as RawSpanEvent[])

  const handleBackToSummary = () => {
    navigate({
      search: (prev) => ({
        ...prev,
        span: undefined,
      }),
    })
  }

  // Span actions (compare / add-to-dataset / re-run), rendered inline on the
  // right of the tab row.
  const isGen =
    getAttr(span, 'observation.type') === 'GENERATION' ||
    span.spanName?.startsWith('provider.')
  const actionModel =
    getAttr(span, 'llm.model') || getAttr(span, 'gen_ai.response.model')
  // Span-level I/O, coalesced across native / OTel GenAI / OpenInference keys.
  // Root-like spans may show trace.input/output; child spans only show payloads
  // recorded on that span so the detail panel does not repeat the root data.
  const {
    input: spanInputPayload,
    output: spanOutputPayload,
    hasSuppressedTracePayload,
  } = getDisplayIOPayloads(span)
  const spanInput = spanInputPayload?.value || ''
  const spanOutput = spanOutputPayload?.value || ''
  const hasIO = !!(spanInput || spanOutput)
  const spanActions = (
    <div className="flex flex-wrap items-center justify-end gap-1.5">
      <Button asChild variant="outline">
        <Link to="/observability/traces/compare" search={{ a: span.traceId }}>
          <GitCompare className="h-4 w-4" />
          Compare trace
        </Link>
      </Button>
      {isGen && spanInput && (
        <Button
          variant="outline"
          onClick={() =>
            setDatasetPayload({
              input: attrToJsonObject(spanInput, 'messages'),
              expectedOutput: spanOutput
                ? attrToJsonObject(spanOutput, 'output')
                : undefined,
              metadata: actionModel ? { model: actionModel } : undefined,
              sourceTraceId: span.traceId,
              sourceObservationId: span.spanId,
            })
          }
        >
          <Database className="h-4 w-4" />
          Add to dataset
        </Button>
      )}
      {isGen && (
        <Button asChild variant="outline">
          <Link
            to="/evaluations/playground"
            search={{
              model: actionModel,
              user: spanInput,
              fromTrace: span.traceId,
              fromSpan: span.spanId,
            }}
          >
            <Play className="h-4 w-4" />
            Re-run in playground
          </Link>
        </Button>
      )}
    </div>
  )

  return (
    <div
      data-slot="trace-details-view"
      className="flex h-full min-h-0 w-full flex-col overflow-hidden"
    >
      {/* Header */}
      <div
        data-slot="trace-details-header"
        className="shrink-0 border-b border-brand-main-500 px-4 py-3"
      >
        <div className="flex items-start justify-between gap-3">
          <div className="flex items-center gap-3">
            <Button
              variant="ghost"
              size="icon"
              onClick={handleBackToSummary}
              className="h-8 w-8 hover:bg-brand-main-600"
              title="Back to Summary"
            >
              <ArrowLeft className="h-4 w-4" />
            </Button>
            {(() => {
              const colors = categoryColors[displayConfig.category]
              const isProvider = displayConfig.category === 'provider'
              return (
                <div
                  data-slot="trace-details-icon"
                  className="flex items-center justify-center size-10 rounded bg-brand-main-600 border border-brand-main-500"
                >
                  {isProvider && displayConfig.provider ? (
                    <ProviderDisplay
                      isActive={span.statusCode === 'Ok'}
                      providerName={displayConfig.provider}
                    />
                  ) : (
                    <Iconify.Icon
                      icon={categoryIcons[displayConfig.category]}
                      className={cn('h-5 w-5', colors.text)}
                    />
                  )}
                </div>
              )
            })()}
            <div>
              <h2
                data-slot="trace-details-title"
                className="text-lg font-semibold text-brand-main-50 flex items-center light:text-brand-main-50"
              >
                {displayConfig.title}
              </h2>
              {displayConfig.subtitle && (
                <p className="text-xs text-zinc-400 ">
                  {displayConfig.subtitle}
                </p>
              )}
            </div>
          </div>
          <div className="flex flex-wrap items-center justify-end gap-1.5">
            <IOViewModeToggle mode={ioViewMode} onChange={onIOViewModeChange} />
            <Badge
              variant="outline"
              className={cn(
                'gap-1',
                categoryColors[displayConfig.category].text,
                categoryColors[displayConfig.category].border,
                categoryColors[displayConfig.category].bg,
              )}
            >
              <Iconify.Icon
                icon={categoryIcons[displayConfig.category]}
                className="h-3 w-3"
              />
              {categoryLabels[displayConfig.category]}
            </Badge>
            <Badge
              variant={span.statusCode === 'Ok' ? 'default' : 'destructive'}
              className="gap-1"
            >
              <CheckCircle2 className="h-3 w-3" />
              {span.statusCode}
            </Badge>
            {isCached && (
              <Badge variant="secondary" className="gap-1">
                <Zap className="h-3 w-3" />
                Cached
              </Badge>
            )}
          </div>
        </div>

        <div
          data-slot="trace-details-metric-grid"
          className="mt-3 grid grid-cols-2 gap-2 rounded border border-brand-main-500 bg-brand-main-600/10 p-3 md:grid-cols-4"
        >
          <div>
            <p className="text-xs text-zinc-400">Duration</p>
            <p className="text-sm text-zinc-200 flex items-center gap-1">
              <Clock className="h-3.5 w-3.5 text-brand-main-200" />
              {formatDuration(span.duration)}
            </p>
          </div>
          <div>
            <p className="text-xs text-zinc-400">Tokens</p>
            <p className="text-sm text-zinc-200 flex items-center gap-1">
              <Zap className="h-3.5 w-3.5 text-brand-main-200" />
              {(() => {
                const count = getSpanTokens(span).total
                return count > 0 ? formatTokens(count) : '—'
              })()}
            </p>
          </div>
          <div>
            <p className="text-xs text-zinc-400">Cost</p>
            <p className="text-sm text-zinc-200 flex items-center gap-1">
              <DollarSign className="h-3.5 w-3.5 text-brand-main-200" />
              {(() => {
                const cost = getSpanCostUSD(span)
                return cost > 0 ? formatCost(cost) : '—'
              })()}
            </p>
          </div>
          <div>
            <p className="text-xs text-zinc-400">Category</p>
            <p className="text-sm text-zinc-200 flex items-center gap-1">
              <Iconify.Icon
                icon={categoryIcons[displayConfig.category]}
                className="h-3.5 w-3.5 text-brand-main-200"
              />
              {categoryLabels[displayConfig.category]}
            </p>
          </div>
        </div>

        <div data-slot="trace-details-summary-stack" className="mt-3 space-y-3">
          {/* Per-type span summary (M4-T1): command/url/query/results by category */}
          <SpanTypeSummaryCard span={span} category={displayConfig.category} />

          {/* Per-Span Cost Badges (P0.4) */}
          <SpanCostBadges span={span} />

          {/* Error Deep-Dive Panel (P0.1) */}
          <ErrorDeepDivePanel span={span} />
        </div>
      </div>

      <AddToDatasetDialog
        open={datasetPayload !== null}
        onOpenChange={(open) => !open && setDatasetPayload(null)}
        payload={datasetPayload}
        sourceLabel="trace span"
      />

      {/* Streaming playback (only shown when TTFT + per-token timings present) */}
      {isPlaybackable(span) && (
        <div data-slot="trace-details-inline-panel" className="shrink-0 px-4 pt-3">
          <GenerationPlayback span={span} />
        </div>
      )}

      {/* Token-level confidence highlight (only shown when logprobs are present) */}
      {isTokenHighlightable(span) && (
        <div data-slot="trace-details-inline-panel" className="shrink-0 px-4 pt-3">
          <TokenHighlight span={span} />
        </div>
      )}

      {/* Tabs */}
      <Tabs
        defaultValue={hasIO ? 'io' : 'attributes'}
        className="flex-1 flex flex-col overflow-hidden"
      >
        <div
          data-slot="trace-details-tabs-bar"
          className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-brand-main-500 px-4 py-2"
        >
          <TabsList className={TAB_LIST_CLASS}>
            {(
              [
                { value: 'io', label: 'Input / Output', Icon: MessageSquare },
                { value: 'attributes', label: 'Attributes', Icon: Tag },
                { value: 'events', label: 'Events', Icon: Activity },
                { value: 'metrics', label: 'Metrics', Icon: Gauge },
                { value: 'resources', label: 'Resources', Icon: Server },
              ] as const
            ).map(({ value, label, Icon }) => (
              <TabsTrigger
                key={value}
                value={value}
                className={TAB_TRIGGER_CLASS}
              >
                <Icon className="size-3.5" />
                {label}
              </TabsTrigger>
            ))}
          </TabsList>
          {spanActions}
        </div>

        <div className="flex-1 overflow-hidden">
          <TabsContent value="io" className="m-0 h-full overflow-auto">
            {ioViewMode === 'json' ? (
              <JsonTabPanel
                data={spanIOJson({
                  input: spanInput,
                  output: spanOutput,
                  inputPayload: spanInputPayload,
                  outputPayload: spanOutputPayload,
                  hasSuppressedTracePayload,
                })}
              />
            ) : (
              <div
                data-slot="trace-details-tab-panel"
                className="space-y-3 px-4 py-3 pb-6"
              >
                {hasIO ? (
                  <>
                    {hasSuppressedTracePayload && (
                      <div className="rounded border border-amber-500/20 bg-amber-500/5 px-2.5 py-1.5 text-[11px] text-amber-200 light:text-amber-700">
                        Trace-level payload omitted on this child span to avoid
                        repeating root I/O.
                      </div>
                    )}
                    {spanInput && (
                      <IOViewer
                        title="Input"
                        icon={MessageSquare}
                        rawData={spanInput}
                        viewMode={ioViewMode}
                        source={spanInputPayload}
                      />
                    )}
                    {spanOutput && (
                      <IOViewer
                        title={
                          displayConfig.category === 'embedding'
                            ? 'Embedding Output'
                            : 'Output'
                        }
                        icon={
                          displayConfig.category === 'embedding'
                            ? Database
                            : Sparkles
                        }
                        rawData={spanOutput}
                        viewMode={ioViewMode}
                        isEmbedding={displayConfig.category === 'embedding'}
                        source={spanOutputPayload}
                      />
                    )}
                  </>
                ) : (
                  <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
                    <div className="flex items-center justify-center size-10 rounded bg-brand-main-600 border border-brand-main-500">
                      <Iconify.Icon
                        icon={categoryIcons[displayConfig.category]}
                        className={cn(
                          'h-5 w-5',
                          categoryColors[displayConfig.category].text,
                        )}
                      />
                    </div>
                    <p className="text-sm text-zinc-300">
                      {hasSuppressedTracePayload
                        ? 'No span-local input/output captured'
                        : `No message input/output on this ${categoryLabels[displayConfig.category].toLowerCase()} span`}
                    </p>
                    <p className="max-w-xs text-xs text-brand-main-50 light:text-black">
                      {hasSuppressedTracePayload
                        ? 'This span only has the trace-level payload, so it is hidden here to avoid repeating the root request and response. Check the trace summary or root span for that payload.'
                        : `${categoryLabels[displayConfig.category]} spans record their work as attributes and events. See the summary above, or the Attributes and Events tabs.`}
                    </p>
                  </div>
                )}
              </div>
            )}
          </TabsContent>

          <TabsContent
            value="attributes"
            className="m-0 h-full overflow-hidden"
          >
            {ioViewMode === 'json' ? (
              <JsonTabPanel data={span.spanAttributes || {}} />
            ) : (
              <div
                data-slot="trace-details-tab-panel"
                className="h-full overflow-auto px-4 pb-6"
              >
                {/* Filter attributes by key or value for fast scanning. */}
                <div className="sticky top-0 z-10 -mx-4 bg-brand-main-900/95 px-4 py-2 backdrop-blur light:bg-background/95">
                  <div className="relative">
                    <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-brand-main-50 light:text-black" />
                    <input
                      value={attrSearch}
                      onChange={(e) => setAttrSearch(e.target.value)}
                      placeholder="Filter attributes..."
                      className="h-8 w-full rounded border border-brand-main-500 bg-brand-main-700 pl-8 pr-3 text-xs text-zinc-200 placeholder:text-brand-main-50 focus:border-brand-secondary-500 focus:outline-none light:placeholder:text-black"
                    />
                  </div>
                </div>
                {(() => {
                  const q = attrSearch.trim().toLowerCase()
                  const improvedGroups = groupAttributesByPrefix(
                    span.spanAttributes,
                  )

                  // Filter each group's attrs by key or value when searching.
                  const groups = Array.from(improvedGroups.entries())
                    .map(([groupName, attrs]) => {
                      const entries = Object.entries(attrs).filter(
                        ([key, value]) =>
                          !q ||
                          key.toLowerCase().includes(q) ||
                          String(value).toLowerCase().includes(q),
                      )
                      return [groupName, entries] as const
                    })
                    .filter(([, entries]) => entries.length > 0)

                  if (groups.length === 0) {
                    return (
                      <div className="flex h-32 flex-col items-center justify-center gap-2 text-brand-main-50 light:text-black">
                        <Search className="size-5 text-brand-main-50 light:text-black" />
                        <span className="text-xs">
                          No attributes match "{attrSearch}"
                        </span>
                      </div>
                    )
                  }

                  return (
                    <Accordion
                      type="multiple"
                      // When searching, expand every matching group so hits are visible.
                      value={q ? groups.map(([g]) => g) : undefined}
                      defaultValue={['LLM', 'Gen AI', 'HTTP', 'General']}
                      className="space-y-2.5 pb-3 pt-2"
                    >
                      {groups.map(([groupName, entries]) => {
                        const description = getGroupDescription(groupName)
                        const keys = sortAttributeKeys(entries.map(([k]) => k))
                        const byKey = Object.fromEntries(entries)

                        return (
                          <AccordionItem
                            key={groupName}
                            value={groupName}
                            className="overflow-hidden rounded border border-brand-main-500 bg-brand-main-600/10 last:border-b"
                          >
                            <AccordionTrigger className="px-4 py-2.5 hover:no-underline data-[state=open]:border-b data-[state=open]:border-brand-main-500/60">
                              <div className="flex w-full items-center gap-2">
                                <span className="font-medium text-sm text-zinc-200">
                                  {groupName}
                                </span>
                                <Badge
                                  variant="outline"
                                  className="text-[10px] py-0 px-1.5 bg-brand-main-600/20 text-brand-main-50 border-brand-main-500 light:text-black"
                                >
                                  {entries.length}
                                </Badge>
                                {description && (
                                  <span className="truncate text-xs text-brand-main-50 light:text-black">
                                    {description}
                                  </span>
                                )}
                              </div>
                            </AccordionTrigger>
                            <AccordionContent className="px-4 py-2">
                              <div>
                                {keys.map((key) => (
                                  <AttributeDisplay
                                    key={key}
                                    attrKey={key}
                                    value={byKey[key]}
                                  />
                                ))}
                              </div>
                            </AccordionContent>
                          </AccordionItem>
                        )
                      })}
                    </Accordion>
                  )
                })()}
              </div>
            )}
          </TabsContent>

          <TabsContent value="events" className="m-0 h-full overflow-auto">
            {ioViewMode === 'json' ? (
              <JsonTabPanel data={spanEventsJson(events, guardrails)} />
            ) : (
              <div data-slot="trace-details-tab-panel" className="px-4 py-3 pb-6">
                {guardrails.checks.length > 0 && (
                  <GuardrailSummaryPanel guardrails={guardrails} />
                )}
                {events.length > 0 ? (
                  <div className="relative space-y-2 pt-1">
                    {/* Continuous rail behind the nodes. */}
                    <span
                      aria-hidden
                      className="absolute left-[12px] top-4 bottom-3 w-px bg-brand-main-500/50"
                    />
                    {events.map((event, i) => (
                      <SpanEventRow
                        key={i}
                        event={event}
                        spanStart={span.timestamp}
                      />
                    ))}
                  </div>
                ) : (
                  <div className="flex h-40 flex-col items-center justify-center gap-2.5">
                    <div className="flex size-11 items-center justify-center rounded-xl border border-brand-main-500 bg-brand-main-600/20">
                      <Activity className="size-5 text-brand-main-50 light:text-black" />
                    </div>
                    <span className="text-xs text-brand-main-50 light:text-black">
                      No events on this span
                    </span>
                  </div>
                )}
              </div>
            )}
          </TabsContent>

          <TabsContent value="metrics" className="m-0 h-full overflow-auto">
            {ioViewMode === 'json' ? (
              <JsonTabPanel data={spanMetricsJson(span, groupedAttributes)} />
            ) : (
              <div data-slot="trace-details-tab-panel" className="px-4 py-3 pb-6">
                <div className="grid gap-3">
                  {metricGroups.map((group) => (
                    <MetricsCard
                      key={group.id}
                      title={group.title}
                      icon={group.icon}
                      metrics={group.metrics}
                    />
                  ))}
                  <StreamingMetricsCard span={span} />
                  {!hasStreamingMetrics && metricGroups.length === 0 && (
                    <div className="flex flex-col items-center justify-center h-32 text-zinc-500 gap-2">
                      <Activity className="size-8 opacity-20" />
                      <span className="text-xs">
                        No metrics recorded for this span
                      </span>
                    </div>
                  )}
                </div>
              </div>
            )}
          </TabsContent>

          <TabsContent value="resources" className="m-0 h-full overflow-auto">
            {ioViewMode === 'json' ? (
              <JsonTabPanel data={spanResourcesJson(span)} />
            ) : (
              <div
                data-slot="trace-details-tab-panel"
                className="space-y-4 px-4 py-3 pb-6"
              >
                {/* Span Identity */}
                <div data-slot="trace-details-card" className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
                  <h4 className="text-xs font-medium text-zinc-400 mb-3">
                    Span Identity
                  </h4>
                  <AttributeRow label="Span ID" value={span.spanId} />
                  <AttributeRow
                    label="Parent Span ID"
                    value={span.parentSpanId || '—'}
                  />
                  <AttributeRow label="Service Name" value={span.serviceName} />
                  <AttributeRow label="Span Kind" value={span.spanKind} />
                </div>
                {/* Resource Attributes */}
                {Object.keys(span.resourceAttributes || {}).length > 0 && (
                  <div data-slot="trace-details-card" className="bg-brand-main-600/10 rounded p-4 border border-brand-main-500">
                    <h4 className="text-xs font-medium text-zinc-400 mb-3">
                      Resource Attributes
                    </h4>
                    {Object.entries(span.resourceAttributes || {}).map(
                      ([key, value]) => (
                        <AttributeRow
                          key={key}
                          label={key}
                          value={safeToString(value)}
                        />
                      ),
                    )}
                  </div>
                )}
              </div>
            )}
          </TabsContent>
        </div>
      </Tabs>
    </div>
  )
}

export function TraceOverview({ selectedSpan, allSpans }: TraceOverviewProps) {
  const [ioViewMode, setIoViewMode] = useState<IOViewMode>('formatted')

  if (selectedSpan) {
    return (
      <SpanDetailView
        key={selectedSpan.spanId}
        span={selectedSpan}
        ioViewMode={ioViewMode}
        onIOViewModeChange={setIoViewMode}
      />
    )
  }

  // When no span is selected, show the trace execution summary
  // We need to filter spans to find the relevant ones for the summary if needed,
  // but TraceExecutionSummary handles extraction from the full list.
  return (
    <TraceExecutionSummary
      spans={allSpans || []}
      ioViewMode={ioViewMode}
      onIOViewModeChange={setIoViewMode}
    />
  )
}
