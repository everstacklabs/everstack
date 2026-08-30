import { useMemo, useState } from 'react'
import {
  Copy,
  RotateCcw,
  Share2,
  ThumbsDown,
  ThumbsUp,
  Link as LinkIcon,
  Paperclip,
  Download,
  MessageCircleQuestion,
  Clock,
  Info,
  Brain,
  ChevronDown,
} from 'lucide-react'
import type { AgentSessionTurn } from '@/server/agents'
import { safeBigIntToNumber } from '@/utils/trace-formatters'
import { ToolCallCard, SANDBOX_TOOLS } from './tool-call-card'
import { AgentMarkdown } from './agent-markdown'
import { MentionText } from './mention-text'
import type { ToolResultCacheEntry } from './session-timeline'
import { ui } from '@everstack/ui'

const IMAGE_EXTENSIONS = /\.(png|jpe?g|gif|webp|svg|bmp|ico)(\?|$)/i

interface ToolCall {
  id: string
  type: string
  function: {
    name: string
    arguments: string
  }
  /** Persisted result fields (enriched by backend checkpoint) */
  result?: string
  success?: boolean
  duration_ms?: number
  sandbox_parent_duration_ms?: number
}

/**
 * An ordered slice of a persisted turn's body. A turn is reconstructed as a
 * sequence of these: contiguous runs of regular tool calls (`tools`) and the
 * HITL question/answer exchanges (`hitl`) that occurred between them, in true
 * execution order.
 */
type BodySegment =
  | { kind: 'tools'; calls: ToolCall[] }
  | { kind: 'reasoning'; content: string }
  | {
      kind: 'hitl'
      id: string
      question: string
      response: string
      cancelled: boolean
    }

/**
 * One entry of the backend-persisted turn timeline (turn.timeline). Mirrors the
 * Go timelineItem shape: a "text" narration/response segment, a "tool" call, or
 * a "hitl" ask_user exchange — in true execution order.
 */
type TimelineItem =
  | { t: 'text'; content?: string }
  | {
      t: 'tool'
      id?: string
      name?: string
      args?: string
      result?: string
      success?: boolean
      duration_ms?: number
      sandbox_parent_duration_ms?: number
    }
  | {
      t: 'hitl'
      id?: string
      question?: string
      response?: string
      cancelled?: boolean
    }

// The backend bakes an LLM-steering instruction into the ask_user tool result
// (see internal/agents/runtime/tools/ask_user.go) so the model keeps working
// after collecting input. That text must never reach the transcript — the live
// view already shows the raw answer (from the user_input.received event), so we
// strip the wrapper here to keep the reloaded HITL bubble in sync. Returns the
// input untouched when no wrapper is present (already-clean answers).
const ASK_USER_ANSWER_PREFIX = 'User answered: '
const ASK_USER_CONTINUATION_MARKER =
  '\n\nContinue the task immediately using this information.'

export function stripAskUserWrapper(raw: string): string {
  if (!raw) return raw
  let text = raw
  const markerIdx = text.indexOf(ASK_USER_CONTINUATION_MARKER)
  if (markerIdx !== -1) text = text.slice(0, markerIdx)
  if (text.startsWith(ASK_USER_ANSWER_PREFIX))
    text = text.slice(ASK_USER_ANSWER_PREFIX.length)
  return text.trim()
}

interface TurnCardProps {
  turn: AgentSessionTurn
  agentId?: string
  sessionId?: string
  /** Cached tool results from streaming events so execution output survives turn persistence */
  toolResultsCache?: Record<string, ToolResultCacheEntry>
  /** When true, sandbox tool calls are hidden from inline rendering (shown in the sandbox sheet instead) */
  hideSandboxTools?: boolean
  /** Tool call rendering mode. summary collapses calls behind a compact header. */
  toolCallView?: 'full' | 'summary'
  /** In summary mode, show only failed calls until expanded. */
  showFailedToolCallsOnly?: boolean
  onCopy?: (turn: AgentSessionTurn) => void
  onRetry?: (turn: AgentSessionTurn) => void
  onShare?: (turn: AgentSessionTurn) => void
  onFeedback?: (turnId: string, value: 'up' | 'down') => void
  feedbackValue?: 'up' | 'down' | null
  disableActions?: boolean
  /** Model that processed this turn (from stream events) */
  model?: string
}

function formatToolBreakdown(toolNames: string[]): string {
  if (toolNames.length === 0) return ''
  const counts = new Map<string, number>()
  for (const name of toolNames) {
    const normalizedName =
      typeof name === 'string' ? name : String(name ?? 'unknown')
    counts.set(normalizedName, (counts.get(normalizedName) ?? 0) + 1)
  }
  const entries = Array.from(counts.entries()).sort((a, b) => {
    if (b[1] !== a[1]) return b[1] - a[1]
    return String(a[0] ?? '').localeCompare(String(b[0] ?? ''))
  })
  const top = entries.slice(0, 3).map(([name, count]) => `${name} x${count}`)
  const extra = entries.length - top.length
  return extra > 0 ? `${top.join(', ')} +${extra} more` : top.join(', ')
}

const { Tooltip } = ui

export interface ParsedAttachment {
  name: string
  url: string
  isImage: boolean
}

export function parseAttachedFiles(userInput: string): {
  message: string
  attachments: ParsedAttachment[]
} {
  const marker = 'Attached files:\n'
  const idx = userInput.indexOf(marker)
  if (idx === -1) return { message: userInput, attachments: [] }

  const message = userInput.slice(0, idx).trimEnd()
  const block = userInput.slice(idx + marker.length)
  const attachments: ParsedAttachment[] = []
  const re = /\[([^\]]+)\]\(([^)]+)\)/g
  let m: RegExpExecArray | null
  while ((m = re.exec(block)) !== null) {
    const name = m[1]
    const url = m[2]
    attachments.push({
      name,
      url,
      isImage: IMAGE_EXTENSIONS.test(url) || IMAGE_EXTENSIONS.test(name),
    })
  }
  return { message, attachments }
}

export function AttachedFilesPreview({
  attachments,
}: {
  attachments: ParsedAttachment[]
}) {
  if (attachments.length === 0) return null
  const images = attachments.filter((a) => a.isImage)
  const files = attachments.filter((a) => !a.isImage)
  return (
    <div className="mt-2 space-y-2">
      {images.length > 0 && (
        <div className="flex flex-wrap gap-2">
          {images.map((att) => (
            <a
              key={att.url}
              href={att.url}
              target="_blank"
              rel="noopener noreferrer"
              className="block group relative rounded-lg overflow-hidden border border-brand-secondary-500/20 hover:border-brand-secondary-400/40 transition-colors"
            >
              <img
                src={att.url}
                alt={att.name}
                className="max-w-[240px] max-h-[180px] object-contain rounded-lg"
                loading="lazy"
              />
              <div className="absolute bottom-0 inset-x-0 bg-gradient-to-t from-black/60 to-transparent px-2 py-1 opacity-0 group-hover:opacity-100 transition-opacity">
                <span className="text-[10px] text-white/80 truncate block light:text-black/80">
                  {att.name}
                </span>
              </div>
            </a>
          ))}
        </div>
      )}
      {files.length > 0 && (
        <div className="flex flex-wrap gap-1.5">
          {files.map((att) => (
            <a
              key={att.url}
              href={att.url}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 rounded-md bg-brand-secondary-600/10 border border-brand-secondary-500/20 px-2.5 py-1 text-xs text-brand-secondary-300 hover:bg-brand-secondary-600/20 hover:border-brand-secondary-400/30 transition-colors"
            >
              <Paperclip className="w-3 h-3 text-brand-secondary-400/60" />
              <span className="truncate max-w-[160px]">{att.name}</span>
              <Download className="w-3 h-3 text-brand-secondary-400/40" />
            </a>
          ))}
        </div>
      )}
    </div>
  )
}

function parseSearchSources(
  text: string,
): Array<{ title: string; url: string; description: string }> {
  const sources: Array<{ title: string; url: string; description: string }> = []
  const re =
    /\d+\.\s+\*\*(.+?)\*\*\s+\((\S+?)\)\n\s+(.+?)(?=\n\n|\n\d+\.|\s*$)/gs
  let m: RegExpExecArray | null
  while ((m = re.exec(text)) !== null) {
    sources.push({ title: m[1], url: m[2], description: m[3].trim() })
  }
  return sources
}

function normalizeUrl(raw: string): string {
  return raw.replace(/[),.;!?]+$/g,'')
}

function extractLinksFromMarkdown(text: string): string[] {
  const urls = new Set<string>()
  const markdownLink = /\[[^\]]*\]\((https?:\/\/[^)\s]+)\)/g
  const bareUrl = /https?:\/\/[^)\s]+/g
  let match: RegExpExecArray | null
  while ((match = markdownLink.exec(text)) !== null) {
    urls.add(normalizeUrl(match[1]))
  }
  while ((match = bareUrl.exec(text)) !== null) {
    urls.add(normalizeUrl(match[0]))
  }
  return Array.from(urls)
}

function extractDomain(url: string): string {
  try {
    return new URL(url).hostname.replace(/^www\./, '')
  } catch {
    return url
  }
}

export function TurnCard({
  turn,
  agentId,
  sessionId,
  toolResultsCache,
  hideSandboxTools,
  toolCallView = 'full',
  showFailedToolCallsOnly = false,
  onCopy,
  onRetry,
  onShare,
  onFeedback,
  feedbackValue = null,
  disableActions = false,
  model,
}: TurnCardProps) {
  const [toolCallDetailsOverride, setToolCallDetailsOverride] = useState<
    'auto' | 'open' | 'closed'
  >('auto')

  // The backend persists tool calls in execution order — it iterates the
  // turn's messages in sequence and appends each call as it runs, with the
  // ask_user (HITL) entries sitting at their true positions (see
  // checkpoint.go). The objects carry no reliable sequence/timestamp, so any
  // client-side re-sort only scrambles a correct array. Preserve array order.
  const toolCalls = useMemo<ToolCall[]>(() => {
    if (!turn.toolCalls) return []
    try {
      const parsed = JSON.parse(turn.toolCalls)
      const rawCalls = Array.isArray(parsed)
        ? parsed
        : parsed && typeof parsed === 'object'
          ? Object.values(parsed)
          : []
      return rawCalls.filter(
        (tc): tc is ToolCall => !!tc && typeof tc === 'object',
      )
    } catch {
      return []
    }
  }, [turn.toolCalls])

  const visibleToolCalls = useMemo(() => {
    let filtered = toolCalls
    if (hideSandboxTools)
      filtered = filtered.filter((tc) => !SANDBOX_TOOLS.has(tc.function.name))
    // Exclude ask_user calls — they are rendered as HITL exchanges below
    filtered = filtered.filter((tc) => tc.function.name !== 'ask_user')
    return filtered
  }, [toolCalls, hideSandboxTools])

  // The backend persists an ordered turn timeline (interleaved assistant text,
  // tool calls, and HITL exchanges). Newer turns carry it; older turns don't.
  const timelineItems = useMemo<TimelineItem[] | null>(() => {
    if (!turn.timeline) return null
    try {
      const parsed = JSON.parse(turn.timeline)
      return Array.isArray(parsed) ? (parsed as TimelineItem[]) : null
    } catch {
      return null
    }
  }, [turn.timeline])

  // Build the ordered body segments. When the persisted timeline is present we
  // use it (so intermediate narration shows as collapsed "Reasoning" between
  // tool calls, exactly like the live stream). Otherwise we fall back to the
  // flat tool_calls array, which the backend already stores in execution order,
  // splitting it into tool runs with each ask_user exchange (HITL) inline.
  const bodySegments = useMemo<BodySegment[]>(() => {
    const segments: BodySegment[] = []
    let toolRun: ToolCall[] = []
    const flushTools = () => {
      if (toolRun.length > 0) {
        segments.push({ kind: 'tools', calls: toolRun })
        toolRun = []
      }
    }

    if (timelineItems) {
      // The final assistant text duplicates assistantOutput, which the bottom
      // response block renders — drop it here so it isn't shown twice.
      let lastTextIdx = -1
      if (turn.assistantOutput) {
        for (let i = timelineItems.length - 1; i >= 0; i--) {
          if (timelineItems[i].t === 'text') {
            lastTextIdx = i
            break
          }
        }
      }
      timelineItems.forEach((item, idx) => {
        if (item.t === 'text') {
          if (idx === lastTextIdx) return
          const content = (item.content ?? '').trim()
          if (!content) return
          flushTools()
          segments.push({ kind: 'reasoning', content })
        } else if (item.t === 'hitl') {
          flushTools()
          segments.push({
            kind: 'hitl',
            id: item.id ?? `hitl-${idx}`,
            question: item.question ?? '',
            response: stripAskUserWrapper(item.response ?? ''),
            cancelled: !!item.cancelled,
          })
        } else if (item.t === 'tool') {
          const name = item.name ?? ''
          if (hideSandboxTools && SANDBOX_TOOLS.has(name)) return
          toolRun.push({
            id: item.id ?? `tool-${idx}`,
            type: 'function',
            function: { name, arguments: item.args ?? '' },
            result: item.result,
            success: item.success,
            duration_ms: item.duration_ms,
            sandbox_parent_duration_ms: item.sandbox_parent_duration_ms,
          })
        }
      })
      flushTools()
      return segments
    }

    for (const tc of toolCalls) {
      if (tc.function.name === 'ask_user') {
        flushTools()
        let question = ''
        try {
          const args = JSON.parse(tc.function.arguments)
          question = args.question || args.prompt || args.message || ''
        } catch {
          question = tc.function.arguments || ''
        }
        const cached = toolResultsCache?.[tc.id]
        const response = stripAskUserWrapper(
          tc.result ?? cached?.toolResult ?? '',
        )
        const success = tc.success ?? cached?.toolSuccess
        segments.push({
          kind: 'hitl',
          id: tc.id,
          question,
          response,
          cancelled: success === false,
        })
        continue
      }
      if (hideSandboxTools && SANDBOX_TOOLS.has(tc.function.name)) continue
      toolRun.push(tc)
    }
    flushTools()
    return segments
  }, [timelineItems, turn.assistantOutput, toolCalls, hideSandboxTools, toolResultsCache])

  const failedToolCalls = useMemo(
    () =>
      visibleToolCalls.filter(
        (tc) =>
          (tc.success ?? toolResultsCache?.[tc.id]?.toolSuccess) === false,
      ),
    [toolResultsCache, visibleToolCalls],
  )
  const useSummaryView = toolCallView === 'summary'
  const autoExpandSummary = failedToolCalls.length > 0
  const showToolDetails =
    !useSummaryView ||
    toolCallDetailsOverride === 'open' ||
    (toolCallDetailsOverride === 'auto' && autoExpandSummary)
  // In summary mode, keep failure-driven auto expansion compact by showing
  // only failed tools unless the user explicitly opens full details.
  const summaryFailedOnly =
    useSummaryView &&
    showFailedToolCallsOnly &&
    toolCallDetailsOverride !== 'open'

  const renderToolCallCard = (tc: ToolCall) => {
    const cached = toolResultsCache?.[tc.id]
    // Prefer persisted result fields from enriched JSON, fall back to cache
    const toolResult = tc.result ?? cached?.toolResult
    const toolSuccess = tc.success ?? cached?.toolSuccess
    const toolDurationMs = tc.duration_ms ?? cached?.toolDurationMs
    const isSandbox = SANDBOX_TOOLS.has(tc.function.name)
    return (
      <ToolCallCard
        key={tc.id}
        toolCallId={tc.id}
        toolName={tc.function.name}
        agentId={agentId}
        toolArgs={tc.function.arguments}
        toolResult={toolResult}
        toolSuccess={toolSuccess}
        toolDurationMs={toolDurationMs}
        status={toolSuccess === false ? 'failed' : 'done'}
        {...(isSandbox
          ? {
              sandboxId: cached?.sandboxId,
              sandboxExitCode: cached?.sandboxExitCode,
              sandboxDurationMs: cached?.sandboxDurationMs,
              sandboxParentDurationMs:
                tc.sandbox_parent_duration_ms ??
                cached?.sandboxParentDurationMs,
              ...(sessionId ? { sessionId } : {}),
            }
          : {})}
      />
    )
  }

  const sources = useMemo(() => {
    const collected: Array<{
      title: string
      url: string
      description: string
    }> = []
    for (const tc of visibleToolCalls) {
      if (tc.function.name !== 'web_search') continue
      const cached = toolResultsCache?.[tc.id]
      const toolResult = tc.result ?? cached?.toolResult
      if (!toolResult) continue
      collected.push(...parseSearchSources(toolResult))
    }
    const byUrl = new Map<
      string,
      { title: string; url: string; description: string }
    >()
    for (const src of collected) {
      if (!byUrl.has(src.url)) byUrl.set(src.url, src)
    }
    return Array.from(byUrl.values())
  }, [toolResultsCache, visibleToolCalls])

  const linkEmbeds = useMemo(() => {
    if (!turn.assistantOutput) return []
    const links = extractLinksFromMarkdown(turn.assistantOutput)
    if (sources.length === 0) return links
    const sourceUrls = new Set(sources.map((src) => src.url))
    return links.filter((url) => !sourceUrls.has(url))
  }, [sources, turn.assistantOutput])

  const latencyMs =
    typeof turn.latencyMs === 'bigint'
      ? safeBigIntToNumber(turn.latencyMs)
      : Number(turn.latencyMs)

  const { message: userMessage, attachments: userAttachments } = useMemo(
    () =>
      turn.userInput
        ? parseAttachedFiles(turn.userInput)
        : { message: '', attachments: [] },
    [turn.userInput],
  )

  return (
    <div className="space-y-2">
      {/* User message */}
      {turn.userInput && (
        <div className="flex justify-end">
          <div className="max-w-[80%] rounded-md text-brand-secondary-500 bg-brand-secondary-600/20 px-4 pt-1.5 pb-2">
            {userMessage && (
              <MentionText className="text-sm text-brand-secondary-200 whitespace-pre-wrap">
                {userMessage}
              </MentionText>
            )}
            <AttachedFilesPreview attachments={userAttachments} />
          </div>
        </div>
      )}

      {/* Turn body: contiguous tool-call runs and HITL exchanges, interleaved
          in true execution order (see bodySegments). Replaces the old layout
          that dumped every tool call before every HITL question. */}
      {bodySegments.map((seg, segIdx) => {
        if (seg.kind === 'reasoning') {
          // Intermediate model narration between tool calls — collapsed by
          // default, matching the live timeline's "Reasoning" disclosure.
          return (
            <details
              key={`reasoning-${segIdx}`}
              className="min-w-0 group/reasoning"
            >
              <summary className="flex cursor-pointer items-center gap-2 rounded px-3 py-1.5 text-xs text-white/30 hover:text-white/40 transition-colors select-none list-none [&::-webkit-details-marker]:hidden light:text-black/30 light:hover:text-black/40">
                <Brain className="w-3 h-3" />
                <span>Reasoning</span>
                <ChevronDown className="w-3 h-3 ml-auto transition-transform group-open/reasoning:rotate-180" />
              </summary>
              <div className="mt-1 px-3 text-xs whitespace-pre-wrap leading-relaxed text-white/30 light:text-black/30">
                {seg.content}
              </div>
            </details>
          )
        }
        if (seg.kind ==='hitl') {
          return (
            <div key={`hitl-${seg.id}`} className="space-y-2">
              <div className="flex items-start gap-2.5 rounded px-3 py-2.5 border border-brand-secondary-500/20 bg-brand-secondary-500/5">
                <MessageCircleQuestion className="w-4 h-4 text-brand-secondary-400 shrink-0 mt-0.5" />
                <div className="text-sm text-white/80 light:text-black/80">{seg.question}</div>
              </div>
              {seg.cancelled ? (
                <div className="flex items-start gap-2.5 rounded px-3 py-2.5 border border-yellow-500/20 bg-yellow-500/5">
                  <Clock className="w-4 h-4 text-yellow-400 shrink-0 mt-0.5" />
                  <div className="text-xs text-yellow-400/70">
                    No response received
                  </div>
                </div>
              ) : seg.response ? (
                <div className="flex justify-end">
                  <div className="max-w-[80%] rounded-2xl bg-brand-secondary-700/15 px-4 py-3">
                    <MentionText className="text-sm text-white/90 whitespace-pre-wrap light:text-black/90">
                      {seg.response}
                    </MentionText>
                  </div>
                </div>
              ) : null}
            </div>
          )
        }

        const calls = seg.calls
        if (calls.length === 0) return null
        const segFailed = calls.filter(
          (tc) =>
            (tc.success ?? toolResultsCache?.[tc.id]?.toolSuccess) === false,
        )
        const segBreakdown = formatToolBreakdown(
          calls.map((tc) => tc.function.name),
        )
        const detailCalls = summaryFailedOnly ? segFailed : calls
        return (
          <div key={`tools-${segIdx}`} className="space-y-1.5">
            {useSummaryView && (
              <div
                onClick={() =>
                  setToolCallDetailsOverride(
                    showToolDetails ?'closed' : 'open',
                  )
                }
                className="flex cursor-pointer items-center gap-2 rounded px-3 py-2 border border-brand-secondary-600/30 bg-brand-main-900/40"
              >
                <span className="text-xs text-white/70 light:text-black/70">
                  Used {calls.length} tool{calls.length === 1 ? '' : 's'}
                  {segBreakdown && (
                    <span className="text-white/45 light:text-black/45"> ({segBreakdown})</span>
                  )}
                  {segFailed.length > 0 && (
                    <span className="text-red-400">
                      {' '}
                      ({segFailed.length} failed)
                    </span>
                  )}
                </span>
                <button
                  type="button"
                  className="ml-auto text-[11px] text-brand-secondary-300 hover:text-cyan-200 transition-colors"
                >
                  {showToolDetails ? 'Hide details' : 'Debug details'}
                </button>
              </div>
            )}
            {showToolDetails && detailCalls.map((tc) => renderToolCallCard(tc))}
          </div>
        )
      })}

      {/* Assistant message */}
      {turn.assistantOutput && (
        <div className="min-w-0">
          <div className="group/turn relative w-full py-3 pr-20 text-sm text-white/90 light:text-black/90">
            <AgentMarkdown variant="chat">{turn.assistantOutput}</AgentMarkdown>
            {(onCopy || onRetry || onShare || onFeedback) && (
              <div className="absolute right-0 top-2 z-10 inline-flex items-center gap-1 rounded border border-brand-main-500 bg-brand-main-900/85 px-1 py-0.5 backdrop-blur-sm opacity-100 transition-opacity md:opacity-0 md:pointer-events-none md:group-hover/turn:opacity-100 md:group-hover/turn:pointer-events-auto md:group-focus-within/turn:opacity-100 md:group-focus-within/turn:pointer-events-auto">
                {onCopy && (
                  <Tooltip content="Copy response" side="top">
                    <button
                      type="button"
                      onClick={() => onCopy(turn)}
                      disabled={disableActions}
                      className="inline-flex items-center justify-center w-6 h-6 rounded text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 transition-colors disabled:opacity-40 light:text-black/40 light:hover:text-black/80"
                    >
                      <Copy className="w-3 h-3" />
                    </button>
                  </Tooltip>
                )}
                {onRetry && (
                  <Tooltip content="Retry" side="top">
                    <button
                      type="button"
                      onClick={() => onRetry(turn)}
                      disabled={disableActions || !turn.userInput}
                      className="inline-flex items-center justify-center w-6 h-6 rounded text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 transition-colors disabled:opacity-40 light:text-black/40 light:hover:text-black/80"
                    >
                      <RotateCcw className="w-3 h-3" />
                    </button>
                  </Tooltip>
                )}
                {onShare && (
                  <Tooltip content="Share" side="top">
                    <button
                      type="button"
                      onClick={() => onShare(turn)}
                      disabled={disableActions}
                      className="inline-flex items-center justify-center w-6 h-6 rounded text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 transition-colors disabled:opacity-40 light:text-black/40 light:hover:text-black/80"
                    >
                      <Share2 className="w-3 h-3" />
                    </button>
                  </Tooltip>
                )}
                {onFeedback && (
                  <div className="inline-flex items-center gap-0.5 rounded">
                    <Tooltip content="Helpful" side="top">
                      <button
                        type="button"
                        onClick={() => onFeedback(turn.id,'up')}
                        disabled={disableActions}
                        className={`inline-flex items-center justify-center w-6 h-6 rounded transition-colors ${
                          feedbackValue === 'up'
                            ? 'text-green-300 bg-green-500/10'
                            : 'text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 light:text-black/40 light:hover:text-black/80'
                        } light:hover:text-black/80`}
                      >
                        <ThumbsUp className="w-3 h-3" />
                      </button>
                    </Tooltip>
                    <Tooltip content="Not helpful" side="top">
                      <button
                        type="button"
                        onClick={() => onFeedback(turn.id, 'down')}
                        disabled={disableActions}
                        className={`inline-flex items-center justify-center w-6 h-6 rounded transition-colors ${
                          feedbackValue === 'down'
                            ? 'text-red-300 bg-red-500/10'
                            : 'text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 light:text-black/40 light:hover:text-black/80'
                        } light:hover:text-black/80`}
                      >
                        <ThumbsDown className="w-3 h-3" />
                      </button>
                    </Tooltip>
                  </div>
                )}
                {turn.totalTokens > 0 && (() => {
                  const cacheRead = turn.cacheReadInputTokens ?? 0
                  const cacheWrite = turn.cacheWriteInputTokens ?? 0
                  const fresh = Math.max(turn.promptTokens - cacheRead - cacheWrite, 0)
                  const hasCacheBreakdown = cacheRead > 0 || cacheWrite > 0
                  return (
                    <Tooltip
                      side="top"
                      delayDuration={150}
                      content={
                        <div className="w-72 p-3 text-xs text-white space-y-2 light:text-brand-main-50">
                          <div className="flex items-center justify-between gap-3">
                            <span className="font-medium">Token usage</span>
                            <span className="font-mono tabular-nums text-white/60 light:text-black/60">
                              Turn {turn.turnNumber}
                            </span>
                          </div>
                          <div className="space-y-1">
                            <div className="flex justify-between text-white/70 light:text-black/70">
                              <span>Prompt</span>
                              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                                {turn.promptTokens.toLocaleString()}
                              </span>
                            </div>
                            {hasCacheBreakdown && (
                              <>
                                <div className="flex justify-between pl-3 text-white/55 light:text-black/55">
                                  <span>· Fresh</span>
                                  <span className="font-mono tabular-nums text-white/85 light:text-black/85">
                                    {fresh.toLocaleString()}
                                  </span>
                                </div>
                                {cacheRead > 0 && (
                                  <div className="flex justify-between pl-3 text-white/55 light:text-black/55">
                                    <span>· Cache read</span>
                                    <span className="font-mono tabular-nums text-white/85 light:text-black/85">
                                      {cacheRead.toLocaleString()}
                                    </span>
                                  </div>
                                )}
                                {cacheWrite > 0 && (
                                  <div className="flex justify-between pl-3 text-white/55 light:text-black/55">
                                    <span>· Cache write</span>
                                    <span className="font-mono tabular-nums text-white/85 light:text-black/85">
                                      {cacheWrite.toLocaleString()}
                                    </span>
                                  </div>
                                )}
                              </>
                            )}
                            <div className="flex justify-between text-white/70 light:text-black/70">
                              <span>Completion</span>
                              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                                {turn.completionTokens.toLocaleString()}
                              </span>
                            </div>
                            <div className="flex justify-between border-t border-white/10 pt-1 text-white/70 light:border-black/10 light:text-black/70">
                              <span>Total</span>
                              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                                {turn.totalTokens.toLocaleString()}
                              </span>
                            </div>
                          </div>
                          <div className="border-t border-white/10 pt-2 text-[10px] text-white/40 leading-relaxed light:border-black/10 light:text-black/40">
                            {hasCacheBreakdown
                              ?'Cache reads are billed at ~10% the fresh rate; cache writes at ~125% (Anthropic). The context-window indicator counts all three as occupancy.'
                              : 'Prompt tokens include the full conversation history sent to the model on this turn, so they grow with each turn. The context window indicator shows the most recent prompt size.'}
                          </div>
                        </div>
                      }
                    >
                      <button
                        type="button"
                        className="inline-flex items-center justify-center w-6 h-6 rounded text-white/40 hover:text-white/80 hover:bg-brand-main-800/60 transition-colors light:text-black/40 light:hover:text-black/80"
                        aria-label="Token usage breakdown"
                      >
                        <Info className="w-3 h-3" />
                      </button>
                    </Tooltip>
                  )
                })()}
              </div>
            )}
          </div>
          {sources.length > 0 && (
            <div className="mt-3">
              <div className="flex items-center gap-2 text-[10px] uppercase tracking-wider text-white/30 mb-2 light:text-black/30">
                Sources
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2">
                {sources.map((src, i) => {
                  const domain = extractDomain(src.url)
                  return (
                    <a
                      key={`${src.url}-${i}`}
                      href={src.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="rounded bg-brand-main-800/40 border border-brand-main-700/30 hover:border-blue-500/30 hover:bg-brand-main-800/60 transition-colors p-2.5"
                    >
                      <div className="flex items-center gap-2 mb-1.5 min-w-0">
                        <img
                          src={`https://www.google.com/s2/favicons?domain=${domain}&sz=32`}
                          alt=""
                          className="w-3.5 h-3.5 rounded-sm shrink-0"
                          loading="lazy"
                        />
                        <span className="text-[9px] text-white/30 truncate light:text-black/30">
                          {domain}
                        </span>
                      </div>
                      <div className="text-[11px] text-white/80 font-medium leading-tight line-clamp-2 light:text-black/80">
                        {src.title}
                      </div>
                      <div className="text-[10px] text-white/40 leading-tight mt-1 line-clamp-2 light:text-black/40">
                        {src.description}
                      </div>
                    </a>
                  )
                })}
              </div>
            </div>
          )}
          {linkEmbeds.length > 0 && (
            <div className="mt-3">
              <div className="flex items-center gap-2 text-[10px] uppercase tracking-wider text-white/30 mb-2 light:text-black/30">
                Links
              </div>
              <div className="flex flex-wrap gap-2">
                {linkEmbeds.map((url) => (
                  <a
                    key={url}
                    href={url}
                    target="_blank"
                    rel="noopener noreferrer"
                    className="inline-flex items-center gap-2 rounded-full border border-brand-main-700/40 bg-brand-main-900/40 px-3 py-1.5 text-[11px] text-white/70 hover:text-white/90 hover:border-brand-secondary-500/40 transition-colors light:text-black/70 light:hover:text-black/90"
                  >
                    <LinkIcon className="w-3 h-3 text-white/40 light:text-black/40" />
                    <span className="max-w-[220px] truncate">
                      {extractDomain(url)}
                    </span>
                  </a>
                ))}
              </div>
            </div>
          )}
        </div>
      )}

      {/* Error */}
      {turn.error && (
        <div className="rounded-xl bg-red-500/10 px-4 py-3 text-sm text-red-400 border border-red-500/15">
          {turn.error}
        </div>
      )}

      {/* Subtle turn metadata */}
      <div className="flex items-center gap-2 text-[10px] text-white/20 select-none light:text-black/20">
        <span>Turn {turn.turnNumber}</span>
        {model && (
          <>
            <span className="text-white/10 light:text-black/10">·</span>
            <span>{model}</span>
          </>
        )}
        {latencyMs > 0 && (
          <>
            <span className="text-white/10 light:text-black/10">·</span>
            <span>{latencyMs}ms</span>
          </>
        )}
      </div>
    </div>
  )
}
