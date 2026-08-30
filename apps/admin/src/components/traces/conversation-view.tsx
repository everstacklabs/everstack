import { useMemo, useState } from 'react'
import Markdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { MARKDOWN_COMPONENTS } from './markdown-code'

// GFM (tables, strikethrough, task lists, autolinks) — bare react-markdown is
// CommonMark only. Module-level so the array reference stays stable across renders.
const REMARK_PLUGINS = [remarkGfm]
import {
  User,
  Bot,
  Settings,
  Wrench,
  Brain,
  ChevronRight,
  ChevronDown,
} from 'lucide-react'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'

import { ToolCallCard } from '../deployments/agents/tool-call-card'
import {
  parseConversation,
  messageText,
  type ChatMessage,
  type ChatRole,
  type ContentPart,
} from '@/utils/trace-messages'
import { getAttr } from '@/utils/traces-common'
import { cn } from '@/lib/utils'

// Each role is distinguished by a coloured left accent + label only (no card
// fill/border) since the conversation already sits inside the IOViewer card.
const ROLE_META: Record<
  ChatRole,
  { label: string; icon: typeof User; text: string; accent: string }
> = {
  system: {
    label: 'System',
    icon: Settings,
    text: 'text-slate-300',
    accent: 'border-slate-500/40',
  },
  developer: {
    label: 'Developer',
    icon: Settings,
    text: 'text-slate-300',
    accent: 'border-slate-500/40',
  },
  user: {
    label: 'User',
    icon: User,
    text: 'text-brand-secondary-300',
    accent: 'border-brand-secondary-500/50',
  },
  assistant: {
    label: 'Assistant',
    icon: Bot,
    text: 'text-teal-300',
    accent: 'border-teal-500/40',
  },
  tool: {
    label: 'Tool',
    icon: Wrench,
    text: 'text-amber-300',
    accent: 'border-amber-500/45',
  },
}

function ContentParts({ parts }: { parts: ContentPart[] }) {
  return (
    <div className="space-y-2">
      {parts.map((part, i) => {
        if (part.type === 'text') {
          return (
            <div
              key={i}
              className="prose prose-invert prose-sm max-w-none text-sm text-zinc-200 break-words"
            >
              <Markdown remarkPlugins={REMARK_PLUGINS} components={MARKDOWN_COMPONENTS}>
                {part.text}
              </Markdown>
            </div>
          )
        }
        if (part.type === 'image') {
          return (
            <a
              key={i}
              href={part.url}
              target="_blank"
              rel="noopener noreferrer"
              className="block"
            >
              <img
                src={part.url}
                alt="message attachment"
                loading="lazy"
                className="max-h-64 rounded border border-brand-main-600 object-contain"
              />
            </a>
          )
        }
        if (part.type === 'audio') {
          return (
            <audio
              key={i}
              controls
              src={part.url}
              className="w-full max-w-sm"
            />
          )
        }
        return (
          <pre
            key={i}
            className="text-[11px] text-brand-main-50 whitespace-pre-wrap break-all rounded bg-black/30 border border-brand-main-800/40 px-3 py-2 max-h-64 overflow-y-auto light:text-black"
          >
            {JSON.stringify(part.value, null, 2)}
          </pre>
        )
      })}
    </div>
  )
}

/**
 * Find the tool span matching a tool call so the card can deep-link to it.
 * Tool spans carry observation.type=TOOL and the tool name in attributes.
 */
function findToolSpanId(
  allSpans: Span[] | undefined,
  toolName: string,
): string | undefined {
  if (!allSpans?.length) return undefined
  const match = allSpans.find((s) => {
    const obs = (getAttr(s, 'observation.type') || '').toUpperCase()
    if (obs !== 'TOOL') return false
    const name =
      getAttr(s, 'tool.name') ||
      getAttr(s, 'gen_ai.tool.name') ||
      s.spanName ||
      ''
    return name === toolName || name.endsWith(toolName)
  })
  return match?.spanId || undefined
}

/**
 * Collapsible reasoning / extended-thinking panel shown above an assistant's
 * final answer. Collapsed by default so the answer stays prominent.
 */
function ReasoningBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="mb-2 rounded border border-violet-500/20 bg-violet-500/5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-1.5 px-2.5 py-1.5 text-left"
      >
        <Brain className="h-3.5 w-3.5 text-violet-400" />
        <span className="text-[11px] font-medium uppercase tracking-wide text-violet-300">
          Reasoning
        </span>
        <span className="text-[10px] text-brand-main-50 light:text-black">
          {text.length.toLocaleString()} chars
        </span>
        {open ? (
          <ChevronDown className="ml-auto h-3 w-3 text-brand-main-50 light:text-black" />
        ) : (
          <ChevronRight className="ml-auto h-3 w-3 text-brand-main-50 light:text-black" />
        )}
      </button>
      {open && (
        <div className="border-t border-violet-500/15 px-2.5 py-2">
          <div className="prose prose-invert prose-sm max-w-none text-[13px] text-violet-100/80 break-words">
            <Markdown remarkPlugins={REMARK_PLUGINS} components={MARKDOWN_COMPONENTS}>
              {text}
            </Markdown>
          </div>
        </div>
      )}
    </div>
  )
}

function truncateInline(value: string, max = 140): string {
  const compact = value.replace(/\s+/g, ' ').trim()
  if (!compact) return ''
  return compact.length > max ? `${compact.slice(0, max - 1)}…` : compact
}

function messageSummary(msg: ChatMessage): string {
  const pieces: string[] = []
  const text = truncateInline(messageText(msg))
  if (text) pieces.push(text)
  if (msg.reasoning)
    pieces.push(`${msg.reasoning.length.toLocaleString()} reasoning chars`)
  if (msg.toolCalls?.length) {
    pieces.push(
      `${msg.toolCalls.length} tool call${msg.toolCalls.length === 1 ? '' : 's'}`,
    )
  }
  const images = msg.content.filter((part) => part.type === 'image').length
  if (images) pieces.push(`${images} image${images === 1 ? '' : 's'}`)
  const audio = msg.content.filter((part) => part.type === 'audio').length
  if (audio) pieces.push(`${audio} audio`)
  const json = msg.content.filter((part) => part.type === 'json').length
  if (json) pieces.push(`${json} JSON block${json === 1 ? '' : 's'}`)
  return pieces.join(' · ') || 'No content'
}

function MessageRow({
  msg,
  allSpans,
  onSelectSpan,
}: {
  msg: ChatMessage
  allSpans?: Span[]
  onSelectSpan?: (spanId: string) => void
}) {
  const meta = ROLE_META[msg.role]
  const Icon = meta.icon
  const [open, setOpen] = useState(true)
  const summary = messageSummary(msg)

  return (
    <div className={cn('border-l pl-2.5', meta.accent)}>
      <button
        type="button"
        onClick={() => setOpen((value) => !value)}
        aria-expanded={open}
        className="group mb-1 flex w-full min-w-0 items-center gap-1.5 rounded px-1 py-0.5 text-left transition-colors hover:bg-white/5 light:hover:bg-black/5"
      >
        {open ? (
          <ChevronDown className="h-3 w-3 shrink-0 text-brand-main-50 transition-colors group-hover:text-brand-main-50 light:text-black light:group-hover:text-black" />
        ) : (
          <ChevronRight className="h-3 w-3 shrink-0 text-brand-main-50 transition-colors group-hover:text-brand-main-50 light:text-black light:group-hover:text-black" />
        )}
        <Icon className={`h-3.5 w-3.5 shrink-0 ${meta.text}`} />
        <span
          className={`shrink-0 text-[11px] font-medium uppercase tracking-wide ${meta.text}`}
        >
          {msg.name ? `${meta.label} · ${msg.name}` : meta.label}
        </span>
        {!open && (
          <span className="min-w-0 flex-1 truncate text-[11px] text-brand-main-50 light:text-black">
            {summary}
          </span>
        )}
        {msg.finishReason && (
          <span className="ml-auto shrink-0 text-[10px] text-brand-main-50 light:text-black">
            {msg.finishReason}
          </span>
        )}
      </button>

      {open && (
        <div className="pb-1 pl-5">
          {msg.reasoning && <ReasoningBlock text={msg.reasoning} />}

          {msg.content.length > 0 && <ContentParts parts={msg.content} />}

          {msg.toolCalls && msg.toolCalls.length > 0 && (
            <div className="mt-2 space-y-1.5">
              {msg.toolCalls.map((tc, i) => {
                const spanId = findToolSpanId(allSpans, tc.name)
                const argsStr =
                  typeof tc.args === 'string'
                    ? tc.args
                    : JSON.stringify(tc.args)
                return (
                  <div key={tc.id || i}>
                    <ToolCallCard
                      toolCallId={tc.id}
                      toolName={tc.name}
                      toolArgs={argsStr}
                    />
                    {spanId && onSelectSpan && (
                      <button
                        type="button"
                        onClick={() => onSelectSpan(spanId)}
                        className="mt-0.5 text-[10px] text-brand-secondary-400/70 hover:text-brand-secondary-300"
                      >
                        View tool span →
                      </button>
                    )}
                  </div>
                )
              })}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

/**
 * Structured conversation renderer. Parses the stored request/response payload
 * into typed messages and renders them as a chat (roles, tool calls, tool
 * results, multimodal images). Returns null when nothing parses so the caller
 * can fall back to the legacy text/JSON view.
 */
export function ConversationView({
  rawData,
  allSpans,
  onSelectSpan,
}: {
  rawData: unknown
  allSpans?: Span[]
  onSelectSpan?: (spanId: string) => void
}) {
  const messages = useMemo(() => parseConversation(rawData), [rawData])

  if (messages.length === 0) return null

  return (
    <div className="space-y-3">
      {messages.map((msg, i) => (
        <MessageRow
          key={i}
          msg={msg}
          allSpans={allSpans}
          onSelectSpan={onSelectSpan}
        />
      ))}
    </div>
  )
}

export { hasStructuredConversation } from '@/utils/trace-messages'
