import { useState, useRef, useEffect, useCallback, useMemo } from 'react'
import {
  ArrowUp,
  Paperclip,
  ImageIcon,
  ChevronDown,
  Sparkles,
  Box,
  Loader2,
  AlertTriangle,
  ExternalLink,
  MessageCircleQuestion,
  Clock,
  Square,
  Terminal,
  Globe,
  GitBranch,
  X,
  Zap,
  Search,
  BrainCircuit,
  Eye,
  EyeOff,
  KeyRound,
  Workflow,
  Check,
  Plus,
} from 'lucide-react'
import {
  AgentMode,
  SessionStatus,
  type AgentDefinition,
  type AgentSession,
  type AgentSessionTurn,
} from '@/server/agents'
import { TurnCard, parseAttachedFiles, AttachedFilesPreview } from './turn-card'
import { ToolCallCard, SANDBOX_TOOLS } from './tool-call-card'
import { SpawnCard } from './spawn-card'
import { SystemBlockRenderer } from '@/components/chat/blocks/system-block-renderer'
import { ModelPicker } from '@/components/providers/model-picker'
import { ContextSelector } from '@/components/home/context-selector'
// import { useSpeechToText } from '@/hooks/deployments/use-audio'
import {
  useSessionEvents,
  useCompleteSession,
  useSteerSession,
  useAgent,
  useUpdateAgent,
  useAgents,
  useAgentCapabilities,
  type AgentStreamEvent,
  type ToolResultCacheEntry,
} from '@/hooks/deployments/use-agents'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import {
  Button,
  Tooltip,
  TooltipProvider,
  toast,
} from '@everstack/ui/components'
import { copyToClipboard } from '@everstack/utils/functions/clipboard'
import { ui } from '@everstack/ui'
import { AgentMarkdown } from './agent-markdown'
import { useAnimatedNumber } from '@/hooks/use-animated-number'
import {
  useLiveTokenCount,
  useLatestPromptTokens,
} from '@/hooks/use-live-token-count'
import { Iconify } from '@everstack/ui/icons'
import {
  extractMentionTokens,
  isPathLikeMentionFilter,
  parseMention,
} from '@/lib/mention-parser'
import {
  FileMentionDropdown,
  type FileMentionDropdownHandle,
} from './file-mention-dropdown'
import { MentionText } from './mention-text'
import {
  MentionComposerInput,
  type MentionComposerInputHandle,
} from './mention-composer-input'
import {
  useGitHubInstallations,
  useGitHubRepositories,
  useGitHubBranches,
  useGitHubRepoTree,
} from '@/hooks/integrations/use-github'
import { useNavigate } from '@tanstack/react-router'
import {
  useFileUpload,
  getReadableUploadErrorMessage,
} from '@/hooks/storage/use-file-upload'
import { ObjectPurpose } from '@/server/storage'
import { useLicenseStatus } from '@/hooks/license/use-license-status'
import { BrowserStreamViewer } from './browser-stream-viewer'
import {
  WorkflowPreviewPanel,
  type WorkflowPreviewData,
} from './workflow-preview-panel'
import { useSidePanelStore } from '@/stores/side-panel-store'
import {
  useConfigureProvider,
  useConfiguredProviders,
  useProviderCatalog,
} from '@/hooks/vault/use-providers'
import { cn } from '@/lib/utils'
import {
  AGENT_SESSION_JSON_DOWNLOAD_EVENT,
  getAgentSessionJsonDownloadDetail,
} from './session-json-export-events'

const {
  Checkbox,
  Sheet,
  SheetContent,
  SheetHeader,
  SheetBody,
  SheetTitle,
  Select,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  Popover,
  PopoverTrigger,
  PopoverContent,
  Input,
  Label,
  Switch,
} = ui

/**
 * Reusable popover dropdown for the composer bar.
 * Matches the brand styling of ContextSelector and ModelPicker.
 */
function ComposerDropdown({
  label,
  value,
  options,
  onChange,
  mono,
}: {
  label: string
  value: string
  options: { value: string; label: string }[]
  onChange: (value: string) => void
  mono?: boolean
}) {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  const selected = options.find((o) => o.value === value)

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-1.5 h-7 rounded px-2 text-[11px] font-medium border border-brand-main-700/70 bg-brand-main-900/55 text-white/70 light:text-black/70 transition-colors hover:text-white/90 light:hover:text-black/90 hover:border-brand-main-600"
      >
        <span className="text-white/45 light:text-black/45 text-[11px]">
          {label}
        </span>
        <span className={mono ? 'font-mono' : ''}>
          {selected?.label ?? value}
        </span>
        <ChevronDown
          className={cn('size-3 transition-transform', open && 'rotate-180')}
        />
      </button>
      {open && (
        <div className="absolute bottom-full left-0 mb-1 min-w-[120px] rounded border border-brand-main-600 bg-brand-main-800 shadow-xl z-50 p-1">
          {options.map((option) => (
            <button
              key={option.value}
              onClick={() => {
                onChange(option.value)
                setOpen(false)
              }}
              className={cn(
                'flex w-full items-center gap-2 px-2 py-1.5 rounded text-left text-xs transition-colors',
                option.value === value
                  ? 'bg-brand-secondary-600/15 text-brand-secondary-300'
                  : 'text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80',
              )}
            >
              {option.label}
              {option.value === value && (
                <Check className="size-3 shrink-0 text-brand-secondary-400 ml-auto" />
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * Popover for file attach, image attach, and web search toggle.
 * Replaces the three separate icon buttons in the composer.
 */
function ComposerToolsPopover({
  onAttachFiles,
  onAttachImages,
  webSearchEnabled,
  webSearchAvailable,
  onToggleWebSearch,
}: {
  onAttachFiles: () => void
  onAttachImages: () => void
  webSearchEnabled: boolean
  webSearchAvailable: boolean
  onToggleWebSearch: () => void
}) {
  const [open, setOpen] = useState(false)
  const popoverRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (popoverRef.current && !popoverRef.current.contains(e.target as Node))
        setOpen(false)
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  return (
    <div ref={popoverRef} className="relative">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center justify-center h-7 w-7 rounded border border-brand-main-700/70 bg-brand-main-900/55 text-white/50 light:text-black/50 transition-colors hover:text-white/80 light:hover:text-black/80 hover:border-brand-main-600"
      >
        <Plus className="size-3.5" />
      </button>
      {open && (
        <div className="absolute bottom-full left-0 mb-1 w-52 rounded border border-brand-main-600 bg-brand-main-800 shadow-xl z-50 p-1">
          <button
            onClick={() => {
              onAttachFiles()
              setOpen(false)
            }}
            className="flex w-full items-center gap-2.5 px-2.5 py-2 rounded text-left text-xs text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80 transition-colors"
          >
            <Paperclip className="size-3.5 shrink-0" />
            Attach files
          </button>
          <button
            onClick={() => {
              onAttachImages()
              setOpen(false)
            }}
            className="flex w-full items-center gap-2.5 px-2.5 py-2 rounded text-left text-xs text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80 transition-colors"
          >
            <ImageIcon className="size-3.5 shrink-0" />
            Attach images
          </button>
          <div className="my-1 h-px bg-brand-main-700" />
          <button
            onClick={onToggleWebSearch}
            disabled={!webSearchAvailable}
            className={cn(
              'flex w-full items-center justify-between px-2.5 py-2 rounded text-left text-xs transition-colors',
              !webSearchAvailable
                ? 'text-white/25 light:text-black/25 cursor-not-allowed'
                : 'text-white/60 light:text-black/60 hover:bg-brand-main-700 hover:text-white/80 light:hover:text-black/80',
            )}
          >
            <span className="flex items-center gap-2.5">
              <Globe className="size-3.5 shrink-0" />
              Web search
            </span>
            <Switch
              checked={webSearchEnabled}
              onCheckedChange={onToggleWebSearch}
              disabled={!webSearchAvailable}
              className="scale-75"
            />
          </button>
          {!webSearchAvailable && (
            <p className="px-2.5 pb-1 text-[10px] text-white/20 light:text-black/20">
              Set EVS_SEARXNG_URL to enable
            </p>
          )}
        </div>
      )}
    </div>
  )
}

type UserInputOption = {
  label: string
  value: string
  description?: string
}

type PendingUserInputRequest = {
  inputId: string
  question: string
  inputType: 'text' | 'single_select' | 'multi_select'
  options: UserInputOption[]
  allowCustomResponse: boolean
  placeholder: string
  minSelections: number
  maxSelections: number
}

function parseUserInputOptions(raw: unknown): UserInputOption[] {
  if (!Array.isArray(raw)) return []

  const seen = new Set<string>()
  const options: UserInputOption[] = []
  for (const item of raw) {
    if (typeof item === 'string') {
      const label = item.trim()
      if (!label || seen.has(label.toLowerCase())) continue
      seen.add(label.toLowerCase())
      options.push({ label, value: label })
      continue
    }

    if (item && typeof item === 'object') {
      const maybeOption = item as Record<string, unknown>
      const label =
        typeof maybeOption.label === 'string' ? maybeOption.label.trim() : ''
      if (!label) continue
      const value =
        typeof maybeOption.value === 'string' && maybeOption.value.trim()
          ? maybeOption.value.trim()
          : label
      const key = value.toLowerCase()
      if (seen.has(key)) continue
      seen.add(key)
      options.push({
        label,
        value,
        description:
          typeof maybeOption.description === 'string'
            ? maybeOption.description.trim()
            : undefined,
      })
    }
  }

  return options
}

function clampSelection(next: string[], maxSelections: number): string[] {
  if (maxSelections > 0 && next.length > maxSelections) {
    return next.slice(0, maxSelections)
  }
  return next
}

function normalizeTurnTokenUsage(
  turns: AgentSessionTurn[],
  sessionTotalTokens: number,
): AgentSessionTurn[] {
  if (turns.length < 2) return turns

  const lastTotal = Number(turns[turns.length - 1]?.totalTokens ?? 0)
  const summedTotals = turns.reduce(
    (sum, turn) => sum + Number(turn.totalTokens ?? 0),
    0,
  )
  const likelyCumulative =
    sessionTotalTokens > 0 &&
    lastTotal === sessionTotalTokens &&
    summedTotals > sessionTotalTokens

  if (!likelyCumulative) return turns

  let prevPrompt = 0
  let prevCompletion = 0
  let prevTotal = 0

  return turns.map((turn) => {
    const promptTokens = Number(turn.promptTokens ?? 0)
    const completionTokens = Number(turn.completionTokens ?? 0)
    const totalTokens = Number(turn.totalTokens ?? 0)

    const normalizedTurn = {
      ...turn,
      promptTokens: Math.max(promptTokens - prevPrompt, 0),
      completionTokens: Math.max(completionTokens - prevCompletion, 0),
      totalTokens: Math.max(totalTokens - prevTotal, 0),
    }

    prevPrompt = promptTokens
    prevCompletion = completionTokens
    prevTotal = totalTokens
    return normalizedTurn
  })
}

function buildUserInputSubmission(
  pendingUserInput: PendingUserInputRequest,
  selectedValues: string[],
  customResponse: string,
): string {
  const selectedLabels = pendingUserInput.options
    .filter((option) => selectedValues.includes(option.value))
    .map((option) => option.label)
  const custom = customResponse.trim()

  if (pendingUserInput.inputType === 'multi_select') {
    const parts: string[] = []
    if (selectedLabels.length > 0) {
      parts.push(selectedLabels.join(', '))
    }
    if (custom) {
      parts.push(custom)
    }
    return parts.join('\n')
  }

  if (pendingUserInput.inputType === 'single_select') {
    if (selectedLabels.length > 0) return selectedLabels[0]
    return custom
  }

  return custom
}

function parseAutomationToast(
  toolName: string,
  toolResult?: string,
): { title: string; detail?: string } | null {
  if (!toolResult) return null

  if (toolName === 'create_trigger') {
    const nameMatch = toolResult.match(/Name:\s*(.+)/)
    const cronMatch = toolResult.match(/Cron:\s*(.+)/)
    return {
      title: nameMatch?.[1]?.trim() || 'Automation created',
      detail: cronMatch?.[1]?.trim(),
    }
  }

  if (toolName === 'schedule_cron') {
    const nameMatch = toolResult.match(/Created cron job \d+:\s*(.+)/i)
    const nextRunMatch = toolResult.match(/Next run:\s*(.+)/i)
    return {
      title: nameMatch?.[1]?.trim() || 'Sandbox schedule created',
      detail: nextRunMatch?.[1]?.trim(),
    }
  }

  return null
}

const SESSION_STATUS_STYLES: Record<
  number,
  { label: string; className: string }
> = {
  [SessionStatus.CREATED]: {
    label: 'Created',
    className: 'bg-brand-main-700/40 text-brand-main-200',
  },
  [SessionStatus.RUNNING]: {
    label: 'Running',
    className: 'bg-brand-secondary-600/20 text-brand-secondary-300',
  },
  [SessionStatus.WAITING_FOR_INPUT]: {
    label: 'Waiting for Input',
    className: 'bg-brand-secondary-500/15 text-brand-secondary-200',
  },
  [SessionStatus.WAITING_FOR_APPROVAL]: {
    label: 'Waiting for Approval',
    className: 'bg-brand-secondary-700/25 text-brand-secondary-300',
  },
  [SessionStatus.COMPLETED]: {
    label: 'Completed',
    className: 'bg-brand-secondary-400 text-brand-secondary-800',
  },
  [SessionStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-red-500/15 text-red-400 light:text-red-600',
  },
  [SessionStatus.CANCELLED]: {
    label: 'Cancelled',
    className: 'bg-brand-main-700/30 text-brand-main-300',
  },
  [8 /* SessionStatus.HIBERNATED */]: {
    label: 'Hibernated',
    className: 'bg-brand-main-700/40 text-brand-main-200',
  },
}

function envFlag(value: unknown, defaultValue: boolean): boolean {
  if (typeof value !== 'string') return defaultValue
  const normalized = value.trim().toLowerCase()
  if (['1', 'true', 'yes', 'on'].includes(normalized)) return true
  if (['0', 'false', 'no', 'off'].includes(normalized)) return false
  return defaultValue
}

const REASONING_ICONS = [Sparkles, Zap, Search, BrainCircuit, Eye] as const

function CyclingReasoningIcon({ active }: { active: boolean }) {
  const [idx, setIdx] = useState(0)
  useEffect(() => {
    if (!active) return
    const id = setInterval(
      () => setIdx((i) => (i + 1) % REASONING_ICONS.length),
      1800,
    )
    return () => clearInterval(id)
  }, [active])
  const Icon = REASONING_ICONS[active ? idx : 0]
  return (
    <Icon
      className={`w-3 h-3 transition-opacity duration-300 ${active ? 'animate-pulse' : ''}`}
    />
  )
}

const TIMELINE_PHASE_SUMMARY = envFlag(
  import.meta.env.VITE_AGENT_TIMELINE_PHASE_SUMMARY,
  true,
)
const TIMELINE_SHOW_FAILED_ONLY = envFlag(
  import.meta.env.VITE_AGENT_TIMELINE_SHOW_FAILED_TOOLS_ONLY,
  true,
)

// ToolResultCacheEntry is re-exported from the hook for consumers that imported from here
export type { ToolResultCacheEntry } from '@/hooks/deployments/use-agents'

interface TemplateCardData {
  id: string
  name: string
  slug: string
  description: string
  icon: string
  iconColor: string
  image: string
  networkMode: string
}

interface SessionTimelineProps {
  session: AgentSession
  hideStatusBar?: boolean
  /** Override the default model for new turns (from parent model picker) */
  initialModel?: string
}

/** Groups spawn events by tree_id into spawn cards */
function groupSpawnEvents(events: AgentStreamEvent[]) {
  const spawns = new Map<
    string,
    {
      task: string
      depth: number
      agentId?: string
      events: AgentStreamEvent[]
      status: 'running' | 'done' | 'failed'
      tokensUsed?: number
    }
  >()

  for (const e of events) {
    if (e.type === 'spawn.start') {
      const key = `${(e as any).spawnTreeId ?? ''}_${(e as any).spawnDepth ?? 0}`
      spawns.set(key, {
        task: (e as any).spawnTask ?? 'Sub-agent task',
        depth: (e as any).spawnDepth ?? 1,
        agentId: (e as any).agentId,
        events: [],
        status: 'running',
      })
      continue
    }

    if (e.type === 'spawn.end') {
      const key = `${(e as any).spawnTreeId ?? ''}_${(e as any).spawnDepth ?? 0}`
      const spawn = spawns.get(key)
      if (spawn) {
        spawn.status = 'done'
        spawn.tokensUsed = (e as any).totalTokens
      }
      continue
    }

    if (e.type === 'spawn.error') {
      const key = `${(e as any).spawnTreeId ?? ''}_${(e as any).spawnDepth ?? 0}`
      const spawn = spawns.get(key)
      if (spawn) spawn.status = 'failed'
      continue
    }

    // Route child events to their spawn group if they have spawn metadata
    const spawnDepth = (e as any).spawnDepth ?? (e as any).spawn_depth
    const spawnTreeId = (e as any).spawnTreeId ?? (e as any).spawn_tree_id
    if (spawnDepth != null && spawnTreeId != null) {
      const key = `${spawnTreeId}_${spawnDepth}`
      const spawn = spawns.get(key)
      if (spawn) spawn.events.push(e)
    }
  }

  return Array.from(spawns.entries()).map(([key, data]) => ({ key, ...data }))
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (!value || typeof value !== 'object') return null
  return value as Record<string, unknown>
}

function readString(
  source: Record<string, unknown> | null,
  keys: string[],
): string | null {
  if (!source) return null
  for (const key of keys) {
    const raw = source[key]
    if (typeof raw === 'string' && raw.trim()) {
      return raw.trim()
    }
  }
  return null
}

function normalizeRepoReference(repo: string): string {
  const normalized = repo.trim().replace(/\.git$/i, '')
  const githubPath = normalized.match(
    /(?:https?:\/\/)?(?:www\.)?github\.com\/(.+)/i,
  )?.[1]
  return githubPath ? githubPath.replace(/^\/+|\/+$/g, '') : normalized
}

function extractModelFromProviderError(
  error: string | null | undefined,
): string | null {
  if (!error) return null
  const directMatch = error.match(/failed to resolve model\s+([^:]+):/i)
  if (directMatch?.[1]?.trim()) return directMatch[1].trim()
  const fallbackMatch = error.match(/model not found:\s*([^\s(]+)/i)
  if (fallbackMatch?.[1]?.trim()) return fallbackMatch[1].trim()
  return null
}

function parseDebugJson(raw: string | undefined): unknown {
  if (!raw) return null
  try {
    return JSON.parse(raw)
  } catch (error) {
    return {
      parseError: error instanceof Error ? error.message : 'Invalid JSON',
      raw,
    }
  }
}

function sessionExportReplacer(_key: string, value: unknown): unknown {
  if (typeof value === 'bigint') return value.toString()
  return value
}

function downloadJson(filename: string, payload: unknown) {
  if (typeof window === 'undefined') return
  const blob = new Blob([JSON.stringify(payload, sessionExportReplacer, 2)], {
    type: 'application/json',
  })
  const url = window.URL.createObjectURL(blob)
  const link = document.createElement('a')
  link.href = url
  link.download = filename
  document.body.appendChild(link)
  link.click()
  link.remove()
  window.URL.revokeObjectURL(url)
}

interface AgentMentionSuggestion {
  agent: AgentDefinition
  mentionKey: string
  score: number
}

interface ResolvedAgentMention {
  token: string
  mentionKey: string
  agentId: string
  agentName: string
}

function normalizeMentionKey(value: string): string {
  return value.trim().toLowerCase()
}

function slugifyAgentName(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, '-')
    .replace(/^-+|-+$/g, '')
}

function getAgentMentionKeys(agent: AgentDefinition): string[] {
  const keys: string[] = []
  const alias = (agent.mentionAlias ?? '').trim()
  if (alias) keys.push(alias)
  if (agent.name.trim()) keys.push(agent.name.trim())
  const slug = slugifyAgentName(agent.name)
  if (slug) keys.push(slug)

  const deduped: string[] = []
  const seen = new Set<string>()
  for (const key of keys) {
    const normalized = normalizeMentionKey(key)
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    deduped.push(key)
  }
  return deduped
}

function getPreferredAgentMentionKey(agent: AgentDefinition): string {
  const keys = getAgentMentionKeys(agent)
  if (keys.length > 0) return keys[0]!
  return agent.id.slice(0, 8)
}

function buildAgentMentionSuggestions(
  filter: string,
  agents: AgentDefinition[],
): AgentMentionSuggestion[] {
  const normalizedFilter = normalizeMentionKey(filter)
  if (!normalizedFilter || isPathLikeMentionFilter(normalizedFilter)) return []

  const suggestions = agents
    .map((agent) => {
      const alias = normalizeMentionKey(agent.mentionAlias ?? '')
      const name = normalizeMentionKey(agent.name)
      const slug = normalizeMentionKey(slugifyAgentName(agent.name))
      const preferredKey = getPreferredAgentMentionKey(agent)
      let score = Number.POSITIVE_INFINITY

      if (alias && alias === normalizedFilter) score = 0
      else if (slug && slug === normalizedFilter) score = 1
      else if (name && name === normalizedFilter) score = 2
      else if (alias && alias.startsWith(normalizedFilter)) score = 3
      else if (slug && slug.startsWith(normalizedFilter)) score = 4
      else if (name && name.startsWith(normalizedFilter)) score = 5
      else if (alias && alias.includes(normalizedFilter)) score = 6
      else if (name && name.includes(normalizedFilter)) score = 7

      if (!Number.isFinite(score)) return null
      return { agent, mentionKey: preferredKey, score }
    })
    .filter((item): item is AgentMentionSuggestion => item !== null)

  suggestions.sort((a, b) => {
    if (a.score !== b.score) return a.score - b.score
    return String(a.agent.name ?? '').localeCompare(String(b.agent.name ?? ''))
  })

  return suggestions.slice(0, 8)
}

function resolveMentionToAgent(
  filter: string,
  agents: AgentDefinition[],
): AgentDefinition | null {
  const normalized = normalizeMentionKey(filter)
  if (!normalized || isPathLikeMentionFilter(normalized)) return null

  const exactAlias = agents.filter(
    (agent) => normalizeMentionKey(agent.mentionAlias ?? '') === normalized,
  )
  if (exactAlias.length === 1) return exactAlias[0]!

  const exactName = agents.filter(
    (agent) => normalizeMentionKey(agent.name) === normalized,
  )
  if (exactName.length === 1) return exactName[0]!

  const exactSlug = agents.filter(
    (agent) => normalizeMentionKey(slugifyAgentName(agent.name)) === normalized,
  )
  if (exactSlug.length === 1) return exactSlug[0]!

  const prefixMatches = agents.filter((agent) => {
    const alias = normalizeMentionKey(agent.mentionAlias ?? '')
    const name = normalizeMentionKey(agent.name)
    const slug = normalizeMentionKey(slugifyAgentName(agent.name))
    return (
      (alias && alias.startsWith(normalized)) ||
      (name && name.startsWith(normalized)) ||
      (slug && slug.startsWith(normalized))
    )
  })

  if (prefixMatches.length === 1) return prefixMatches[0]!
  return null
}

function resolveMentionedAgents(
  text: string,
  subagents: AgentDefinition[],
): ResolvedAgentMention[] {
  if (!text.trim() || subagents.length === 0) return []

  const mentions = extractMentionTokens(text)
  if (mentions.length === 0) return []

  const resolved: ResolvedAgentMention[] = []
  const seen = new Set<string>()
  for (const mention of mentions) {
    const agent = resolveMentionToAgent(mention.filter, subagents)
    if (!agent) continue

    const normalizedToken = normalizeMentionKey(mention.filter)
    const dedupeKey = `${normalizedToken}:${agent.id}`
    if (seen.has(dedupeKey)) continue
    seen.add(dedupeKey)

    resolved.push({
      token: mention.filter,
      mentionKey: getPreferredAgentMentionKey(agent),
      agentId: agent.id,
      agentName: agent.name,
    })
  }

  return resolved
}

function LiveTokenCounter({
  persistedTokens,
  events,
  isStreaming,
}: {
  persistedTokens: number
  events: AgentStreamEvent[]
  isStreaming: boolean
}) {
  const liveTokens = useLiveTokenCount(events)
  const rawCount = isStreaming
    ? persistedTokens + liveTokens.totalTokens
    : persistedTokens
  const displayCount = useAnimatedNumber(rawCount, !isStreaming)

  if (displayCount <= 0) return null

  return (
    <span
      className={`text-xs font-mono tabular-nums transition-colors duration-300 ${
        isStreaming ? 'text-blue-400' : 'text-white/30 light:text-black/30'
      }`}
    >
      {displayCount.toLocaleString()} tokens
    </span>
  )
}

// Context-window utilization for persistent agents. Compact circular
// ring beside the model picker; hover for the full breakdown.
//
// Tracks the LATEST llm.end's promptTokens, not a cumulative sum: each
// turn re-includes the entire conversation, so what's actually in the
// model's context window right now is the most recent prompt size.
//
// Colors at 80% / 85% / 95% match the backend compactor's tier
// thresholds (see internal/lib/handlers/gateway/compact/config.go) so
// the user sees the same signal that drives compaction.
function ContextWindowIndicator({
  events,
  orderedTurns,
  maxContextTokens,
}: {
  events: AgentStreamEvent[]
  orderedTurns: Array<{ promptTokens?: number | bigint }>
  maxContextTokens: number
}) {
  const livePrompt = useLatestPromptTokens(events)

  const latestPersisted = useMemo(() => {
    for (let i = orderedTurns.length - 1; i >= 0; i--) {
      const p = Number(orderedTurns[i]?.promptTokens ?? 0)
      if (p > 0) return p
    }
    return 0
  }, [orderedTurns])

  const used = livePrompt > 0 ? livePrompt : latestPersisted
  if (maxContextTokens <= 0) return null

  const ratio = Math.max(0, Math.min(1, used / maxContextTokens))
  const pct = ratio * 100

  const tier =
    ratio >= 0.95
      ? 'emergency'
      : ratio >= 0.85
        ? 'aggressive'
        : ratio >= 0.8
          ? 'background'
          : 'ok'

  const ringStroke =
    tier === 'emergency'
      ? '#ef4444'
      : tier === 'aggressive'
        ? '#f59e0b'
        : tier === 'background'
          ? '#eab308'
          : 'var(--color-brand-secondary-400)'

  const tierLabel =
    tier === 'emergency'
      ? 'Emergency · hard truncation imminent'
      : tier === 'aggressive'
        ? 'Aggressive · summarising 60% of history'
        : tier === 'background'
          ? 'Background · summarising 30% of history'
          : 'Healthy · no compaction needed'

  // 24px ring, stroke 3, radius 9 → circumference = 2π·9 ≈ 56.55
  const SIZE = 24
  const STROKE = 3
  const R = (SIZE - STROKE) / 2
  const C = 2 * Math.PI * R
  const dash = C * ratio

  const fmt = (n: number) =>
    n >= 1_000_000
      ? `${(n / 1_000_000).toFixed(1)}M`
      : n >= 1000
        ? `${(n / 1000).toFixed(n >= 10_000 ? 0 : 1)}k`
        : String(n)

  return (
    <Tooltip
      side="top"
      delayDuration={150}
      content={
        <div className="w-72 p-3 text-xs text-white space-y-2 light:text-brand-main-50">
          <div className="flex items-center justify-between gap-3">
            <span className="font-medium">Context window</span>
            <span
              className="font-mono tabular-nums"
              style={{ color: ringStroke }}
            >
              {pct.toFixed(1)}%
            </span>
          </div>
          <div className="space-y-1">
            <div className="flex justify-between text-white/70 light:text-black/70">
              <span>Used</span>
              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                {used.toLocaleString()} tokens
              </span>
            </div>
            <div className="flex justify-between text-white/70 light:text-black/70">
              <span>Limit</span>
              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                {maxContextTokens.toLocaleString()} tokens
              </span>
            </div>
            <div className="flex justify-between text-white/70 light:text-black/70">
              <span>Remaining</span>
              <span className="font-mono tabular-nums text-white light:text-brand-main-50">
                {Math.max(0, maxContextTokens - used).toLocaleString()} tokens
              </span>
            </div>
          </div>
          <div className="relative h-1 w-full overflow-hidden rounded-full bg-brand-main-800">
            <div
              className="absolute inset-y-0 left-0 transition-[width] duration-300"
              style={{ width: `${pct}%`, backgroundColor: ringStroke }}
            />
            {/* Compaction tier markers */}
            {[0.8, 0.85, 0.95].map((t) => (
              <div
                key={t}
                className="absolute inset-y-0 w-px bg-white/30 light:bg-black/30"
                style={{ left: `${t * 100}%` }}
              />
            ))}
          </div>
          <div className="text-[10px] text-white/50 light:text-black/50">
            {tierLabel}
          </div>
          <div className="border-t border-white/10 pt-2 text-[10px] text-white/40 leading-relaxed light:border-black/10 light:text-black/40">
            Compaction tiers: 80% background · 85% aggressive · 95% emergency.
            Counts the most recent LLM call's prompt size, not cumulative
            tokens.
          </div>
        </div>
      }
    >
      <button
        type="button"
        aria-label={`Context window ${pct.toFixed(0)}% used`}
        className="relative inline-flex items-center justify-center h-7 w-7 shrink-0 rounded hover:bg-white/5 focus:outline-none focus:ring-1 focus:ring-white/20 light:hover:bg-black/5 light:focus:ring-black/20"
      >
        <svg
          width={SIZE}
          height={SIZE}
          viewBox={`0 0 ${SIZE} ${SIZE}`}
          className="-rotate-90"
        >
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={R}
            stroke="rgba(255,255,255,0.12)"
            strokeWidth={STROKE}
            fill="none"
          />
          <circle
            cx={SIZE / 2}
            cy={SIZE / 2}
            r={R}
            stroke={ringStroke}
            strokeWidth={STROKE}
            fill="none"
            strokeLinecap="round"
            strokeDasharray={`${dash} ${C}`}
            className="transition-[stroke-dasharray,stroke] duration-300"
          />
        </svg>
        <span
          className="absolute inset-0 flex items-center justify-center text-[8px] font-mono tabular-nums"
          style={{ color: ringStroke }}
        >
          {pct >= 100 ? '!' : pct < 1 ? '·' : `${Math.round(pct)}`}
        </span>
        <span className="sr-only">
          {fmt(used)} / {fmt(maxContextTokens)}
        </span>
      </button>
    </Tooltip>
  )
}

export function SessionTimeline({
  session,
  hideStatusBar,
  initialModel,
}: SessionTimelineProps) {
  const orderedTurns = useMemo(() => {
    const sortedTurns = [...(session.turns ?? [])].sort((a, b) => {
      if (a.turnNumber !== b.turnNumber) return a.turnNumber - b.turnNumber
      return String(a.createdAt ?? '').localeCompare(String(b.createdAt ?? ''))
    })
    return normalizeTurnTokenUsage(
      sortedTurns,
      Number(session.totalTokens ?? 0),
    )
  }, [session.totalTokens, session.turns])
  const persistedSessionTokens = useMemo(
    () =>
      orderedTurns.reduce(
        (sum, turn) => sum + Number(turn.totalTokens ?? 0),
        0,
      ),
    [orderedTurns],
  )
  const [userInput, setUserInput] = useState('')
  const [pendingMessage, setPendingMessage] = useState<string | null>(null)
  const [queuedMessage, setQueuedMessage] = useState<string | null>(null)
  const [feedbackByTurn, setFeedbackByTurn] = useState<
    Record<string, 'up' | 'down' | null>
  >({})
  const {
    events,
    isStreaming,
    startTurn,
    stopStream,
    discardTurnEvents,
    toolResultsCache,
    exposedURLs,
    browserStreamActive,
    browserStreamSessionId,
    browserScreenshotBase64,
    hydrationDone,
  } = useSessionEvents(session.id, session.status, orderedTurns)
  const [browserViewerDismissed, setBrowserViewerDismissed] = useState(false)
  const [browserPanelWidth, setBrowserPanelWidth] = useState(480)
  const workflowPanelData = useSidePanelStore(
    (s) => s.workflowPanels[session.id] ?? null,
  )
  const workflowPanelVisible = useSidePanelStore((s) => s.workflowPanelVisible)
  const workflowPanelWidth = useSidePanelStore((s) => s.workflowPanelWidth)
  const setWorkflowPanelWidth = useSidePanelStore(
    (s) => s.setWorkflowPanelWidth,
  )
  // Reset dismissed state when browser stream becomes active
  const prevBrowserActiveRef = useRef(false)
  useEffect(() => {
    if (browserStreamActive && !prevBrowserActiveRef.current) {
      setBrowserViewerDismissed(false)
    }
    prevBrowserActiveRef.current = browserStreamActive
  }, [browserStreamActive])

  // Track create_workflow tool results → show workflow preview panel.
  // Scans both live stream events AND persisted turn history so the
  // toggle button survives page navigation / component remount.
  const lastWorkflowIdRef = useRef<string | null>(null)
  const lastWorkflowSessionRef = useRef<string | null>(null)
  const setWorkflowPanel = useSidePanelStore((s) => s.setWorkflowPanel)

  // Reset tracking ref when session changes
  useEffect(() => {
    if (
      lastWorkflowSessionRef.current &&
      lastWorkflowSessionRef.current !== session.id
    ) {
      lastWorkflowIdRef.current = null
    }
    lastWorkflowSessionRef.current = session.id
  }, [session.id])

  useEffect(() => {
    // 1. Check live stream events
    for (const e of events) {
      if (
        e.type === 'tool_call.end' &&
        e.toolName === 'create_workflow' &&
        e.toolResult
      ) {
        try {
          const parsed = JSON.parse(e.toolResult)
          if (
            parsed?.workflow_id &&
            parsed.workflow_id !== lastWorkflowIdRef.current
          ) {
            lastWorkflowIdRef.current = parsed.workflow_id
            setWorkflowPanel(session.id, parsed as WorkflowPreviewData)
          }
        } catch {
          /* ignore */
        }
      }
    }
    // 2. Check persisted turn history (for page reloads / navigation back)
    if (!lastWorkflowIdRef.current && session.turns) {
      for (const turn of orderedTurns) {
        if (!turn.toolCalls) continue
        try {
          const tcs = JSON.parse(turn.toolCalls) as Array<{
            function: { name: string }
            result?: string
          }>
          for (const tc of tcs) {
            if (tc.function.name === 'create_workflow' && tc.result) {
              try {
                const parsed = JSON.parse(tc.result)
                if (parsed?.workflow_id) {
                  lastWorkflowIdRef.current = parsed.workflow_id
                  setWorkflowPanel(session.id, parsed as WorkflowPreviewData)
                }
              } catch {
                /* ignore */
              }
            }
          }
        } catch {
          /* ignore parse errors */
        }
      }
    }
  }, [events, orderedTurns, session.id, setWorkflowPanel])

  const completeMutation = useCompleteSession()
  const steerSessionMutation = useSteerSession()

  // Build a map of turn number → model name from stream events
  const modelByTurn = useMemo(() => {
    const map: Record<number, string> = {}
    for (const e of events) {
      if (
        (e.type === 'turn.start' || e.type === 'turn.end') &&
        e.data?.model &&
        e.turnNumber > 0
      ) {
        map[e.turnNumber] = String(e.data.model)
      }
    }
    return map
  }, [events])

  const composerRef = useRef<MentionComposerInputHandle>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const [autoScroll, setAutoScroll] = useState(true)
  const [attachedFiles, setAttachedFiles] = useState<
    Array<{ file: File; objectId?: string; url?: string; uploading?: boolean }>
  >([])
  const { upload: uploadFile } = useFileUpload()
  const { data: licenseData } = useLicenseStatus()
  const currentTier = licenseData?.license?.tier ?? 'free'

  const [sandboxSheetOpen, setSandboxSheetOpen] = useState(false)
  const sandboxSheetRef = useRef<HTMLDivElement>(null)
  const sandboxAutoScroll = useRef(true)
  const prevSandboxCountRef = useRef(0)
  const [urlDropdownOpen, setUrlDropdownOpen] = useState(false)
  // Web search toggle (sticky per-session)
  const [webSearchEnabled, setWebSearchEnabled] = useState(false)
  const { data: capabilities } = useAgentCapabilities()
  const webSearchAvailable = capabilities?.web_search_available ?? false

  // File mention dropdown state
  const [mentionOpen, setMentionOpen] = useState(false)
  const [mentionFilter, setMentionFilter] = useState('')
  const [mentionRange, setMentionRange] = useState<{
    start: number
    end: number
  }>({ start: 0, end: 0 })
  const [selectedAgentMentionIndex, setSelectedAgentMentionIndex] = useState(0)
  const mentionDropdownRef = useRef<FileMentionDropdownHandle>(null)
  const { data: agent } = useAgent(session.agentId)
  const { data: subagents = [] } = useAgents({
    includeHidden: true,
    mode: AgentMode.SUBAGENT,
  })
  const [contextPath, setContextPath] = useState('/workspace')
  const [permissionProfile, setPermissionProfile] = useState('default')
  const [composerContext, setComposerContext] = useState('auto')
  const [composerModel, setComposerModel] = useState(() => {
    // Restore from localStorage if available, otherwise use initialModel or agent model
    if (typeof window !== 'undefined') {
      const stored = localStorage.getItem(`chat:model:${session.id}`)
      if (stored) return stored
    }
    return initialModel || agent?.model || ''
  })
  // Persist model selection per session
  const handleModelChange = useCallback(
    (newModel: string) => {
      setComposerModel(newModel)
      if (typeof window !== 'undefined') {
        localStorage.setItem(`chat:model:${session.id}`, newModel)
      }
    },
    [session.id],
  )

  // Pull max_context_tokens off the agent config for the context indicator
  // shown next to the model picker. Mirrors parseExistingMonitorConfig in
  // agent-form.tsx — same default (128k) when the agent has no monitor
  // block. agent.config is google.protobuf.Struct so values come through
  // as plain JS objects.
  const maxContextTokens = useMemo(() => {
    const cfg = agent?.config as Record<string, any> | undefined
    const mon = cfg?.monitor as Record<string, any> | undefined
    const v = mon?.max_context_tokens
    return typeof v === 'number' && v > 0 ? v : 128000
  }, [agent])
  const [repoInput, setRepoInput] = useState('')
  const [branchInput, setBranchInput] = useState('')
  const [contextEditorOpen, setContextEditorOpen] = useState(false)
  const [contextSaveStatus, setContextSaveStatus] = useState<
    'idle' | 'saving' | 'saved' | 'error'
  >('idle')
  const navigate = useNavigate()
  const { data: configuredProvidersData } = useConfiguredProviders()
  const { data: providerCatalogData } = useProviderCatalog()
  const configureProviderMutation = useConfigureProvider()
  const updateAgentMutation = useUpdateAgent()
  const { data: gitHubInstallations } = useGitHubInstallations()
  const isGitHubConnected = (gitHubInstallations?.length ?? 0) > 0
  const [inlineProviderName, setInlineProviderName] = useState('')
  const [inlineModelName, setInlineModelName] = useState('')
  const inlineProviderUserChanged = useRef(false)
  const [inlineApiKey, setInlineApiKey] = useState('')
  const [showInlineApiKey, setShowInlineApiKey] = useState(false)

  const agentGitInstallationId = useMemo(() => {
    const cfg = agent?.config as Record<string, any> | undefined
    const sb = cfg?.sandbox as Record<string, any> | undefined
    return Number(sb?.git_installation_id ?? sb?.gitInstallationId ?? 0)
  }, [agent?.config])
  const parsedRepo = useMemo(() => {
    const raw = repoInput.trim()
    if (!raw) return null
    let normalized = raw
    if (normalized.startsWith('https://github.com/')) {
      normalized = normalized.slice('https://github.com/'.length)
    }
    normalized = normalized.replace(/\.git$/, '').replace(/^\/+|\/+$/g, '')
    const parts = normalized.split('/')
    if (parts.length < 2 || !parts[0] || !parts[1]) return null
    return {
      owner: parts[0],
      repo: parts[1],
      fullName: `${parts[0]}/${parts[1]}`,
    }
  }, [repoInput])
  const gitInstallationId =
    agentGitInstallationId > 0 ? agentGitInstallationId : 0
  const { data: gitHubReposData } = useGitHubRepositories(gitInstallationId, {
    page: 1,
    perPage: 50,
  })
  const { data: gitHubBranches = [] } = useGitHubBranches(
    gitInstallationId,
    parsedRepo?.owner ?? '',
    parsedRepo?.repo ?? '',
    { page: 1, perPage: 100 },
  )
  const gitHubRepos = gitHubReposData?.repositories ?? []

  const effectiveSessionStatus = isStreaming
    ? SessionStatus.RUNNING
    : session.status
  const statusStyle = SESSION_STATUS_STYLES[effectiveSessionStatus] ?? {
    label: 'Unknown',
    className: 'bg-gray-500/20 text-gray-400',
  }
  const configuredProviders = useMemo(() => {
    const providers = configuredProvidersData?.providers ?? []
    return providers.filter((provider) => provider.isConfigured !== false)
  }, [configuredProvidersData?.providers])
  const missingProviderOptions = useMemo(() => {
    const providers = providerCatalogData?.providers ?? []
    return providers
      .filter((provider) => provider.name && (provider.models?.length ?? 0) > 0)
      .sort((a, b) =>
        String(a.displayName ?? a.name).localeCompare(
          String(b.displayName ?? b.name),
        ),
      )
  }, [providerCatalogData?.providers])
  const hasConfiguredProvider = configuredProviders.length > 0
  const selectedInlineProvider = useMemo(
    () =>
      missingProviderOptions.find(
        (provider) => provider.name === inlineProviderName,
      ) ?? null,
    [missingProviderOptions, inlineProviderName],
  )
  const selectedInlineProviderModels = selectedInlineProvider?.models ?? []
  const configuredAgentModel = agent?.model?.trim() || ''
  // Sessions can be continued unless explicitly cancelled by the user.
  const sessionCanContinue = session.status !== SessionStatus.CANCELLED
  const isActive = sessionCanContinue || isStreaming || events.length > 0
  // Dormant = session ended (completed/failed) but can be revived by sending a new message.
  const sessionDormant =
    session.status === SessionStatus.COMPLETED ||
    session.status === SessionStatus.FAILED
  const sessionRepoContext = useMemo(() => {
    const metadata = asRecord(session.metadata)
    const metadataSandbox = asRecord(metadata?.sandbox)
    const config = asRecord(agent?.config)
    const configSandbox = asRecord(config?.sandbox)

    const repo =
      readString(metadata, [
        'git_repo_url',
        'gitRepoUrl',
        'repo',
        'repository',
        'repo_url',
      ]) ??
      readString(metadataSandbox, [
        'git_repo_url',
        'gitRepoUrl',
        'repo',
        'repository',
        'repo_url',
      ]) ??
      readString(configSandbox, ['git_repo_url', 'gitRepoUrl'])
    const branch =
      readString(metadata, ['git_branch', 'gitBranch', 'branch']) ??
      readString(metadataSandbox, ['git_branch', 'gitBranch', 'branch']) ??
      readString(configSandbox, ['git_branch', 'gitBranch'])
    const explicitWorkDir =
      readString(metadata, ['work_dir', 'workDir', 'workdir', 'cwd']) ??
      readString(metadataSandbox, ['work_dir', 'workDir', 'workdir', 'cwd'])

    return {
      repo: repo ? normalizeRepoReference(repo) : null,
      branch,
      mountPath: repo ? '/repo' : (explicitWorkDir ?? '/workspace'),
    }
  }, [session.metadata, agent?.config])
  const contextPathOptions = useMemo(
    () => (sessionRepoContext.repo ? ['/repo', '/workspace'] : ['/workspace']),
    [sessionRepoContext.repo],
  )

  const startTurnWithMentionRouting = useCallback(
    async (input: string, modelOverride = composerModel) => {
      const resolvedMentions = resolveMentionedAgents(input, subagents)

      if (resolvedMentions.length > 0) {
        const steerLines = [
          'User referenced these sub-agents via @mentions in the next turn.',
          ...resolvedMentions.map(
            (item) =>
              `- @${item.token} => ${item.agentName} (id: ${item.agentId}, mention_key: ${item.mentionKey})`,
          ),
          'Treat these mappings as explicit delegation hints when selecting spawn/delegation targets.',
        ]

        try {
          await steerSessionMutation.mutateAsync({
            sessionId: session.id,
            role: 'system',
            content: steerLines.join('\n'),
          })
        } catch {
          toast.error(
            'Agent mention routing hint failed to persist; continuing with the turn.',
          )
        }
      }

      await startTurn(input, {
        enableWebSearch: webSearchEnabled,
        modelOverride: modelOverride || undefined,
      })
    },
    [
      session.id,
      startTurn,
      steerSessionMutation,
      subagents,
      webSearchEnabled,
      composerModel,
    ],
  )

  const handleCopyTurn = useCallback(async (turn: AgentSessionTurn) => {
    if (!turn.assistantOutput) return
    try {
      await copyToClipboard(turn.assistantOutput)
      toast.success('Copied response')
    } catch {
      toast.error('Failed to copy response')
    }
  }, [])

  const handleShareTurn = useCallback(async (turn: AgentSessionTurn) => {
    const parts = [] as string[]
    if (turn.userInput) parts.push(`User:\n${turn.userInput}`)
    if (turn.assistantOutput) parts.push(`Assistant:\n${turn.assistantOutput}`)
    if (parts.length === 0) return
    try {
      await copyToClipboard(parts.join('\n\n'))
      toast.success('Copied share snippet')
    } catch {
      toast.error('Failed to copy share snippet')
    }
  }, [])

  const handleRetryTurn = useCallback(
    async (turn: AgentSessionTurn) => {
      if (!turn.userInput) {
        toast.error('No user input to retry')
        return
      }
      if (isStreaming) {
        toast.error('Wait for the current turn to finish')
        return
      }
      await startTurnWithMentionRouting(turn.userInput)
    },
    [isStreaming, startTurnWithMentionRouting],
  )

  const handleFeedback = useCallback((turnId: string, value: 'up' | 'down') => {
    setFeedbackByTurn((prev) => ({
      ...prev,
      [turnId]: prev[turnId] === value ? null : value,
    }))
    toast.success('Thanks for the feedback')
  }, [])

  // Build repo context for the file mention dropdown
  const mentionRepoContext = useMemo(() => {
    if (gitInstallationId <= 0 || !parsedRepo) return null
    return {
      installationId: gitInstallationId,
      owner: parsedRepo.owner,
      repo: parsedRepo.repo,
      branch: branchInput.trim() || sessionRepoContext.branch || undefined,
    }
  }, [gitInstallationId, parsedRepo, branchInput, sessionRepoContext.branch])

  const agentMentionSuggestions = useMemo(
    () => buildAgentMentionSuggestions(mentionFilter, subagents),
    [mentionFilter, subagents],
  )
  const showAgentMentionDropdown =
    mentionOpen &&
    agentMentionSuggestions.length > 0 &&
    !isPathLikeMentionFilter(mentionFilter)

  // Prefetch the GitHub tree at page load so the @ dropdown is instant
  useGitHubRepoTree(
    mentionRepoContext?.installationId ?? 0,
    mentionRepoContext?.owner ?? '',
    mentionRepoContext?.repo ?? '',
    { ref: mentionRepoContext?.branch, enabled: !!mentionRepoContext },
  )

  // Track whether auto-save should be suppressed (during initialization)
  const contextInitRef = useRef(true)

  useEffect(() => {
    contextInitRef.current = true
    setContextPath(sessionRepoContext.mountPath)
    setRepoInput(sessionRepoContext.repo ?? '')
    setBranchInput(sessionRepoContext.branch ?? '')
    setPermissionProfile('default')
    setContextEditorOpen(false)
    setContextSaveStatus('idle')
    // Allow auto-save after initial values settle
    const t = setTimeout(() => {
      contextInitRef.current = false
    }, 300)
    return () => clearTimeout(t)
  }, [
    session.id,
    sessionRepoContext.mountPath,
    sessionRepoContext.repo,
    sessionRepoContext.branch,
  ])

  useEffect(() => {
    if (!hasConfiguredProvider && missingProviderOptions.length === 0) return
    if (
      !inlineProviderName ||
      !missingProviderOptions.some(
        (provider) => provider.name === inlineProviderName,
      )
    ) {
      setInlineProviderName(missingProviderOptions[0]?.name || '')
    }
  }, [hasConfiguredProvider, inlineProviderName, missingProviderOptions])

  useEffect(() => {
    if (!selectedInlineProvider) {
      if (inlineModelName) setInlineModelName('')
      return
    }
    const models = selectedInlineProvider.models ?? []
    if (
      !inlineModelName ||
      !models.some((model) => model.name === inlineModelName)
    ) {
      setInlineModelName(models[0]?.name || '')
    }
  }, [inlineModelName, selectedInlineProvider])

  useEffect(() => {
    if (!contextPathOptions.includes(contextPath)) {
      contextInitRef.current = true
      setContextPath(contextPathOptions[0] ?? '/workspace')
      setContextSaveStatus('idle')
      const t = setTimeout(() => {
        contextInitRef.current = false
      }, 300)
      return () => clearTimeout(t)
    }
  }, [contextPath, contextPathOptions])

  // Discard events for turns that are fully persisted (have assistantOutput).
  // Events are kept until the TurnCard has content, preventing the "flash"
  // where content disappears during the CQRS handoff.
  const discardedTurnsRef = useRef<Set<number>>(new Set())
  // Sticky ref: holds the last known user message for the current turn so it
  // survives the brief window where events are discarded but TurnCard hasn't
  // rendered yet (CQRS handoff race condition).
  const stickyUserMessageRef = useRef<string | null>(null)

  // Save tool results from events to the store cache so they survive event cleanup.
  // Runs separately from the discard logic since it depends on events.
  useEffect(() => {
    const entries: Record<string, ToolResultCacheEntry> = {}
    for (const e of events) {
      if (e.type === 'tool_call.end' && e.toolCallId) {
        entries[e.toolCallId] = {
          toolResult: e.toolResult,
          toolSuccess: e.toolSuccess,
          toolDurationMs: e.toolDurationMs,
          sandboxId: e.sandboxId,
          sandboxExitCode: e.sandboxExitCode,
          sandboxDurationMs: e.sandboxDurationMs,
          sandboxParentDurationMs: e.sandboxParentDurationMs,
        }
      }
    }
    if (Object.keys(entries).length > 0) {
      useAgentSessionStore
        .getState()
        .updateToolResultsCache(session.id, entries)
    }
  }, [events, session.id])

  // Discard events and clear UI state only when turns are FULLY persisted
  // (both userInput AND assistantOutput present). This matches the
  // currentTurnPersisted gate so TurnCard can render the full user+assistant
  // pair before live content is removed. Does NOT depend on `events`.
  useEffect(() => {
    if (!orderedTurns?.length) return

    let didDiscardNew = false
    for (const turn of orderedTurns) {
      if (
        turn.assistantOutput &&
        turn.userInput &&
        !discardedTurnsRef.current.has(turn.turnNumber)
      ) {
        discardedTurnsRef.current.add(turn.turnNumber)
        discardTurnEvents(turn.turnNumber)
        didDiscardNew = true
      }
    }

    // Clear all pending state when a turn was newly discarded (backend
    // persisted it with both userInput AND assistantOutput). Since both
    // fields are present, the TurnCard will render the full turn in the
    // same React commit — no flash. Clearing stickyUserMessageRef prevents
    // a duplicate user bubble in the live block (events are gone, but the
    // stale ref would keep currentTurnUserMessage non-null).
    if (didDiscardNew) {
      setPendingMessage(null)
      setSubmittedUserInput(null)
      stickyUserMessageRef.current = null
    }
  }, [orderedTurns, discardTurnEvents])

  // Process queued message when streaming ends naturally
  const prevStreamingRef = useRef(isStreaming)
  useEffect(() => {
    const wasStreaming = prevStreamingRef.current
    prevStreamingRef.current = isStreaming

    if (wasStreaming && !isStreaming && queuedMessage) {
      const msg = queuedMessage
      setQueuedMessage(null)
      // Wait for turn data to persist before starting the next turn
      const timer = setTimeout(() => {
        setPendingMessage(msg)
        void startTurnWithMentionRouting(msg)
      }, 1200)
      return () => clearTimeout(timer)
    }
  }, [isStreaming, queuedMessage, startTurnWithMentionRouting])

  // Force-send: stop current turn and immediately dispatch queued message.
  // Kept as a ref-stable callback so the queued message banner can call it.
  const forceSendQueuedRef = useRef<(() => void) | null>(null)
  forceSendQueuedRef.current = () => {
    const msg = queuedMessage
    if (!msg) return
    setQueuedMessage(null)
    setPendingMessage(msg)
    // MUST await stopStream() before starting the new turn.
    // The POST to /turns/stop is async — if we fire-and-forget it,
    // the stop can arrive at the backend AFTER the new turn starts,
    // killing the new turn instead of the old one.
    void (async () => {
      try {
        await stopStream()
      } catch {
        // Treat abort errors as fire-and-forget (opencode pattern)
      }
      await startTurnWithMentionRouting(msg)
    })()
  }

  useEffect(() => {
    setSelectedAgentMentionIndex(0)
  }, [mentionFilter, showAgentMentionDropdown])

  // Auto-scroll to bottom on new events
  useEffect(() => {
    if (autoScroll && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
    }
  }, [events.length, autoScroll, orderedTurns?.length])

  const handleScroll = useCallback(() => {
    if (!scrollRef.current) return
    const { scrollTop, scrollHeight, clientHeight } = scrollRef.current
    setAutoScroll(scrollHeight - scrollTop - clientHeight < 60)
  }, [])

  const scrollToBottom = useCallback(() => {
    if (scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight
      setAutoScroll(true)
    }
  }, [])

  const handleFileAttach = useCallback(
    async (e: React.ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files
      if (!files?.length) return
      for (const file of Array.from(files)) {
        const entry = { file, uploading: true } as {
          file: File
          objectId?: string
          url?: string
          uploading?: boolean
        }
        setAttachedFiles((prev) => [...prev, entry])
        try {
          const result = await uploadFile(
            file,
            ObjectPurpose.ARTIFACT,
            'agent_session',
            session.id,
          )
          setAttachedFiles((prev) =>
            prev.map((f) =>
              f.file === file
                ? {
                    ...f,
                    objectId: result.objectId,
                    url: result.url,
                    uploading: false,
                  }
                : f,
            ),
          )
        } catch (err) {
          setAttachedFiles((prev) => prev.filter((f) => f.file !== file))
          toast.error(
            `Failed to upload ${file.name}: ${getReadableUploadErrorMessage(err)}`,
          )
        }
      }
      if (fileInputRef.current) fileInputRef.current.value = ''
    },
    [uploadFile, session.id],
  )

  const removeAttachedFile = useCallback((file: File) => {
    setAttachedFiles((prev) => prev.filter((f) => f.file !== file))
  }, [])

  const handleSend = async () => {
    const input = userInput.trim()

    // HITL mode: route input to the user-input submit handler
    if (pendingUserInput && !submittedUserInput && input) {
      setUserInput('')
      setAutoScroll(true)
      await handleUserInputSubmit(input)
      return
    }

    // If there's a queued message from a stopped turn and no new input,
    // dispatch the queued message now.
    if (!input && attachedFiles.length === 0 && queuedMessage && !isStreaming) {
      const msg = queuedMessage
      setQueuedMessage(null)
      setPendingMessage(msg)
      setAutoScroll(true)
      await startTurnWithMentionRouting(msg)
      return
    }

    if (!input && attachedFiles.length === 0) return

    let message = input
    const uploadedFiles = attachedFiles.filter((f) => f.url && !f.uploading)
    if (uploadedFiles.length > 0) {
      const fileRefs = uploadedFiles
        .map((f) => `[${f.file.name}](${f.url})`)
        .join('\n')
      message = message
        ? `${message}\n\nAttached files:\n${fileRefs}`
        : `Attached files:\n${fileRefs}`
    }

    setUserInput('')
    setAttachedFiles([])
    setMentionOpen(false)
    setAutoScroll(true)

    if (isStreaming) {
      setQueuedMessage(message)
      return
    }

    setPendingMessage(message)
    await startTurnWithMentionRouting(message)
  }

  const handleInputChange = useCallback((val: string, cursor: number) => {
    setUserInput(val)
    const token = parseMention(val, cursor)
    if (token) {
      setMentionOpen(true)
      setMentionFilter(token.filter)
      setMentionRange({ start: token.start, end: token.end })
    } else {
      setMentionOpen(false)
    }
  }, [])

  const handleMentionSelect = useCallback(
    (filePath: string) => {
      const replacement = `@${filePath} `
      const caretPos = mentionRange.start + replacement.length
      composerRef.current?.replaceRange(
        mentionRange.start,
        mentionRange.end,
        replacement,
        caretPos,
      )
      setMentionOpen(false)
      requestAnimationFrame(() => {
        composerRef.current?.focus()
      })
    },
    [mentionRange],
  )

  const handleAgentMentionSelect = useCallback(
    (item: AgentMentionSuggestion) => {
      const replacement = `@${item.mentionKey} `
      const caretPos = mentionRange.start + replacement.length
      composerRef.current?.replaceRange(
        mentionRange.start,
        mentionRange.end,
        replacement,
        caretPos,
      )
      setMentionOpen(false)
      requestAnimationFrame(() => {
        composerRef.current?.focus()
      })
    },
    [mentionRange],
  )

  const handleKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
    // Intercept keys when mention dropdown is open
    if (mentionOpen && !e.nativeEvent.isComposing) {
      if (showAgentMentionDropdown) {
        if (
          ['ArrowUp', 'ArrowDown', 'Enter', 'Escape', 'Tab'].includes(e.key)
        ) {
          e.preventDefault()
          if (e.key === 'ArrowDown') {
            setSelectedAgentMentionIndex((prev) =>
              Math.min(prev + 1, agentMentionSuggestions.length - 1),
            )
            return
          }
          if (e.key === 'ArrowUp') {
            setSelectedAgentMentionIndex((prev) => Math.max(prev - 1, 0))
            return
          }
          if (e.key === 'Escape') {
            setMentionOpen(false)
            return
          }
          const suggestion = agentMentionSuggestions[selectedAgentMentionIndex]
          if (suggestion) {
            handleAgentMentionSelect(suggestion)
          }
          return
        }
      }
      const isBrowseMode = !mentionFilter
      if (['ArrowUp', 'ArrowDown', 'Enter', 'Escape', 'Tab'].includes(e.key)) {
        e.preventDefault()
        mentionDropdownRef.current?.handleKey(e.key)
        return
      }
      // Arrow right/left and backspace: only intercept in browse mode
      // so the cursor can still move in search text
      if (
        isBrowseMode &&
        ['ArrowRight', 'ArrowLeft', 'Backspace'].includes(e.key)
      ) {
        e.preventDefault()
        mentionDropdownRef.current?.handleKey(e.key)
        return
      }
    }
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      void handleSend()
    }
  }

  const handleEndSession = () => {
    completeMutation.mutate(session.id)
  }

  const stoppedByUserRef = useRef(false)
  const handleStop = () => {
    stoppedByUserRef.current = true
    void stopStream()
  }

  const handleInlineProviderSave = useCallback(async () => {
    if (!selectedInlineProvider) {
      toast.error('Select a provider first')
      return
    }
    if (!inlineModelName) {
      toast.error('Select a model first')
      return
    }
    if (!inlineApiKey.trim()) {
      toast.error('Enter an API key')
      return
    }

    try {
      await configureProviderMutation.mutateAsync({
        providerName: selectedInlineProvider.name,
        apiKey: inlineApiKey.trim(),
        enabledModels: [inlineModelName],
        customBaseUrl: selectedInlineProvider.baseUrl || undefined,
      })

      if (agent?.id && inlineModelName !== configuredAgentModel) {
        await updateAgentMutation.mutateAsync({
          id: agent.id,
          model: inlineModelName,
        })
      }

      handleModelChange(inlineModelName)
      setInlineApiKey('')
      setShowInlineApiKey(false)
      toast.success(
        `${selectedInlineProvider.displayName || selectedInlineProvider.name} connected`,
      )
      const replayMessage =
        queuedMessage || pendingMessage || stickyUserMessageRef.current
      if (replayMessage && !isStreaming) {
        setPendingMessage(replayMessage)
        void startTurnWithMentionRouting(replayMessage, inlineModelName)
      }
      requestAnimationFrame(() => {
        composerRef.current?.focus()
      })
    } catch (error) {
      toast.error(
        `Failed to configure provider: ${error instanceof Error ? error.message : 'Unknown error'}`,
      )
    }
  }, [
    agent?.id,
    configuredAgentModel,
    configureProviderMutation,
    handleModelChange,
    isStreaming,
    inlineApiKey,
    inlineModelName,
    pendingMessage,
    queuedMessage,
    selectedInlineProvider,
    startTurnWithMentionRouting,
    updateAgentMutation,
  ])

  // Auto-save context changes with debounce — steers the session automatically
  useEffect(() => {
    if (contextInitRef.current) return

    const timer = setTimeout(() => {
      const normalizedRepo = repoInput.trim()
      const normalizedBranch = branchInput.trim()
      const selectedPath = contextPath.trim() || '/workspace'
      const permissionLabel =
        permissionProfile === 'read_only'
          ? 'read-only'
          : permissionProfile === 'elevated'
            ? 'elevated'
            : 'default'

      const steerContentLines = [
        'Session context updated by user. Apply this context to subsequent actions.',
        `- Preferred working directory: ${selectedPath}`,
        `- Permission profile: ${permissionLabel}`,
        normalizedRepo
          ? `- Repository hint: ${normalizedRepo}`
          : '- Repository hint: none (use instance context)',
        normalizedBranch
          ? `- Branch hint: ${normalizedBranch}`
          : '- Branch hint: default',
        'Acknowledge this update implicitly and continue.',
      ]

      setContextSaveStatus('saving')
      steerSessionMutation.mutate(
        {
          sessionId: session.id,
          role: 'system',
          content: steerContentLines.join('\n'),
        },
        {
          onSuccess: (response) => {
            if (response.accepted) {
              setContextSaveStatus('saved')
              toast.success('Session context updated')
            } else {
              setContextSaveStatus('error')
              toast.error('Session did not accept context update')
            }
          },
          onError: () => {
            setContextSaveStatus('error')
            toast.error('Failed to update session context')
          },
        },
      )
    }, 600)

    return () => clearTimeout(timer)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [contextPath, permissionProfile, repoInput, branchInput])

  // Clear "saved" indicator after a few seconds
  useEffect(() => {
    if (contextSaveStatus !== 'saved') return
    const t = setTimeout(() => setContextSaveStatus('idle'), 3000)
    return () => clearTimeout(t)
  }, [contextSaveStatus])

  // Scope all streaming display to the LATEST turn only.
  // When events from multiple turns accumulate (e.g. trooper stub sessions
  // where clearEvents doesn't fire until the DB catches up), we must avoid
  // concatenating responses from previous turns into the current one.
  // Slice from the LAST turn.start boundary — more reliable than turnNumber
  // because synthetic turn.start events may have a different turnNumber
  // than the backend's events.
  const latestTurnEvents = useMemo(() => {
    if (events.length === 0) return events
    // Find the index of the last turn.start event
    let lastTurnStartIdx = -1
    for (let i = events.length - 1; i >= 0; i--) {
      if (events[i]?.type === 'turn.start') {
        lastTurnStartIdx = i
        break
      }
    }
    if (lastTurnStartIdx <= 0) return events // 0 or no boundary — use all
    // Include session-level events (sandbox.ready, etc.) that came before the turn.
    // Exclude sandbox.template.select — it's turn-specific (HITL card), not session-level.
    const sessionLevelEvents = events
      .slice(0, lastTurnStartIdx)
      .filter(
        (e) =>
          (e.type?.startsWith('sandbox.') &&
            e.type !== 'sandbox.template.select') ||
          e.type === 'session.start',
      )
    return [...sessionLevelEvents, ...events.slice(lastTurnStartIdx)]
  }, [events])

  const seenAutomationToastIdsRef = useRef<Set<string>>(new Set())

  useEffect(() => {
    for (const event of latestTurnEvents) {
      if (event.type !== 'tool_call.end' || event.toolSuccess === false)
        continue
      if (
        event.toolName !== 'create_trigger' &&
        event.toolName !== 'schedule_cron'
      ) {
        continue
      }

      const toastKey = `${event.toolName}:${event.toolCallId}`
      if (seenAutomationToastIdsRef.current.has(toastKey)) continue

      const parsed = parseAutomationToast(event.toolName, event.toolResult)
      if (!parsed) continue

      seenAutomationToastIdsRef.current.add(toastKey)
      toast.success(
        parsed.detail ? `${parsed.title} - ${parsed.detail}` : parsed.title,
      )
    }
  }, [latestTurnEvents])

  // Build live streaming text from chunk events.
  // Smoothing is handled upstream by the RAF-based drip queue in the store
  // (createEventBatcher) — chunks arrive at ~60fps instead of in bursts.
  const streamingText = useMemo(() => {
    let text = ''
    let hasText = false
    for (const e of latestTurnEvents) {
      if (
        e.type === 'llm.start' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth &&
        hasText
      ) {
        // New LLM iteration in the tool loop — add spacing so sentences
        // from separate responses don't run together.
        text += '\n\n'
      }
      if (
        e.type === 'llm.chunk' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth
      ) {
        text += e.textDelta
        hasText = true
      }
    }
    return text
  }, [latestTurnEvents])

  // Build chronological timeline segments that interleave text with HITL exchanges.
  // This ensures HITL questions appear between the text that came before and after them.
  type TimelineSegment =
    | { kind: 'text'; content: string }
    | { kind: 'narration'; content: string }
    | {
        kind: 'tool-call'
        toolCallId: string
        toolName: string
        toolArgs?: string
        toolResult?: string
        toolSuccess?: boolean
        toolDurationMs?: number
        status: 'running' | 'done' | 'failed'
        sandboxId?: string
        sandboxExitCode?: number
        sandboxDurationMs?: number
        sandboxParentDurationMs?: number
      }
    | {
        kind: 'hitl-resolved'
        inputId: string
        question: string
        response: string
      }
    | {
        kind: 'hitl-cancelled'
        inputId: string
        question: string
        reason: string
      }
    | {
        kind: 'hitl-pending'
        inputId: string
        question: string
        inputType: PendingUserInputRequest['inputType']
        options: UserInputOption[]
        allowCustomResponse: boolean
      }
    | {
        kind: 'system-block'
        data: Record<string, unknown>
      }
    | { kind: 'fallback'; event: AgentStreamEvent }
    | { kind: 'session-error'; event: AgentStreamEvent }

  const toolCallStarts = useMemo(() => {
    const starts = latestTurnEvents.filter(
      (e) =>
        e.type === 'tool_call.start' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth,
    )
    const seen = new Set<string>()
    const deduped: AgentStreamEvent[] = []
    for (const e of starts) {
      const key = e.toolCallId
        ? `id:${e.toolCallId}`
        : `sig:${e.toolName ?? ''}:${e.toolArgs ?? ''}`
      if (seen.has(key)) continue
      seen.add(key)
      deduped.push(e)
    }
    return deduped
  }, [latestTurnEvents])
  const toolCallEndByKey = useMemo(() => {
    const ends = latestTurnEvents.filter(
      (e) =>
        e.type === 'tool_call.end' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth,
    )
    const byKey = new Map<string, AgentStreamEvent>()
    for (const e of ends) {
      const key = e.toolCallId
        ? `id:${e.toolCallId}`
        : `sig:${e.toolName ?? ''}:${e.toolArgs ?? ''}`
      // Keep latest end event for this key.
      byKey.set(key, e)
    }
    return byKey
  }, [latestTurnEvents])
  const spawnGroups = useMemo(
    () => groupSpawnEvents(latestTurnEvents),
    [latestTurnEvents],
  )

  // ── Persistent activity summary ──────────────────────────────────────
  // Aggregates tool/spawn/sandbox activity from BOTH persisted turns AND live events.
  // This data drives the sticky activity bar that survives page refresh.
  type ActivityItem = {
    id: string
    name: string
    category: 'tool' | 'sandbox' | 'spawn'
    status: 'running' | 'done' | 'failed'
    durationMs?: number
    /** For spawns: the delegated task description */
    task?: string
    /** Turn number this activity belongs to */
    turnNumber?: number
  }

  const activitySummary = useMemo(() => {
    const items: ActivityItem[] = []
    const seenIds = new Set<string>()

    // 1. From persisted turns
    for (const turn of orderedTurns) {
      if (!turn.toolCalls) continue
      let tcs: Array<{
        id: string
        function: { name: string; arguments: string }
        result?: string
        success?: boolean
        duration_ms?: number
      }> = []
      try {
        tcs = JSON.parse(turn.toolCalls)
      } catch {
        continue
      }
      for (const tc of tcs) {
        if (seenIds.has(tc.id)) continue
        seenIds.add(tc.id)
        const name = tc.function.name
        if (name === 'ask_user') continue

        if (name === 'spawn_agent') {
          let task = 'Sub-agent task'
          try {
            const args = JSON.parse(tc.function.arguments)
            task = args.task?.slice(0, 120) || task
          } catch {
            /* */
          }
          items.push({
            id: tc.id,
            name,
            category: 'spawn',
            status: tc.success === false ? 'failed' : 'done',
            durationMs: tc.duration_ms,
            task,
            turnNumber: turn.turnNumber,
          })
        } else if (SANDBOX_TOOLS.has(name)) {
          items.push({
            id: tc.id,
            name,
            category: 'sandbox',
            status: tc.success === false ? 'failed' : 'done',
            durationMs: tc.duration_ms,
            turnNumber: turn.turnNumber,
          })
        } else {
          items.push({
            id: tc.id,
            name,
            category: 'tool',
            status: tc.success === false ? 'failed' : 'done',
            durationMs: tc.duration_ms,
            turnNumber: turn.turnNumber,
          })
        }
      }
    }

    // 2. From live tool call events (dedup against persisted)
    for (const tc of toolCallStarts) {
      if (tc.toolCallId && seenIds.has(tc.toolCallId)) continue
      if (tc.toolCallId) seenIds.add(tc.toolCallId)
      if (tc.toolName === 'ask_user') continue

      const key = tc.toolCallId
        ? `id:${tc.toolCallId}`
        : `sig:${tc.toolName ?? ''}:${tc.toolArgs ?? ''}`
      const ended = toolCallEndByKey.get(key)

      if (tc.toolName === 'spawn_agent') {
        let task = 'Sub-agent task'
        try {
          const args = JSON.parse(tc.toolArgs || '{}')
          task = args.task?.slice(0, 120) || task
        } catch {
          /* */
        }
        items.push({
          id: tc.toolCallId || key,
          name: tc.toolName,
          category: 'spawn',
          status: ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running',
          durationMs: ended?.toolDurationMs,
          task,
          turnNumber: tc.turnNumber,
        })
      } else if (SANDBOX_TOOLS.has(tc.toolName)) {
        items.push({
          id: tc.toolCallId || key,
          name: tc.toolName,
          category: 'sandbox',
          status: ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running',
          durationMs: ended?.toolDurationMs,
          turnNumber: tc.turnNumber,
        })
      } else {
        items.push({
          id: tc.toolCallId || key,
          name: tc.toolName,
          category: 'tool',
          status: ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running',
          durationMs: ended?.toolDurationMs,
          turnNumber: tc.turnNumber,
        })
      }
    }

    // 3. Live spawn groups (supplement with spawn events that don't have tool_call counterparts)
    for (const sg of spawnGroups) {
      // Check if already tracked via tool call
      const alreadyTracked = items.some(
        (it) =>
          it.category === 'spawn' &&
          it.status === sg.status &&
          it.task === sg.task,
      )
      if (!alreadyTracked) {
        items.push({
          id: sg.key,
          name: 'spawn_agent',
          category: 'spawn',
          status: sg.status,
          task: sg.task,
        })
      }
    }

    const tools = items.filter((i) => i.category === 'tool')
    const sandbox = items.filter((i) => i.category === 'sandbox')
    const spawns = items.filter((i) => i.category === 'spawn')

    return {
      items,
      tools,
      sandbox,
      spawns,
      totalTools: tools.length,
      failedTools: tools.filter((i) => i.status === 'failed').length,
      sandboxOps: sandbox.length,
      spawnCount: spawns.length,
      spawnRunning: spawns.filter((i) => i.status === 'running').length,
      spawnFailed: spawns.filter((i) => i.status === 'failed').length,
      hasActivity: items.length > 0,
    }
  }, [orderedTurns, toolCallStarts, toolCallEndByKey, spawnGroups])

  const runningActivityCount =
    activitySummary.tools.filter((item) => item.status === 'running').length +
    activitySummary.sandbox.filter((item) => item.status === 'running').length +
    activitySummary.spawns.filter((item) => item.status === 'running').length

  const openActivityPanel = () => {
    if (activitySummary.tools.length > 0) setActivityPanelTab('tools')
    else if (activitySummary.sandbox.length > 0) setActivityPanelTab('sandbox')
    else setActivityPanelTab('spawns')
    setActivitySheetOpen(true)
  }

  useEffect(() => {
    const openActivity = () => openActivityPanel()
    const openSandbox = () => setSandboxSheetOpen(true)

    window.addEventListener(
      `session-timeline:open-activity:${session.id}`,
      openActivity,
    )
    window.addEventListener(
      `session-timeline:open-sandbox:${session.id}`,
      openSandbox,
    )
    return () => {
      window.removeEventListener(
        `session-timeline:open-activity:${session.id}`,
        openActivity,
      )
      window.removeEventListener(
        `session-timeline:open-sandbox:${session.id}`,
        openSandbox,
      )
    }
  }, [
    session.id,
    activitySummary.tools.length,
    activitySummary.sandbox.length,
    activitySummary.spawns.length,
  ])

  const [activitySheetOpen, setActivitySheetOpen] = useState(false)
  const [activityPanelTab, setActivityPanelTab] = useState<
    'tools' | 'sandbox' | 'spawns'
  >('tools')
  const [expandedActivityRows, setExpandedActivityRows] = useState<Set<string>>(
    new Set(),
  )

  const latestFullPersistedTurnNumber = useMemo(() => {
    for (let i = orderedTurns.length - 1; i >= 0; i--) {
      const turn = orderedTurns[i]
      if (turn?.turnNumber && turn.userInput && turn.assistantOutput) {
        return turn.turnNumber
      }
    }
    return 0
  }, [orderedTurns])

  // Derive the current turn's user message from events (authoritative source).
  // The synthetic turn.start always carries data.user_input, so this works for
  // both UI-initiated and external (Discord/Slack) messages. Falls back to
  // pendingMessage for the brief moment before the synthetic event is in the store.
  //
  const currentTurnUserMessage = useMemo(() => {
    // First try events — the most reliable source
    for (let i = latestTurnEvents.length - 1; i >= 0; i--) {
      const e = latestTurnEvents[i]
      if (e.type === 'turn.start' && e.data?.user_input) {
        return e.data.user_input as string
      }
    }
    // Fall back to pendingMessage (set immediately by handleSend, before events exist)
    if (pendingMessage) return pendingMessage
    // Last resort: sticky ref holds the last known user message for this turn,
    // preventing it from vanishing during the CQRS handoff window.
    return stickyUserMessageRef.current
  }, [latestTurnEvents, pendingMessage])

  // Detect whether the latest turn's events are from a turn that's already been
  // fully persisted by the backend (both userInput AND assistantOutput).
  // When true, the TurnCard is the source of truth - the live block is suppressed.
  const currentTurnPersisted = useMemo(() => {
    const lastPersistedTurn =
      orderedTurns.length > 0 ? orderedTurns[orderedTurns.length - 1] : null

    // During CQRS handoff, the persisted turn can arrive before the streaming
    // flags/events are fully cleared. If the latest persisted turn already
    // matches the current user message and has assistant output, prefer the
    // persisted card so duplicate tool/HITL blocks do not linger at the bottom.
    if (
      lastPersistedTurn?.assistantOutput &&
      lastPersistedTurn.userInput &&
      currentTurnUserMessage &&
      lastPersistedTurn.userInput === currentTurnUserMessage
    ) {
      return true
    }

    if (isStreaming) return false

    let latestEventTurnNumber = 0
    for (let i = latestTurnEvents.length - 1; i >= 0; i--) {
      const tn = latestTurnEvents[i]?.turnNumber
      if (tn && tn > 0) {
        latestEventTurnNumber = tn
        break
      }
    }
    if (
      latestEventTurnNumber > 0 &&
      latestFullPersistedTurnNumber > latestEventTurnNumber
    ) {
      return true
    }

    for (let i = latestTurnEvents.length - 1; i >= 0; i--) {
      const tn = latestTurnEvents[i]?.turnNumber
      if (tn && tn > 0) {
        return orderedTurns.some(
          (t) => t.turnNumber === tn && !!t.assistantOutput && !!t.userInput,
        )
      }
    }

    return !isStreaming && (orderedTurns?.length ?? 0) > 0
  }, [
    currentTurnUserMessage,
    isStreaming,
    latestFullPersistedTurnNumber,
    latestTurnEvents,
    orderedTurns,
  ])

  // Keep the sticky ref in sync: capture non-null messages.
  // Clear it when the turn is fully persisted so the live block doesn't
  // show a stale user message from the sticky ref.
  if (currentTurnPersisted) {
    stickyUserMessageRef.current = null
  } else if (currentTurnUserMessage) {
    stickyUserMessageRef.current = currentTurnUserMessage
  }

  // For backward compat — thinkingPhase and other code that checked externalUserInput
  const externalUserInput =
    currentTurnUserMessage && !pendingMessage ? currentTurnUserMessage : null

  // Guard against duplicate user message bubbles during CQRS handoff.
  // If the last persisted turn's userInput matches the live turn's message,
  // the TurnCard will render it (or already rendered it) — suppress the live copy.
  const liveUserMessageAlreadyPersisted = useMemo(() => {
    if (!currentTurnUserMessage) return false
    if (isStreaming) return false // during streaming, live block owns the message
    const lastTurn =
      orderedTurns.length > 0 ? orderedTurns[orderedTurns.length - 1] : null
    if (!lastTurn) return false
    return (
      !!lastTurn.userInput &&
      !!lastTurn.assistantOutput &&
      lastTurn.userInput === currentTurnUserMessage
    )
  }, [currentTurnUserMessage, orderedTurns, isStreaming])

  // Derive the current thinking/processing phase from latest turn events
  const thinkingPhase = useMemo(() => {
    if (!isStreaming || !currentTurnUserMessage) return null
    if (latestTurnEvents.length === 0) return 'Thinking'

    // Walk events to determine current phase
    const types = new Set(latestTurnEvents.map((e) => e.type))

    // Once we have text or tool calls, the main UI takes over
    if (streamingText || toolCallStarts.length > 0 || spawnGroups.length > 0)
      return null

    if (types.has('llm.start')) return 'Reasoning'
    if (types.has('turn.start')) return 'Processing'
    if (types.has('session.start')) return 'Analyzing'

    return 'Thinking'
  }, [
    isStreaming,
    pendingMessage,
    externalUserInput,
    latestTurnEvents,
    streamingText,
    toolCallStarts,
    spawnGroups,
  ])

  const sandboxState = useMemo(() => {
    let isReady = false
    let sandboxId = ''
    let hasError = false
    let errorMsg = ''
    let isProvisioning = false
    let isGitCloning = false

    // Sandbox events are session-level (turn 0) — scan all events, not just latest turn.
    // A sandbox.ready AFTER a sandbox.error clears the error (sandbox became available).
    // A new turn starting after a sandbox.error also clears it — the sandbox was usable
    // enough to accept the new turn.
    for (const e of events) {
      if (e.type === 'sandbox.create') {
        isProvisioning = true
        isGitCloning = false
      } else if (e.type === 'sandbox.git.clone') {
        isGitCloning = true
      } else if (e.type === 'sandbox.ready') {
        isReady = true
        isProvisioning = false
        isGitCloning = false
        sandboxId = e.sandboxId || (e as any).sandbox_id || ''
        hasError = false
        errorMsg = ''
      } else if (e.type === 'sandbox.error') {
        hasError = true
        isProvisioning = false
        isGitCloning = false
        errorMsg = e.error || (e as any).message || 'Unknown sandbox error'
      } else if (e.type === 'sandbox.destroy') {
        isReady = false
      } else if (e.type === 'turn.start' && hasError) {
        // A new turn started after an error — the sandbox recovered
        hasError = false
        errorMsg = ''
      }
    }

    return {
      isReady,
      sandboxId,
      hasError,
      errorMsg,
      isProvisioning,
      isGitCloning,
    }
  }, [events])

  // Collect all sandbox tool calls from both persisted turns and live streaming events
  const sandboxToolCalls = useMemo(() => {
    const calls: Array<{
      key: string
      turnNumber?: number
      toolCallId: string
      toolName: string
      toolArgs?: string
      toolResult?: string
      toolSuccess?: boolean
      toolDurationMs?: number
      status: 'running' | 'done' | 'failed'
      sandboxId?: string
      sandboxExitCode?: number
      sandboxDurationMs?: number
      sandboxParentDurationMs?: number
      sessionId?: string
    }> = []

    // 1. Persisted turns
    for (const turn of orderedTurns) {
      if (!turn.toolCalls) continue
      let tcs: Array<{
        id: string
        function: { name: string; arguments: string }
        result?: string
        success?: boolean
        duration_ms?: number
        sandbox_parent_duration_ms?: number
      }> = []
      try {
        tcs = JSON.parse(turn.toolCalls)
      } catch {
        continue
      }

      for (const tc of tcs) {
        if (!SANDBOX_TOOLS.has(tc.function.name)) continue
        const cached = toolResultsCache[tc.id]
        calls.push({
          key: `persisted-${tc.id}`,
          turnNumber: turn.turnNumber,
          toolCallId: tc.id,
          toolName: tc.function.name,
          toolArgs: tc.function.arguments,
          toolResult: tc.result ?? cached?.toolResult,
          toolSuccess: tc.success ?? cached?.toolSuccess,
          toolDurationMs: tc.duration_ms ?? cached?.toolDurationMs,
          status:
            (tc.success ?? cached?.toolSuccess) === false ? 'failed' : 'done',
          sandboxId: cached?.sandboxId,
          sandboxExitCode: cached?.sandboxExitCode,
          sandboxDurationMs: cached?.sandboxDurationMs,
          sandboxParentDurationMs:
            tc.sandbox_parent_duration_ms ?? cached?.sandboxParentDurationMs,
          sessionId: session.id,
        })
      }
    }

    // 2. Live streaming events
    for (const tc of toolCallStarts) {
      if (!SANDBOX_TOOLS.has(tc.toolName)) continue
      const key = tc.toolCallId
        ? `id:${tc.toolCallId}`
        : `sig:${tc.toolName ?? ''}:${tc.toolArgs ?? ''}`
      const ended = toolCallEndByKey.get(key)
      calls.push({
        key: `live-${tc.toolCallId || key}`,
        toolCallId: tc.toolCallId,
        toolName: tc.toolName,
        toolArgs: tc.toolArgs,
        toolResult: ended?.toolResult,
        toolSuccess: ended?.toolSuccess,
        toolDurationMs: ended?.toolDurationMs,
        status: ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running',
        sandboxId: ended?.sandboxId || sandboxState.sandboxId,
        sandboxExitCode: ended?.sandboxExitCode,
        sandboxDurationMs: ended?.sandboxDurationMs,
        sandboxParentDurationMs: ended?.sandboxParentDurationMs,
        sessionId: session.id,
      })
    }

    return calls
  }, [
    orderedTurns,
    session.id,
    toolCallStarts,
    toolCallEndByKey,
    sandboxState.sandboxId,
  ])

  // Scroll sandbox sheet to bottom on open
  useEffect(() => {
    if (!sandboxSheetOpen) {
      sandboxAutoScroll.current = true
      return
    }
    const timer = setTimeout(() => {
      const el = sandboxSheetRef.current
      if (el) el.scrollTop = el.scrollHeight
    }, 50)
    return () => clearTimeout(timer)
  }, [sandboxSheetOpen])

  // Auto-scroll when new sandbox events arrive (only if user is near bottom)
  useEffect(() => {
    const count = sandboxToolCalls.length
    if (count <= prevSandboxCountRef.current) {
      prevSandboxCountRef.current = count
      return
    }
    prevSandboxCountRef.current = count
    if (!sandboxSheetOpen || !sandboxAutoScroll.current) return
    requestAnimationFrame(() => {
      const el = sandboxSheetRef.current
      if (el) el.scrollTop = el.scrollHeight
    })
  }, [sandboxToolCalls.length, sandboxSheetOpen])

  // Track user scroll to pause auto-scroll when they scroll up
  useEffect(() => {
    const el = sandboxSheetRef.current
    if (!el || !sandboxSheetOpen) return
    const onScroll = () => {
      const distanceFromBottom =
        el.scrollHeight - el.scrollTop - el.clientHeight
      sandboxAutoScroll.current = distanceFromBottom < 80
    }
    el.addEventListener('scroll', onScroll, { passive: true })
    return () => el.removeEventListener('scroll', onScroll)
  }, [sandboxSheetOpen])

  const portExposeEvents = useMemo(() => {
    // Deduplicate by port — keep the latest event per port number
    const byPort = new Map<number, AgentStreamEvent>()
    for (const e of latestTurnEvents) {
      if (e.type !== 'sandbox.port.expose') continue
      const port = (e.data?.port as number) ?? 0
      byPort.set(port, e)
    }
    return Array.from(byPort.values())
  }, [latestTurnEvents])

  // Template selection cards — persist across event clears so cards remain
  // visible after the turn is persisted (events are cleared at that point).
  const [pendingTemplates, setPendingTemplates] = useState<
    TemplateCardData[] | null
  >(null)
  // Once the user clicks a template card, suppress all future template cards
  // for this session. The LLM may call sandbox_list_templates again (e.g. after
  // sandbox_set_template fails), but re-showing the HITL cards is confusing.
  const templateAnsweredRef = useRef(false)

  useEffect(() => {
    if (templateAnsweredRef.current) return

    // Detect template select event → show cards
    const selectEvent = latestTurnEvents.find(
      (e) => e.type === 'sandbox.template.select',
    )
    if (selectEvent) {
      const data = selectEvent.data || {}
      if (data.templates && Array.isArray(data.templates)) {
        setPendingTemplates(
          (data.templates as any[]).map((t: any) => ({
            id: t.id,
            name: t.name,
            slug: t.slug,
            description: t.description,
            icon: t.icon,
            iconColor: t.icon_color,
            image: t.image,
            networkMode: t.network_mode,
          })),
        )
      }
    }

    // Clear cards if sandbox_set_template was called (user already selected)
    const setTemplateCall = latestTurnEvents.find(
      (e) =>
        e.type === 'tool_call.start' && e.toolName === 'sandbox_set_template',
    )
    if (setTemplateCall) {
      setPendingTemplates(null)
    }
  }, [latestTurnEvents])

  const handleTemplateSelect = useCallback(
    (slug: string, name: string) => {
      if (isStreaming) return
      templateAnsweredRef.current = true
      setPendingTemplates(null)
      setPendingMessage(`Use the **${name}** environment`)
      setAutoScroll(true)
      startTurn(`Use the ${name} environment (${slug})`)
    },
    [isStreaming, startTurn],
  )

  const fallbackEvents = useMemo(() => {
    return latestTurnEvents.filter(
      (e) =>
        e.type === 'fallback.triggered' ||
        e.type === 'fallback.succeeded' ||
        e.type === 'fallback.failed',
    )
  }, [latestTurnEvents])

  // Session-level errors (model not found, config errors, etc.). Keep them
  // tied to the current live turn; failed-turn events can remain cached after a
  // later successful persisted turn, and should not keep re-opening the setup card.
  const sessionErrors = useMemo(() => {
    const hasTurnBoundary = latestTurnEvents.some(
      (e) => e.type === 'turn.start',
    )
    return latestTurnEvents.filter((e) => {
      if (e.type !== 'session.error' || !e.error) return false
      if (!hasTurnBoundary && latestFullPersistedTurnNumber > 0) return false
      if (
        !isStreaming &&
        latestFullPersistedTurnNumber > 0 &&
        e.turnNumber > 0 &&
        e.turnNumber < latestFullPersistedTurnNumber
      ) {
        return false
      }
      return true
    })
  }, [isStreaming, latestFullPersistedTurnNumber, latestTurnEvents])
  const missingProviderSessionError = useMemo(() => {
    return sessionErrors.find((event) => {
      const error = event.error?.toLowerCase() ?? ''
      return (
        error.includes('model not found') ||
        error.includes('no provider found for model') ||
        error.includes('may not be activated or configured')
      )
    })
  }, [sessionErrors])
  const missingProviderModelName = useMemo(
    () => extractModelFromProviderError(missingProviderSessionError?.error),
    [missingProviderSessionError?.error],
  )
  const preferredInlineModelName =
    configuredAgentModel || missingProviderModelName
  const suggestedInlineProvider = useMemo(() => {
    if (!preferredInlineModelName) return null
    return (
      missingProviderOptions.find((provider) =>
        (provider.models ?? []).some(
          (model) =>
            model.name === preferredInlineModelName ||
            model.displayName === preferredInlineModelName,
        ),
      ) ?? null
    )
  }, [missingProviderOptions, preferredInlineModelName])
  // Only show the inline provider setup card when the *current* turn
  // actually hit a provider/model error. Previously, `!hasConfiguredProvider`
  // alone would keep the card visible even after the user switched models
  // and the new turn succeeded — the error state was "sticky".
  const showInlineProviderSetup =
    missingProviderOptions.length > 0 && !!missingProviderSessionError
  const shouldReplaceSessionErrorWithProviderSetup =
    showInlineProviderSetup && !!missingProviderSessionError

  // Set initial suggested provider/model — only once, don't override user changes
  useEffect(() => {
    if (inlineProviderUserChanged.current) return
    if (!suggestedInlineProvider?.name) return
    if (inlineProviderName !== suggestedInlineProvider.name) {
      setInlineProviderName(suggestedInlineProvider.name)
    }
  }, [suggestedInlineProvider?.name])

  useEffect(() => {
    if (inlineProviderUserChanged.current) return
    if (!preferredInlineModelName) return
    if (!selectedInlineProvider) return
    if (
      selectedInlineProvider.models?.some(
        (model) => model.name === preferredInlineModelName,
      ) &&
      inlineModelName !== preferredInlineModelName
    ) {
      setInlineModelName(preferredInlineModelName)
    }
  }, [preferredInlineModelName, selectedInlineProvider])

  // User input (ask_user) state
  const [userInputSubmitting, setUserInputSubmitting] = useState(false)
  const [submittedUserInput, setSubmittedUserInput] = useState<{
    inputId: string
    text: string
  } | null>(null)
  const [structuredSelections, setStructuredSelections] = useState<string[]>([])
  const [structuredCustomResponse, setStructuredCustomResponse] = useState('')

  const pendingUserInput = useMemo(() => {
    // Find the latest user_input.requested event that hasn't been resolved
    let latestRequest: PendingUserInputRequest | null = null
    const resolvedIds = new Set<string>()

    for (const e of latestTurnEvents) {
      if (
        e.type === 'user_input.received' ||
        e.type === 'user_input.cancelled'
      ) {
        resolvedIds.add(e.userInputId || (e.data?.input_id as string) || '')
      }
    }

    for (const e of latestTurnEvents) {
      if (e.type === 'user_input.requested') {
        const inputId = e.userInputId || (e.data?.input_id as string) || ''
        if (!resolvedIds.has(inputId)) {
          const inputTypeRaw = (e.data?.input_type as string) || 'text'
          const inputType =
            inputTypeRaw === 'multi_select' || inputTypeRaw === 'single_select'
              ? inputTypeRaw
              : 'text'
          latestRequest = {
            inputId,
            question: (e.data?.question as string) || '',
            inputType,
            options: parseUserInputOptions(e.data?.options),
            allowCustomResponse: !!e.data?.allow_custom_response,
            placeholder: (e.data?.placeholder as string) || '',
            minSelections: Number(e.data?.min_selections || 0),
            maxSelections: Number(e.data?.max_selections || 0),
          }
        }
      }
    }

    return latestRequest
  }, [latestTurnEvents])

  const isHitlMode = !!pendingUserInput && !submittedUserInput

  useEffect(() => {
    setStructuredSelections([])
    setStructuredCustomResponse('')
  }, [pendingUserInput?.inputId])

  useEffect(() => {
    if (!submittedUserInput) return
    if (
      !pendingUserInput ||
      pendingUserInput.inputId !== submittedUserInput.inputId
    ) {
      setSubmittedUserInput(null)
    }
  }, [pendingUserInput, submittedUserInput])

  // Auto-focus composer and scroll to bottom when HITL question arrives
  useEffect(() => {
    if (isHitlMode) {
      composerRef.current?.focus()
      setAutoScroll(true)
      if (scrollRef.current) {
        scrollRef.current.scrollTop = scrollRef.current.scrollHeight
      }
    }
  }, [isHitlMode])

  const handleUserInputSubmit = useCallback(
    async (text: string) => {
      if (!pendingUserInput || userInputSubmitting) return
      const normalizedText = text.trim()
      if (!normalizedText) return
      setUserInputSubmitting(true)
      try {
        const inputId = pendingUserInput.inputId
        if (!inputId) {
          console.error(
            '[handleUserInputSubmit] input_id is empty, pendingUserInput:',
            pendingUserInput,
          )
          return
        }
        setSubmittedUserInput({ inputId, text: normalizedText })
        const res = await fetch(
          `/v1/agents/sessions/${session.id}/user-input`,
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ input_id: inputId, text: normalizedText }),
          },
        )
        if (!res.ok) {
          const errBody = await res.text().catch(() => '')
          console.error('[handleUserInputSubmit] Error:', res.status, errBody)
          setSubmittedUserInput(null)
        }
      } finally {
        setUserInputSubmitting(false)
      }
    },
    [pendingUserInput, userInputSubmitting, session.id],
  )

  const handleStructuredOptionToggle = useCallback(
    (value: string, checked: boolean) => {
      setStructuredSelections((current) => {
        if (!pendingUserInput) return current
        const next = checked
          ? clampSelection(
              [...current.filter((item) => item !== value), value],
              pendingUserInput.maxSelections,
            )
          : current.filter((item) => item !== value)
        return next
      })
    },
    [pendingUserInput],
  )

  const handleStructuredUserInputSubmit = useCallback(async () => {
    if (!pendingUserInput) return
    const submission = buildUserInputSubmission(
      pendingUserInput,
      structuredSelections,
      structuredCustomResponse,
    )
    if (!submission.trim()) return
    if (
      pendingUserInput.inputType === 'multi_select' &&
      pendingUserInput.minSelections > 0 &&
      structuredSelections.length < pendingUserInput.minSelections
    ) {
      toast.error(
        `Select at least ${pendingUserInput.minSelections} option${pendingUserInput.minSelections === 1 ? '' : 's'}`,
      )
      return
    }
    await handleUserInputSubmit(submission)
  }, [
    handleUserInputSubmit,
    pendingUserInput,
    structuredCustomResponse,
    structuredSelections,
  ])

  // Build chronological timeline segments that interleave text with HITL exchanges.
  // This ensures HITL questions appear between the text that came before and after them,
  // maintaining proper chronological order in the chat.
  const timelineSegments = useMemo((): TimelineSegment[] => {
    const segments: TimelineSegment[] = []
    let currentText = ''
    const seenToolSegmentKeys = new Set<string>()
    const visibleSessionErrors = new Set(sessionErrors)

    // Build question lookup
    const questionMap = new Map<string, string>()
    const requestMap = new Map<string, PendingUserInputRequest>()
    for (const e of latestTurnEvents) {
      if (e.type === 'user_input.requested') {
        const inputId = e.userInputId || (e.data?.input_id as string) || ''
        const inputTypeRaw = (e.data?.input_type as string) || 'text'
        const inputType =
          inputTypeRaw === 'multi_select' || inputTypeRaw === 'single_select'
            ? inputTypeRaw
            : 'text'
        const request: PendingUserInputRequest = {
          inputId,
          question: (e.data?.question as string) || '',
          inputType,
          options: parseUserInputOptions(e.data?.options),
          allowCustomResponse: !!e.data?.allow_custom_response,
          placeholder: (e.data?.placeholder as string) || '',
          minSelections: Number(e.data?.min_selections || 0),
          maxSelections: Number(e.data?.max_selections || 0),
        }
        questionMap.set(inputId, request.question)
        requestMap.set(inputId, request)
      }
    }

    // Build resolved/cancelled sets
    const resolvedMap = new Map<string, string>()
    const cancelledMap = new Map<string, string>()
    for (const e of latestTurnEvents) {
      if (e.type === 'user_input.received') {
        const inputId = e.userInputId || (e.data?.input_id as string) || ''
        resolvedMap.set(inputId, (e.data?.response as string) || '')
      }
      if (e.type === 'user_input.cancelled') {
        const inputId = e.userInputId || (e.data?.input_id as string) || ''
        cancelledMap.set(
          inputId,
          e.finishReason || (e.data?.reason as string) || 'timeout',
        )
      }
    }

    const flushText = () => {
      if (currentText) {
        segments.push({ kind: 'text', content: currentText })
        currentText = ''
      }
    }

    for (const e of latestTurnEvents) {
      if (
        e.type === 'llm.start' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth
      ) {
        // New LLM iteration — flush previous text as a separate segment
        flushText()
      }
      if (
        e.type === 'llm.chunk' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth
      ) {
        currentText += e.textDelta
      } else if (
        e.type === 'tool_call.start' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth &&
        e.toolName !== 'ask_user'
      ) {
        flushText()
        const key = e.toolCallId
          ? `id:${e.toolCallId}`
          : `sig:${e.toolName ?? ''}:${e.toolArgs ?? ''}`
        if (seenToolSegmentKeys.has(key)) continue
        seenToolSegmentKeys.add(key)
        const ended = toolCallEndByKey.get(key)
        segments.push({
          kind: 'tool-call',
          toolCallId: e.toolCallId || key,
          toolName: e.toolName,
          toolArgs: e.toolArgs,
          toolResult: ended?.toolResult,
          toolSuccess: ended?.toolSuccess,
          toolDurationMs: ended?.toolDurationMs,
          sandboxId: ended?.sandboxId || sandboxState.sandboxId,
          sandboxExitCode: ended?.sandboxExitCode,
          sandboxDurationMs: ended?.sandboxDurationMs,
          sandboxParentDurationMs: ended?.sandboxParentDurationMs,
          status: ended ? (ended.toolSuccess ? 'done' : 'failed') : 'running',
        })
      } else if (e.type === 'user_input.requested') {
        flushText()
        const inputId = e.userInputId || (e.data?.input_id as string) || ''
        const question = questionMap.get(inputId) || ''
        if (resolvedMap.has(inputId)) {
          segments.push({
            kind: 'hitl-resolved',
            inputId,
            question,
            response: resolvedMap.get(inputId)!,
          })
        } else if (cancelledMap.has(inputId)) {
          segments.push({
            kind: 'hitl-cancelled',
            inputId,
            question,
            reason: cancelledMap.get(inputId)!,
          })
        } else {
          const request = requestMap.get(inputId)
          segments.push({
            kind: 'hitl-pending',
            inputId,
            question,
            inputType: request?.inputType || 'text',
            options: request?.options || [],
            allowCustomResponse: request?.allowCustomResponse || false,
          })
        }
      } else if (e.type === 'system_block' && e.data) {
        flushText()
        segments.push({
          kind: 'system-block',
          data: e.data as Record<string, unknown>,
        })
      } else if (
        e.type === 'fallback.triggered' ||
        e.type === 'fallback.succeeded' ||
        e.type === 'fallback.failed'
      ) {
        flushText()
        segments.push({ kind: 'fallback', event: e })
      } else if (e.type === 'session.error' && e.error) {
        if (!visibleSessionErrors.has(e)) continue
        flushText()
        segments.push({ kind: 'session-error', event: e })
      }
    }
    flushText()

    // Decide which text segments are narration vs final response.
    //
    // During streaming in a tool loop, we can't know if the current text
    // will be followed by more tool calls, so ALL text is treated as
    // narration to avoid the jank of text appearing as an assistant
    // message then jumping into the Reasoning dropdown.
    //
    // After streaming ends, the last text segment is promoted to a
    // real response — unless tool calls came after it (turn ended
    // mid-loop with no final text).
    const textSegments = segments.filter((s) => s.kind === 'text')
    const hasToolCalls = latestTurnEvents.some(
      (e) =>
        e.type === 'tool_call.start' &&
        !(e as any).spawnDepth &&
        !(e as any).spawn_depth,
    )

    let lastTextIdx = -1
    for (let i = segments.length - 1; i >= 0; i--) {
      if (segments[i].kind === 'text') {
        lastTextIdx = i
        break
      }
    }

    // While streaming in a tool loop, all text is narration.
    // After streaming ends, check if the last text had tool calls after it.
    let allNarration: boolean
    if (isStreaming && hasToolCalls) {
      allNarration = true
    } else if (textSegments.length === 0) {
      allNarration = true
    } else {
      // Check if tool calls happened after the last text segment
      const hasToolAfterLastText =
        lastTextIdx >= 0 &&
        latestTurnEvents.some((e, _i, arr) => {
          if (
            e.type !== 'tool_call.start' ||
            (e as any).spawnDepth ||
            (e as any).spawn_depth
          )
            return false
          let llmStartCount = 0
          for (const prev of arr) {
            if (prev === e) break
            if (
              prev.type === 'llm.start' &&
              !(prev as any).spawnDepth &&
              !(prev as any).spawn_depth
            ) {
              llmStartCount++
            }
          }
          return llmStartCount >= textSegments.length
        })
      allNarration = !!hasToolAfterLastText
    }

    // Collect narration parts and build final segment list
    const narrationParts: string[] = []
    const result: TimelineSegment[] = []
    for (let i = 0; i < segments.length; i++) {
      const isNarration =
        segments[i].kind === 'text' && (allNarration || i !== lastTextIdx)
      if (isNarration) {
        narrationParts.push(
          (segments[i] as { kind: 'text'; content: string }).content,
        )
      } else {
        // Insert the grouped narration block right before the final text
        if (i === lastTextIdx && !allNarration && narrationParts.length > 0) {
          result.push({
            kind: 'narration',
            content: narrationParts.join('\n\n'),
          })
        }
        result.push(segments[i])
      }
    }
    // All text is narration (no final response yet) — append as single block
    if (narrationParts.length > 0 && (allNarration || lastTextIdx === -1)) {
      result.push({ kind: 'narration', content: narrationParts.join('\n\n') })
    }

    return result
  }, [
    latestTurnEvents,
    isStreaming,
    sessionErrors,
    toolCallEndByKey,
    sandboxState.sandboxId,
  ])

  const handleDownloadSessionJson = useCallback(() => {
    const exportedAt = new Date().toISOString()
    const payload = {
      schemaVersion: 1,
      exportedAt,
      session,
      renderer: {
        orderedTurns: orderedTurns.map((turn) => ({
          ...turn,
          parsedToolCalls: parseDebugJson(turn.toolCalls),
          parsedTimeline: parseDebugJson(turn.timeline),
        })),
        liveEvents: events,
        latestTurnEvents,
        timelineSegments,
        fallbackEvents,
        sessionErrors,
        missingProviderSessionError,
        missingProviderModelName,
        currentTurnUserMessage,
        currentTurnPersisted,
        liveUserMessageAlreadyPersisted,
        streamingText,
        isStreaming,
        hydrationDone,
        modelState: {
          composerModel,
          configuredAgentModel,
          inlineProviderName,
          inlineModelName,
          selectedInlineProvider: selectedInlineProvider
            ? {
                name: selectedInlineProvider.name,
                displayName: selectedInlineProvider.displayName,
              }
            : null,
        },
        toolResultsCache,
        exposedURLs,
      },
    }
    const safeDate = exportedAt.replace(/[:.]/g, '-')
    downloadJson(
      `agent-session-${session.id.slice(0, 8)}-${safeDate}.json`,
      payload,
    )
    toast.success('Session JSON downloaded')
  }, [
    composerModel,
    configuredAgentModel,
    currentTurnPersisted,
    currentTurnUserMessage,
    events,
    exposedURLs,
    fallbackEvents,
    hydrationDone,
    inlineModelName,
    inlineProviderName,
    isStreaming,
    latestTurnEvents,
    liveUserMessageAlreadyPersisted,
    missingProviderModelName,
    missingProviderSessionError,
    orderedTurns,
    selectedInlineProvider,
    session,
    sessionErrors,
    streamingText,
    timelineSegments,
    toolResultsCache,
  ])

  useEffect(() => {
    const handleRequestedDownload = (event: Event) => {
      const { sessionId } = getAgentSessionJsonDownloadDetail(event)
      if (sessionId && sessionId !== session.id) return
      handleDownloadSessionJson()
    }

    window.addEventListener(
      AGENT_SESSION_JSON_DOWNLOAD_EVENT,
      handleRequestedDownload,
    )
    return () => {
      window.removeEventListener(
        AGENT_SESSION_JSON_DOWNLOAD_EVENT,
        handleRequestedDownload,
      )
    }
  }, [handleDownloadSessionJson, session.id])

  const showBrowserPanel = browserStreamActive && !browserViewerDismissed
  const showWorkflowPanel = workflowPanelData !== null && workflowPanelVisible
  const inlineProviderSetupCard =
    shouldReplaceSessionErrorWithProviderSetup || showInlineProviderSetup ? (
      <div className="rounded border border-red-500/20 bg-brand-main-900/80 p-4 shadow-[0_0_0_1px_rgba(239,68,68,0.04)] sm:p-5">
        <div className="flex items-start gap-3.5">
          <div className="mt-0.5 flex h-10 w-10 shrink-0 items-center justify-center rounded border border-red-500/20 bg-red-500/10 text-red-300">
            <KeyRound className="h-4 w-4" />
          </div>
          <div className="min-w-0 flex-1 space-y-4">
            <div className="space-y-1">
              <p className="text-sm font-medium text-red-300">
                Missing API key
              </p>
              <p className="text-sm leading-6 text-white/65 light:text-black/65">
                Connect a provider and model here, then I will retry your last
                message automatically.
              </p>
              {missingProviderModelName && (
                <p className="text-xs text-white/40 light:text-black/40">
                  Requested model: `{missingProviderModelName}`
                </p>
              )}
            </div>

            <div className="grid gap-3 md:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
              <div className="space-y-1.5">
                <Label className="text-xs uppercase tracking-[0.14em] text-white/40 light:text-black/40">
                  Provider
                </Label>
                <Select
                  value={inlineProviderName}
                  onValueChange={(val) => {
                    inlineProviderUserChanged.current = true
                    setInlineProviderName(val)
                    setInlineModelName('')
                  }}
                >
                  <SelectTrigger className="h-11 w-full border-brand-main-600 bg-brand-main-950 text-white light:text-brand-main-50">
                    <SelectValue placeholder="Select provider" />
                  </SelectTrigger>
                  <SelectContent className="z-9999">
                    {missingProviderOptions.map((provider) => (
                      <SelectItem key={provider.name} value={provider.name}>
                        {provider.displayName || provider.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs uppercase tracking-[0.14em] text-white/40 light:text-black/40">
                  Model
                </Label>
                <Select
                  value={inlineModelName}
                  onValueChange={setInlineModelName}
                  disabled={selectedInlineProviderModels.length === 0}
                >
                  <SelectTrigger className="h-11 w-full border-brand-main-600 bg-brand-main-950 text-white light:text-brand-main-50">
                    <SelectValue placeholder="Select model" />
                  </SelectTrigger>
                  <SelectContent className="z-[9999]">
                    {selectedInlineProviderModels.map((model) => (
                      <SelectItem key={model.name} value={model.name}>
                        {model.displayName || model.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>

            <div className="space-y-1.5">
              <Label
                htmlFor="inline-provider-api-key"
                className="text-xs uppercase tracking-[0.14em] text-white/40 light:text-black/40"
              >
                API Key
              </Label>
              <div className="relative">
                <Input
                  id="inline-provider-api-key"
                  type={showInlineApiKey ? 'text' : 'password'}
                  value={inlineApiKey}
                  onChange={(e) => setInlineApiKey(e.target.value)}
                  placeholder="Paste your provider API key"
                  className="h-11 border-brand-main-600 bg-brand-main-950 pr-12 text-white placeholder:text-white/25 light:text-brand-main-50 light:placeholder:text-black/25"
                />
                <button
                  type="button"
                  onClick={() => setShowInlineApiKey((prev) => !prev)}
                  className="absolute inset-y-0 right-0 flex items-center px-3 text-white/35 transition-colors hover:text-white/60 light:text-black/35 light:hover:text-black/60"
                  aria-label={
                    showInlineApiKey ? 'Hide API key' : 'Show API key'
                  }
                >
                  {showInlineApiKey ? (
                    <EyeOff className="h-4 w-4" />
                  ) : (
                    <Eye className="h-4 w-4" />
                  )}
                </button>
              </div>
              <p className="text-xs text-white/35 light:text-black/35">
                We will enable `{inlineModelName || 'the selected model'}` and
                retry the request.
              </p>
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <Button
                type="button"
                onClick={() => void handleInlineProviderSave()}
                disabled={
                  configureProviderMutation.isPending ||
                  !inlineProviderName ||
                  !inlineModelName ||
                  !inlineApiKey.trim()
                }
              >
                {configureProviderMutation.isPending ? (
                  <>
                    <Loader2 className="mr-2 h-4 w-4 animate-spin" />
                    Saving...
                  </>
                ) : (
                  'Save and retry'
                )}
              </Button>
              <Button
                type="button"
                variant="ghost"
                onClick={() => navigate({ to: '/vault/llm-providers' })}
                className="text-white/55 hover:text-white light:text-black/55 light:hover:text-brand-main-50"
              >
                Open Vault
              </Button>
            </div>
          </div>
        </div>
      </div>
    ) : null

  return (
    <TooltipProvider>
      <div className="flex h-full">
        {/* Main chat column */}
        <div className="flex flex-col flex-1 min-w-0 h-full">
          {/* Status bar */}
          {!hideStatusBar && (
            <div className="shrink-0 flex items-center justify-between px-4 py-2.5 border-b border-brand-main-800/30">
              <div className="flex items-center gap-3">
                <span
                  className={`px-2.5 py-0.5 rounded-full text-[10px] font-medium ${statusStyle.className}`}
                >
                  {statusStyle.label}
                </span>
                <span className="text-xs text-white/30 light:text-black/30">
                  {session.turnCount} turn{session.turnCount !== 1 ? 's' : ''}
                </span>
                <LiveTokenCounter
                  persistedTokens={persistedSessionTokens}
                  events={events}
                  isStreaming={isStreaming}
                />
                {isStreaming && (
                  <span className="flex items-center gap-1.5 text-[10px] text-blue-400">
                    <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
                    Streaming
                  </span>
                )}
                {sandboxState.isProvisioning && !sandboxState.isReady && (
                  <span className="flex items-center gap-1.5 text-[10px] text-brand-secondary-400/70">
                    <Loader2 className="w-3 h-3 animate-spin" />
                    {sandboxState.isGitCloning
                      ? 'Cloning repo...'
                      : 'Provisioning...'}
                  </span>
                )}
                {activitySummary.hasActivity && (
                  <button
                    type="button"
                    onClick={openActivityPanel}
                    className={`inline-flex items-center gap-1.5 rounded border px-2.5 py-1 text-[10px] transition-colors ${
                      runningActivityCount > 0
                        ? 'border-brand-secondary-500/30 bg-brand-secondary-500/10 text-brand-secondary-200'
                        : 'border-brand-main-700/40 bg-brand-main-900/50 text-white/55 hover:border-brand-secondary-500/30 hover:text-white/75 light:text-black/55 light:hover:text-black/75'
                    } light:text-black/55`}
                  >
                    {runningActivityCount > 0 ? (
                      <span className="inline-block h-1.5 w-1.5 rounded-full bg-brand-secondary-300 animate-pulse" />
                    ) : (
                      <Terminal className="w-3 h-3 text-brand-secondary-400/75" />
                    )}
                    {runningActivityCount > 0 ? 'Active tools' : 'Activity'}
                    <span className="rounded bg-brand-main-800/80 px-1.5 py-0.5 text-[10px] font-medium text-white/45 light:text-black/45">
                      {activitySummary.items.length}
                    </span>
                    {runningActivityCount > 0 && (
                      <span className="text-[10px] text-brand-secondary-300/80">
                        {runningActivityCount} live
                      </span>
                    )}
                  </button>
                )}
                {(sandboxState.isReady || sandboxToolCalls.length > 0) && (
                  <button
                    type="button"
                    onClick={() => setSandboxSheetOpen(true)}
                    className="flex items-center gap-1.5 text-[10px] text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors"
                  >
                    {sandboxToolCalls.some((tc) => tc.status === 'running') ? (
                      <Loader2 className="w-3 h-3 animate-spin" />
                    ) : (
                      <Box className="w-3 h-3" />
                    )}
                    Sandbox active
                    {sandboxToolCalls.length > 0 && (
                      <span className="px-1.5 py-0.5 rounded bg-brand-secondary-500/15 text-brand-secondary-300 font-medium">
                        {sandboxToolCalls.length}
                      </span>
                    )}
                  </button>
                )}
                {(() => {
                  const urlEntries = Object.entries(exposedURLs)
                  if (urlEntries.length === 0) return null
                  if (urlEntries.length === 1) {
                    const [, url] = urlEntries[0]
                    return (
                      <a
                        href={url}
                        target="_blank"
                        rel="noopener noreferrer"
                        className="flex items-center gap-1.5 text-[10px] text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors"
                      >
                        <Globe className="w-3 h-3" />
                        Open App
                        <ExternalLink className="w-2.5 h-2.5" />
                      </a>
                    )
                  }
                  return (
                    <div className="relative">
                      <button
                        type="button"
                        onClick={() => setUrlDropdownOpen((v) => !v)}
                        className="flex items-center gap-1.5 text-[10px] text-brand-secondary-400 hover:text-brand-secondary-300 transition-colors"
                      >
                        <Globe className="w-3 h-3" />
                        Open App
                        <ChevronDown className="w-2.5 h-2.5" />
                      </button>
                      {urlDropdownOpen && (
                        <div className="absolute top-full left-0 mt-1 z-50 min-w-[140px] rounded-md border border-brand-main-700/50 bg-brand-main-900 shadow-lg py-1">
                          {urlEntries.map(([port, url]) => (
                            <a
                              key={port}
                              href={url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="flex items-center gap-2 px-3 py-1.5 text-[11px] text-brand-secondary-300 hover:bg-brand-main-800 transition-colors"
                            >
                              Port {port}
                              <ExternalLink className="w-2.5 h-2.5 ml-auto opacity-50" />
                            </a>
                          ))}
                        </div>
                      )}
                    </div>
                  )
                })()}
              </div>
              <div className="flex items-center gap-3">
                {/* Panel toggle buttons */}
                {browserStreamSessionId && (
                  <button
                    type="button"
                    onClick={() => setBrowserViewerDismissed((d) => !d)}
                    className={`flex items-center gap-1 text-[10px] px-2 py-1 rounded-md border transition-colors ${
                      showBrowserPanel
                        ? 'text-brand-secondary-300 border-brand-secondary-500/30 bg-brand-secondary-500/10'
                        : 'text-white/30 border-brand-main-700/30 hover:text-white/50 hover:border-brand-main-600/50 light:text-black/30 light:hover:text-black/50'
                    } light:hover:text-black/50`}
                  >
                    <Iconify.Icon
                      icon="simple-icons:googlechrome"
                      className="w-3 h-3"
                    />
                    Browser
                  </button>
                )}
                {workflowPanelData && (
                  <button
                    type="button"
                    onClick={() =>
                      useSidePanelStore.getState().toggleWorkflowPanel()
                    }
                    className={`flex items-center gap-1 text-[10px] px-2 py-1 rounded-md border transition-colors ${
                      showWorkflowPanel
                        ? 'text-violet-300 border-violet-500/30 bg-violet-500/10'
                        : 'text-white/30 border-brand-main-700/30 hover:text-white/50 hover:border-brand-main-600/50 light:text-black/30 light:hover:text-black/50'
                    } light:hover:text-black/50`}
                  >
                    <Workflow className="w-3 h-3" />
                    Studio
                  </button>
                )}
                <span className="text-[10px] text-white/20 font-mono light:text-black/20">
                  {session.id.slice(0, 12)}
                </span>
                {isActive && !isStreaming && !sessionDormant && (
                  <button
                    type="button"
                    onClick={handleEndSession}
                    disabled={completeMutation.isPending}
                    className="text-[10px] text-white/40 hover:text-white/60 transition-colors px-2.5 py-1 rounded-md border border-brand-main-700/30 hover:border-brand-main-600/50 light:text-black/40 light:hover:text-black/60"
                  >
                    {completeMutation.isPending ? 'Closing...' : 'Close'}
                  </button>
                )}
              </div>
            </div>
          )}

          {/* Chat area */}
          <div
            ref={scrollRef}
            onScroll={handleScroll}
            className="flex-1 overflow-y-auto min-h-0"
          >
            <div className="max-w-3xl mx-auto pl-6 pr-3 py-6 space-y-6">
              {/* Empty state — only show when hydration is complete */}
              {hydrationDone &&
                orderedTurns.length === 0 &&
                !currentTurnUserMessage &&
                events.length === 0 && (
                  <div className="flex flex-col items-center justify-center pt-44 text-brand-main-300">
                    <Sparkles className="w-8 h-8 mb-3 text-brand-secondary-500/30" />
                    <p className="text-sm">
                      Start a conversation with the agent
                    </p>
                  </div>
                )}

              {/* ── Persisted turns ──
                             Only render turns that are FULLY persisted: both userInput AND
                             assistantOutput present. Partially-persisted turns are handled
                             by the live turn block below to avoid duplicate/missing content. */}
              {orderedTurns.map((turn) => {
                // Skip the latest turn if it's not fully persisted — the live block handles it
                if (!turn.userInput || !turn.assistantOutput) return null
                return (
                  <TurnCard
                    key={turn.id}
                    turn={turn}
                    agentId={session.agentId}
                    sessionId={session.id}
                    toolResultsCache={toolResultsCache}
                    hideSandboxTools
                    toolCallView={TIMELINE_PHASE_SUMMARY ? 'summary' : 'full'}
                    showFailedToolCallsOnly={TIMELINE_SHOW_FAILED_ONLY}
                    onCopy={handleCopyTurn}
                    onRetry={handleRetryTurn}
                    onShare={handleShareTurn}
                    onFeedback={handleFeedback}
                    feedbackValue={feedbackByTurn[turn.id] ?? null}
                    disableActions={isStreaming}
                    model={modelByTurn[turn.turnNumber]}
                  />
                )
              })}

              {/* ── Live turn block ──
                             Renders the current in-progress or recently-completed turn from
                             events. This single block owns ALL rendering for the current turn:
                             user message, thinking indicator, streaming text, tool calls, HITL.
                             It stays visible until currentTurnPersisted is true (meaning the
                             TurnCard above is confirmed to be rendering the full turn). */}
              {!currentTurnPersisted &&
                (currentTurnUserMessage || events.length > 0) && (
                  <div className="space-y-4">
                    {/* User message for the current turn.
                                     Suppressed when a persisted TurnCard already renders the same message
                                     to prevent the duplicate bubble during the CQRS handoff. */}
                    {currentTurnUserMessage &&
                      !liveUserMessageAlreadyPersisted &&
                      (() => {
                        const rawMsg = currentTurnUserMessage
                        const {
                          message: msgText,
                          attachments: msgAttachments,
                        } = parseAttachedFiles(rawMsg)
                        return (
                          <div className="flex justify-end">
                            <div className="max-w-[80%] rounded-md bg-brand-secondary-600/20 px-4 pt-1.5 pb-2">
                              {msgText && (
                                <MentionText className="text-sm text-white/90 whitespace-pre-wrap light:text-black/90">
                                  {msgText}
                                </MentionText>
                              )}
                              <AttachedFilesPreview
                                attachments={msgAttachments}
                              />
                            </div>
                          </div>
                        )
                      })()}

                    {/* Thinking indicator — shown before first meaningful event */}
                    {thinkingPhase && (
                      <div className="flex gap-3">
                        <div className="shrink-0 w-6 h-6 rounded-full bg-brand-secondary-500/15 flex items-center justify-center mt-0.5">
                          <Sparkles className="w-3.5 h-3.5 text-brand-secondary-400" />
                        </div>
                        <div className="flex items-center gap-2 text-sm text-white/40 light:text-black/40">
                          <Loader2 className="w-3.5 h-3.5 animate-spin" />
                          <span>{thinkingPhase}</span>
                          <span className="flex gap-0.5">
                            <span className="inline-block w-1 h-1 rounded-full bg-white/30 animate-bounce [animation-delay:0ms] light:bg-black/30" />
                            <span className="inline-block w-1 h-1 rounded-full bg-white/30 animate-bounce [animation-delay:150ms] light:bg-black/30" />
                            <span className="inline-block w-1 h-1 rounded-full bg-white/30 animate-bounce [animation-delay:300ms] light:bg-black/30" />
                          </span>
                        </div>
                      </div>
                    )}
                    {/* Sandbox provisioning indicator */}
                    {sandboxState.isProvisioning && (
                      <div className="flex items-center gap-2.5 rounded-lg px-3 py-2.5 border border-brand-secondary-500/20 bg-brand-secondary-500/5">
                        <Loader2 className="w-4 h-4 text-brand-secondary-400 animate-spin shrink-0" />
                        <div className="min-w-0">
                          <span className="text-xs font-medium text-brand-secondary-300">
                            {sandboxState.isGitCloning
                              ? 'Cloning repository...'
                              : 'Provisioning sandbox...'}
                          </span>
                        </div>
                      </div>
                    )}

                    {/* Sandbox error — suppressed when a sandbox is already available (limit error is about
                                   creating additional sandboxes, not about the existing one being broken) */}
                    {sandboxState.hasError &&
                      !sandboxState.isReady &&
                      (() => {
                        const isLimitError = /limit reached|upgrade/i.test(
                          sandboxState.errorMsg,
                        )
                        return (
                          <div className="rounded bg-red-500/10 px-4 py-3 border border-red-500/15">
                            <div className="flex items-start gap-2.5">
                              <AlertTriangle className="w-4 h-4 text-red-400 shrink-0 mt-0.5" />
                              <div className="min-w-0 flex-1">
                                <p className="text-sm font-medium text-red-300">
                                  {isLimitError
                                    ? 'Persistent agent limit reached'
                                    : 'Sandbox error'}
                                </p>
                                <p className="text-xs text-red-400/80 mt-1">
                                  {isLimitError
                                    ? "You've reached the maximum number of persistent agents for your plan. Upgrade to add more."
                                    : sandboxState.errorMsg}
                                </p>
                                {isLimitError && (
                                  <button
                                    type="button"
                                    onClick={() =>
                                      navigate({
                                        to: '/settings/billing',
                                        search: {
                                          plan: currentTier,
                                          upgrade_success: true,
                                        },
                                      })
                                    }
                                    className="mt-2 inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-brand-main-950 border border-brand-main-500/30 hover:bg-brand-main-900 hover:border-brand-main-500/50 transition-colors text-xs font-medium text-brand-main-50"
                                  >
                                    Upgrade Plan
                                    <ExternalLink className="w-3 h-3" />
                                  </button>
                                )}
                              </div>
                            </div>
                          </div>
                        )
                      })()}

                    {/* Chronological timeline segments: text + HITL interleaved in order */}
                    {timelineSegments.map((seg, segIdx) => {
                      if (seg.kind === 'narration') {
                        // Intermediate LLM reasoning between tool calls — collapsed plain text
                        const isActiveReasoning =
                          isStreaming &&
                          !timelineSegments
                            .slice(segIdx + 1)
                            .some((s) => s.kind === 'text')
                        return (
                          <details
                            key={`seg-narration-${segIdx}`}
                            className="min-w-0 group/narration"
                          >
                            <summary className="flex cursor-pointer items-center gap-2 rounded px-3 py-1.5 text-xs text-white/30 hover:text-white/40 transition-colors select-none list-none [&::-webkit-details-marker]:hidden light:text-black/30 light:hover:text-black/40">
                              <CyclingReasoningIcon
                                active={isActiveReasoning}
                              />
                              <span
                                className={
                                  isActiveReasoning ? 'reasoning-shimmer' : ''
                                }
                              >
                                {isActiveReasoning
                                  ? 'Reasoning...'
                                  : 'Reasoning'}
                              </span>
                              <ChevronDown className="w-3 h-3 ml-auto transition-transform group-open/narration:rotate-180" />
                            </summary>
                            <div
                              className={`mt-1 px-3 text-xs whitespace-pre-wrap leading-relaxed ${isActiveReasoning ? 'text-white/40 reasoning-shimmer light:text-black/40' : 'text-white/30 light:text-black/30'}`}
                            >
                              {seg.content}
                            </div>
                          </details>
                        )
                      }
                      if (seg.kind === 'text') {
                        // Determine if this is the last text segment (for streaming cursor)
                        const isLastTextSegment = !timelineSegments
                          .slice(segIdx + 1)
                          .some((s) => s.kind === 'text')
                        return (
                          <div key={`seg-text-${segIdx}`} className="min-w-0">
                            <div className="w-full rounded-2xl border border-brand-main-700/40 px-4 py-3 text-sm text-white/90 light:text-black/90">
                              <AgentMarkdown
                                variant="chat"
                                isStreaming={isStreaming && isLastTextSegment}
                              >
                                {seg.content}
                              </AgentMarkdown>
                            </div>
                          </div>
                        )
                      }
                      if (seg.kind === 'tool-call') {
                        const isSandboxTool = SANDBOX_TOOLS.has(seg.toolName)
                        return (
                          <ToolCallCard
                            key={`seg-tool-${seg.toolCallId}-${segIdx}`}
                            toolCallId={seg.toolCallId}
                            toolName={seg.toolName}
                            agentId={session.agentId}
                            toolArgs={seg.toolArgs}
                            toolResult={seg.toolResult}
                            toolSuccess={seg.toolSuccess}
                            toolDurationMs={seg.toolDurationMs}
                            status={seg.status}
                            sandboxId={
                              isSandboxTool ? seg.sandboxId : undefined
                            }
                            sandboxExitCode={
                              isSandboxTool ? seg.sandboxExitCode : undefined
                            }
                            sandboxDurationMs={
                              isSandboxTool ? seg.sandboxDurationMs : undefined
                            }
                            sandboxParentDurationMs={
                              isSandboxTool
                                ? seg.sandboxParentDurationMs
                                : undefined
                            }
                            sessionId={isSandboxTool ? session.id : undefined}
                          />
                        )
                      }
                      if (seg.kind === 'hitl-resolved') {
                        return (
                          <div
                            key={`seg-hitl-resolved-${seg.inputId}`}
                            className="space-y-2"
                          >
                            <div className="flex items-start gap-2.5 rounded px-3 py-2.5 border border-brand-secondary-500/20 bg-brand-secondary-500/5">
                              <MessageCircleQuestion className="w-4 h-4 text-brand-secondary-400 shrink-0 mt-0.5" />
                              <div className="text-sm text-white/80 light:text-black/80">
                                {seg.question}
                              </div>
                            </div>
                            <div className="flex justify-end">
                              <div className="max-w-[80%] rounded-2xl bg-brand-secondary-700/15 px-4 py-3">
                                <MentionText className="text-sm text-white/90 whitespace-pre-wrap light:text-black/90">
                                  {seg.response}
                                </MentionText>
                              </div>
                            </div>
                          </div>
                        )
                      }
                      if (seg.kind === 'hitl-cancelled') {
                        return (
                          <div
                            key={`seg-hitl-cancelled-${seg.inputId}`}
                            className="flex items-start gap-2.5 rounded px-3 py-2.5 border border-yellow-500/20 bg-yellow-500/5"
                          >
                            <Clock className="w-4 h-4 text-yellow-400 shrink-0 mt-0.5" />
                            <div>
                              <div className="text-sm text-white/70 light:text-black/70">
                                {seg.question}
                              </div>
                              <div className="text-xs text-yellow-400/70 mt-1">
                                No response received ({seg.reason})
                              </div>
                            </div>
                          </div>
                        )
                      }
                      if (seg.kind === 'hitl-pending') {
                        if (submittedUserInput?.inputId === seg.inputId) {
                          // Show as resolved with the locally submitted response
                          return (
                            <div
                              key={`seg-hitl-submitted-${seg.inputId}`}
                              className="space-y-2"
                            >
                              <div className="flex items-start gap-2.5 rounded px-3 py-2.5 border border-brand-secondary-500/20 bg-brand-secondary-500/5">
                                <MessageCircleQuestion className="w-4 h-4 text-brand-secondary-400 shrink-0 mt-0.5" />
                                <div className="text-sm text-white/80 light:text-black/80">
                                  {seg.question}
                                </div>
                              </div>
                              <div className="flex justify-end">
                                <div className="max-w-[80%] rounded-2xl bg-brand-secondary-700/15 px-4 py-3">
                                  <MentionText className="text-sm text-white/90 whitespace-pre-wrap light:text-black/90">
                                    {submittedUserInput.text}
                                  </MentionText>
                                </div>
                              </div>
                            </div>
                          )
                        }
                        // Pending HITL: show question in timeline, input is in the composer
                        if (
                          session.status === SessionStatus.RUNNING ||
                          session.status === SessionStatus.WAITING_FOR_INPUT ||
                          isStreaming
                        ) {
                          return (
                            <div
                              key={`seg-hitl-pending-${seg.inputId}`}
                              className="flex items-start gap-2.5 rounded-lg px-3 py-2.5 border border-brand-secondary-500/20 bg-brand-secondary-500/5"
                            >
                              <MessageCircleQuestion className="w-4 h-4 text-brand-secondary-400 shrink-0 mt-0.5" />
                              <div className="flex-1 min-w-0">
                                <div className="text-sm text-white/80 light:text-black/80">
                                  {seg.question}
                                </div>
                                <div className="flex items-center gap-1.5 mt-1.5 text-[10px] text-brand-secondary-400/50">
                                  <ChevronDown className="w-3 h-3" />
                                  Answer using the question card below
                                </div>
                              </div>
                            </div>
                          )
                        }
                      }
                      if (seg.kind === 'system-block') {
                        return (
                          <div key={`seg-sysblock-${segIdx}`}>
                            <SystemBlockRenderer data={seg.data} />
                          </div>
                        )
                      }
                      if (seg.kind === 'fallback') {
                        const fe = seg.event
                        return (
                          <div
                            key={`seg-fallback-${segIdx}`}
                            className={`flex items-center gap-2 rounded px-3 py-2 text-xs border ${
                              fe.type === 'fallback.succeeded'
                                ? 'bg-green-500/10 text-green-400 border-green-500/15'
                                : fe.type === 'fallback.failed'
                                  ? 'bg-red-500/10 text-red-400 border-red-500/15'
                                  : 'bg-yellow-500/10 text-yellow-400 border-yellow-500/15'
                            }`}
                          >
                            <AlertTriangle className="w-3.5 h-3.5 shrink-0" />
                            <span>
                              {fe.type === 'fallback.triggered' && (
                                <>
                                  Fallback: {fe.fallbackFromModel}
                                  {' -> '}
                                  {fe.fallbackToModel} (attempt{' '}
                                  {fe.fallbackAttempt})
                                </>
                              )}
                              {fe.type === 'fallback.succeeded' && (
                                <>
                                  Fallback succeeded: using {fe.fallbackToModel}
                                </>
                              )}
                              {fe.type === 'fallback.failed' && (
                                <>Fallback exhausted: all models failed</>
                              )}
                            </span>
                          </div>
                        )
                      }
                      if (seg.kind === 'session-error') {
                        const se = seg.event
                        if (
                          shouldReplaceSessionErrorWithProviderSetup &&
                          se === missingProviderSessionError
                        ) {
                          return (
                            <div key={`seg-session-error-${segIdx}`}>
                              {inlineProviderSetupCard}
                            </div>
                          )
                        }
                        const isMaxTurns = se.error
                          ?.toLowerCase()
                          .includes('maximum turns exceeded')
                        return (
                          <div
                            key={`seg-session-error-${segIdx}`}
                            className={`rounded px-4 py-3 border ${isMaxTurns ? 'bg-amber-500/10 border-amber-500/20' : 'bg-red-500/10 border-red-500/20'}`}
                          >
                            <div className="flex items-start gap-2.5">
                              <AlertTriangle
                                className={`w-4 h-4 shrink-0 mt-0.5 ${isMaxTurns ? 'text-amber-400' : 'text-red-400'}`}
                              />
                              <div className="min-w-0">
                                <p
                                  className={`text-sm font-medium ${isMaxTurns ? 'text-amber-300' : 'text-red-300'}`}
                                >
                                  {isMaxTurns
                                    ? 'Session Limit Reached'
                                    : 'Session Error'}
                                </p>
                                <p
                                  className={`text-sm mt-1 break-all ${isMaxTurns ? 'text-amber-400/90' : 'text-red-400/90'}`}
                                >
                                  {isMaxTurns
                                    ? 'This session has reached its maximum number of turns. Please start a new session to continue.'
                                    : se.error}
                                </p>
                              </div>
                            </div>
                          </div>
                        )
                      }
                      return null
                    })}

                    {/* Spawn cards */}
                    {spawnGroups.map((spawn) => (
                      <SpawnCard
                        key={spawn.key}
                        task={spawn.task}
                        agentId={spawn.agentId}
                        events={spawn.events}
                        status={spawn.status}
                        tokensUsed={spawn.tokensUsed}
                      />
                    ))}

                    {/* Port exposure events */}
                    {portExposeEvents.map((pe) => {
                      const data = pe.data || {}
                      const url = data.url as string | undefined
                      const port = data.port as number | undefined
                      return (
                        <div
                          key={`port-${port}`}
                          className="rounded-md border border-brand-secondary-500/20 bg-brand-secondary-500/[0.06] overflow-hidden"
                        >
                          <div className="flex items-center gap-2.5 px-3 py-2">
                            <span className="flex items-center justify-center w-6 h-6 rounded-md bg-brand-secondary-500/15 shrink-0">
                              <Globe className="w-3.5 h-3.5 text-brand-secondary-400" />
                            </span>
                            <span className="text-xs font-medium text-brand-secondary-200">
                              Port {port} exposed
                            </span>
                            <span className="px-1.5 py-0.5 rounded text-[10px] font-mono font-medium bg-brand-secondary-500/15 text-brand-secondary-300">
                              :{port}
                            </span>
                          </div>
                          {url && (
                            <a
                              href={url}
                              target="_blank"
                              rel="noopener noreferrer"
                              className="flex items-center gap-2 px-3 py-2 border-t border-brand-secondary-500/10 bg-black/20 group min-w-0"
                            >
                              <ExternalLink className="w-3 h-3 text-brand-secondary-400/60 group-hover:text-brand-secondary-300 shrink-0 transition-colors" />
                              <code className="text-[11px] font-mono text-brand-secondary-300/80 group-hover:text-brand-secondary-200 truncate transition-colors">
                                {url}
                              </code>
                            </a>
                          )}
                        </div>
                      )
                    })}

                    {/* HITL blocks are now rendered chronologically via timelineSegments above */}
                  </div>
                )}

              {/* Template selection cards — rendered outside events block
                        so they persist after streaming events are cleared */}
              {pendingTemplates && pendingTemplates.length > 0 && (
                <div className="space-y-2">
                  <div className="flex items-center gap-2">
                    <Box className="w-3 h-3 text-brand-secondary-400/70" />
                    <span className="text-[11px] text-white/50 light:text-black/50">
                      Pick an environment
                    </span>
                  </div>
                  <div className="flex flex-wrap gap-1.5">
                    {pendingTemplates.map((tpl) => (
                      <button
                        key={tpl.id}
                        type="button"
                        onClick={() => handleTemplateSelect(tpl.slug, tpl.name)}
                        className="inline-flex items-center gap-1.5 px-2.5 py-1.5 rounded-md border border-brand-main-600 bg-brand-main-800/40 hover:border-brand-secondary-500/60 hover:bg-brand-main-700/50 transition-colors text-left"
                      >
                        <Iconify.Icon
                          icon={tpl.icon}
                          className="w-3.5 h-3.5 shrink-0"
                          style={{ color: tpl.iconColor }}
                        />
                        <span className="text-xs font-medium text-white/90 light:text-black/90">
                          {tpl.name}
                        </span>
                        <span className="text-[10px] text-white/35 light:text-black/35">
                          {tpl.description}
                        </span>
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>

            {/* Scroll-to-bottom button */}
            {!autoScroll && (
              <button
                type="button"
                onClick={scrollToBottom}
                className="sticky bottom-4 left-1/2 -translate-x-1/2 flex items-center gap-1.5 px-3 py-1.5 rounded-full bg-brand-main-800/90 border border-brand-main-700/50 text-xs text-white/50 hover:text-white/70 transition-colors shadow-lg backdrop-blur-sm light:text-black/50 light:hover:text-black/70"
              >
                <ChevronDown className="w-3 h-3" />
                New messages
              </button>
            )}
          </div>

          {/* Input area */}
          {isActive && (
            <div className="shrink-0 border-t border-brand-main-800/30">
              <div className="max-w-3xl mx-auto px-4 py-3">
                {/* Queued message banner */}
                {queuedMessage && (
                  <div className="mb-2 flex items-start gap-2 rounded border border-brand-main-700/40 bg-brand-main-900/60 px-3 py-2">
                    <div className="flex items-center gap-1.5 shrink-0 mt-0.5">
                      <Loader2 className="w-3 h-3 animate-spin text-white/30 light:text-black/30" />
                      <span className="text-[10px] font-medium uppercase tracking-wider text-white/30 light:text-black/30">
                        Queued
                      </span>
                    </div>
                    <MentionText className="text-sm text-white/50 whitespace-pre-wrap wrap-break-words min-w-0 flex-1 light:text-black/50">
                      {queuedMessage}
                    </MentionText>
                    <button
                      type="button"
                      onClick={() => forceSendQueuedRef.current?.()}
                      className="shrink-0 px-2 py-0.5 rounded text-[10px] font-medium text-brand-secondary-300/70 hover:text-brand-secondary-200 border border-brand-secondary-500/20 hover:border-brand-secondary-500/40 bg-brand-secondary-500/5 hover:bg-brand-secondary-500/10 transition-colors"
                      title="Stop current response and send this message now"
                    >
                      Send now
                    </button>
                    <button
                      type="button"
                      onClick={() => setQueuedMessage(null)}
                      className="shrink-0 p-0.5 rounded text-white/20 hover:text-white/50 transition-colors light:text-black/20 light:hover:text-black/50"
                      title="Cancel queued message"
                    >
                      <X className="w-3.5 h-3.5" />
                    </button>
                  </div>
                )}

                {/* HITL question banner in composer */}
                {pendingUserInput && !submittedUserInput && (
                  <div className="mb-2 rounded-lg border border-brand-secondary-500/30 bg-brand-secondary-500/5 p-2.5 space-y-2">
                    <div className="flex items-start gap-2">
                      <MessageCircleQuestion className="mt-0.5 h-4 w-4 shrink-0 text-brand-secondary-400" />
                      <div className="flex-1 min-w-0">
                        <div className="mb-1 text-[10px] font-medium uppercase tracking-wider text-brand-secondary-400/70">
                          Agent needs your input
                        </div>
                        <div className="text-sm leading-5 text-white/85 light:text-black/85">
                          {pendingUserInput.question}
                        </div>
                      </div>
                    </div>
                    <div className="max-h-56 space-y-2 overflow-y-auto pl-6 pr-1">
                      {pendingUserInput.inputType === 'single_select' &&
                        pendingUserInput.options.length > 0 && (
                          <div className="grid gap-1.5 sm:grid-cols-2">
                            {pendingUserInput.options.map((opt) => {
                              const selected = structuredSelections.includes(
                                opt.value,
                              )
                              return (
                                <button
                                  key={opt.value}
                                  type="button"
                                  onClick={() =>
                                    setStructuredSelections([opt.value])
                                  }
                                  disabled={userInputSubmitting}
                                  className={`w-full rounded-md border px-2.5 py-2 text-left transition-colors ${
                                    selected
                                      ? 'border-brand-secondary-400/60 bg-brand-secondary-500/15 text-white light:text-brand-main-50'
                                      : 'border-brand-secondary-500/20 bg-brand-main-950/60 text-white/80 hover:border-brand-secondary-500/50 hover:bg-brand-secondary-500/10 light:text-black/80'
                                  } disabled:opacity-50 light:text-black/80`}
                                >
                                  <div className="text-xs font-medium leading-4">
                                    {opt.label}
                                  </div>
                                  {opt.description && (
                                    <div className="mt-0.5 text-[11px] leading-4 text-white/45 light:text-black/45">
                                      {opt.description}
                                    </div>
                                  )}
                                </button>
                              )
                            })}
                          </div>
                        )}

                      {pendingUserInput.inputType === 'multi_select' &&
                        pendingUserInput.options.length > 0 && (
                          <div className="space-y-1.5 rounded-md border border-brand-main-700/40 bg-brand-main-950/50 p-2.5">
                            {pendingUserInput.options.map((opt) => (
                              <label
                                key={opt.value}
                                className="flex items-start gap-2 text-sm text-white/85 light:text-black/85"
                              >
                                <Checkbox
                                  checked={structuredSelections.includes(
                                    opt.value,
                                  )}
                                  onCheckedChange={(checked) =>
                                    handleStructuredOptionToggle(
                                      opt.value,
                                      checked === true,
                                    )
                                  }
                                  disabled={
                                    userInputSubmitting ||
                                    (pendingUserInput.maxSelections > 0 &&
                                      !structuredSelections.includes(
                                        opt.value,
                                      ) &&
                                      structuredSelections.length >=
                                        pendingUserInput.maxSelections)
                                  }
                                  className="mt-0.5 border-brand-secondary-500/40 data-[state=checked]:bg-brand-secondary-500 data-[state=checked]:text-brand-main-950"
                                />
                                <span className="min-w-0">
                                  <span className="block text-xs font-medium leading-4">
                                    {opt.label}
                                  </span>
                                  {opt.description && (
                                    <span className="mt-0.5 block text-[11px] leading-4 text-white/45 light:text-black/45">
                                      {opt.description}
                                    </span>
                                  )}
                                </span>
                              </label>
                            ))}
                            {(pendingUserInput.minSelections > 0 ||
                              pendingUserInput.maxSelections > 0) && (
                              <div className="pt-0.5 text-[11px] text-white/40 light:text-black/40">
                                {pendingUserInput.minSelections > 0
                                  ? `Choose at least ${pendingUserInput.minSelections}. `
                                  : ''}
                                {pendingUserInput.maxSelections > 0
                                  ? `Up to ${pendingUserInput.maxSelections} selections.`
                                  : ''}
                              </div>
                            )}
                          </div>
                        )}

                      {(pendingUserInput.inputType === 'text' ||
                        pendingUserInput.allowCustomResponse) && (
                        <Input
                          value={structuredCustomResponse}
                          onChange={(event) =>
                            setStructuredCustomResponse(event.target.value)
                          }
                          placeholder={
                            pendingUserInput.placeholder ||
                            'Type your response...'
                          }
                          disabled={userInputSubmitting}
                          className="h-9 border-brand-main-700 bg-brand-main-950/70 text-sm text-white placeholder:text-white/30 light:text-brand-main-50 light:placeholder:text-black/30"
                        />
                      )}

                      <div className="flex items-center justify-between gap-2 pt-0.5">
                        <div className="text-[11px] text-white/35 light:text-black/35">
                          {pendingUserInput.inputType === 'multi_select'
                            ? 'Select options and submit'
                            : pendingUserInput.inputType === 'single_select'
                              ? 'Pick one option or type a custom answer'
                              : 'Submit your answer to continue'}
                        </div>
                        <Button
                          type="button"
                          size="sm"
                          onClick={() => void handleStructuredUserInputSubmit()}
                          disabled={
                            userInputSubmitting ||
                            (!structuredCustomResponse.trim() &&
                              structuredSelections.length === 0)
                          }
                          className="h-8 px-3 text-xs"
                        >
                          Submit answer
                        </Button>
                      </div>
                    </div>
                  </div>
                )}
                {!shouldReplaceSessionErrorWithProviderSetup &&
                  showInlineProviderSetup && (
                    <div className="mb-3">{inlineProviderSetupCard}</div>
                  )}
                <div
                  className={`relative rounded border bg-brand-main-950 transition-colors focus-within:ring-1 ${isHitlMode ? 'border-brand-secondary-500/40 focus-within:ring-brand-secondary-500/60' : 'border-brand-main-600 focus-within:ring-brand-secondary-500'}`}
                >
                  {mentionOpen && showAgentMentionDropdown && (
                    <div className="absolute bottom-full left-0 right-0 mb-1 z-50 bg-brand-main-900 border border-brand-main-600 rounded shadow-xl overflow-hidden">
                      <div className="flex items-center gap-2 px-3 py-1.5 border-b border-brand-main-700/50 text-[11px] text-white/40 light:text-black/40">
                        <span>Subagents</span>
                        <span className="text-white/25 light:text-black/25">
                          ({agentMentionSuggestions.length})
                        </span>
                      </div>
                      <div className="max-h-[240px] overflow-y-auto">
                        {agentMentionSuggestions.map((item, index) => (
                          <button
                            key={item.agent.id}
                            type="button"
                            onClick={() => handleAgentMentionSelect(item)}
                            onMouseEnter={() =>
                              setSelectedAgentMentionIndex(index)
                            }
                            className={`flex w-full items-center gap-2 px-3 py-2 text-left text-xs transition-colors ${
                              index === selectedAgentMentionIndex
                                ? 'bg-brand-main-700/50'
                                : 'hover:bg-brand-main-800/50'
                            }`}
                          >
                            <span
                              className="h-2 w-2 shrink-0 rounded-full border border-white/20 light:border-black/20"
                              style={{
                                backgroundColor: item.agent.color || '#64748b',
                              }}
                            />
                            <span className="truncate text-white/85 light:text-black/85">
                              {item.agent.name}
                            </span>
                            {item.agent.mentionAlias && (
                              <span className="rounded bg-brand-main-800/80 px-1 py-0.5 text-[10px] text-white/50 light:text-black/50">
                                @{item.agent.mentionAlias}
                              </span>
                            )}
                            <span className="ml-auto text-[10px] text-white/30 light:text-black/30">
                              insert
                            </span>
                          </button>
                        ))}
                      </div>
                    </div>
                  )}
                  {mentionOpen && !showAgentMentionDropdown && (
                    <FileMentionDropdown
                      ref={mentionDropdownRef}
                      sessionId={session.id}
                      isOpen={mentionOpen}
                      filter={mentionFilter}
                      onSelect={handleMentionSelect}
                      onClose={() => setMentionOpen(false)}
                      repoContext={mentionRepoContext}
                    />
                  )}
                  <MentionComposerInput
                    ref={composerRef}
                    value={userInput}
                    onValueCursorChange={handleInputChange}
                    onKeyDown={handleKeyDown}
                    placeholder={
                      pendingUserInput && !submittedUserInput
                        ? pendingUserInput.placeholder ||
                          'Type your response...'
                        : isStreaming
                          ? 'Type a message to queue for after this turn...'
                          : sessionDormant
                            ? 'Send a message to resume this session...'
                            : 'Message agent... (type @ for files or subagents)'
                    }
                  />
                  {/* Attached files preview */}
                  {attachedFiles.length > 0 && (
                    <div className="flex flex-wrap gap-1.5 px-3 py-1.5 border-t border-brand-main-700/40">
                      {attachedFiles.map((af, idx) => {
                        const isImage = af.file.type.startsWith('image/')
                        return (
                          <Popover key={idx}>
                            <PopoverTrigger asChild>
                              <span className="inline-flex items-center gap-1 rounded bg-brand-main-800 px-2 py-0.5 text-xs text-white/70 cursor-pointer hover:bg-brand-main-700 transition-colors light:text-black/70">
                                {af.uploading ? (
                                  <Loader2 className="w-3 h-3 animate-spin" />
                                ) : isImage ? (
                                  <ImageIcon className="w-3 h-3 text-white/40 light:text-black/40" />
                                ) : (
                                  <Paperclip className="w-3 h-3 text-white/40 light:text-black/40" />
                                )}
                                <span className="truncate max-w-[120px]">
                                  {af.file.name}
                                </span>
                                <button
                                  type="button"
                                  onClick={(e) => {
                                    e.stopPropagation()
                                    removeAttachedFile(af.file)
                                  }}
                                  className="text-white/30 hover:text-red-400 light:text-black/30"
                                >
                                  <X className="w-3 h-3" />
                                </button>
                              </span>
                            </PopoverTrigger>
                            <PopoverContent
                              side="top"
                              align="start"
                              className="w-auto max-w-xs p-2 bg-brand-main-900 border border-brand-main-700 rounded-lg"
                            >
                              {isImage ? (
                                <img
                                  src={URL.createObjectURL(af.file)}
                                  alt={af.file.name}
                                  className="max-w-[300px] max-h-[300px] rounded object-contain"
                                  onLoad={(e) =>
                                    URL.revokeObjectURL(
                                      (e.target as HTMLImageElement).src,
                                    )
                                  }
                                />
                              ) : (
                                <div className="space-y-1">
                                  <div className="flex items-center gap-2 text-xs text-white/70 light:text-black/70">
                                    <Paperclip className="w-3.5 h-3.5 text-white/40 light:text-black/40" />
                                    <span className="font-medium truncate">
                                      {af.file.name}
                                    </span>
                                  </div>
                                  <div className="text-[10px] text-white/40 light:text-black/40">
                                    {af.file.type || 'Unknown type'} &middot;{' '}
                                    {(af.file.size / 1024).toFixed(1)} KB
                                  </div>
                                </div>
                              )}
                            </PopoverContent>
                          </Popover>
                        )
                      })}
                    </div>
                  )}
                  <div className="flex items-center justify-between px-2 pb-2">
                    <div className="flex items-center gap-1">
                      <ContextSelector
                        value={composerContext}
                        onChange={setComposerContext}
                      />
                      <input
                        ref={fileInputRef}
                        type="file"
                        multiple
                        className="hidden"
                        onChange={handleFileAttach}
                      />
                      <ComposerToolsPopover
                        onAttachFiles={() => fileInputRef.current?.click()}
                        onAttachImages={() => {
                          const input = document.createElement('input')
                          input.type = 'file'
                          input.accept = 'image/*'
                          input.multiple = true
                          input.onchange = (e) => handleFileAttach(e as any)
                          input.click()
                        }}
                        webSearchEnabled={webSearchEnabled}
                        webSearchAvailable={webSearchAvailable}
                        onToggleWebSearch={() => setWebSearchEnabled((v) => !v)}
                      />
                    </div>
                    <div className="flex items-center gap-1.5">
                      {isStreaming && (
                        <Button
                          size="xs"
                          variant="default"
                          type="button"
                          onClick={handleStop}
                          className="bg-red-500/15 hover:bg-red-500/25 text-red-400 border-red-500/20"
                        >
                          <Square className="w-3.5 h-3.5" />
                          <span className="ml-1 text-xs">Stop</span>
                        </Button>
                      )}
                      {!isStreaming && userInput.trim() && (
                        <span className="text-[11px] text-brand-main-300 font-light hidden sm:flex items-center gap-1 select-none">
                          <kbd className="bg-white/10 px-1.5 py-0.5 rounded text-[10px] font-mono opacity-50 light:bg-black/10">
                            ↵
                          </kbd>
                          to {isHitlMode ? 'respond' : 'send'} ·
                          <kbd className="bg-white/10 px-1.5 py-0.5 rounded text-[10px] font-mono opacity-50 light:bg-black/10">
                            ⇧↵
                          </kbd>
                          for newline
                        </span>
                      )}
                      <ContextWindowIndicator
                        events={events}
                        orderedTurns={orderedTurns}
                        maxContextTokens={maxContextTokens}
                      />
                      <ModelPicker
                        value={composerModel}
                        onChange={handleModelChange}
                        variant="compact"
                      />
                      {
                        <Button
                          size="xs"
                          variant={
                            !userInput.trim().length ? 'ghost' : 'default'
                          }
                          type="button"
                          onClick={handleSend}
                          disabled={
                            (isStreaming && queuedMessage !== null) ||
                            (userInputSubmitting && !userInput.trim().length)
                          }
                          title={
                            isHitlMode
                              ? 'Send response to agent'
                              : isStreaming
                                ? 'Queue message for after this turn'
                                : undefined
                          }
                          className={cn(
                            isHitlMode
                              ? 'bg-brand-secondary-500/20 hover:bg-brand-secondary-500/30 text-brand-secondary-400 border-brand-secondary-500/30'
                              : undefined,
                          )}
                        >
                          {userInputSubmitting ? (
                            <Loader2 className="w-4 h-4 animate-spin" />
                          ) : (
                            <ArrowUp className="w-4 h-4" />
                          )}
                        </Button>
                      }
                    </div>
                  </div>
                </div>
                <div className="mt-2 px-1">
                  <div className="flex flex-wrap items-center justify-between gap-2 text-[11px]">
                    <div className="flex flex-wrap items-center gap-1.5">
                      <span className="inline-flex items-center gap-1.5 h-7 rounded border border-brand-main-700/70 bg-brand-main-900/55 px-2 text-[11px] text-white/70 light:text-black/70">
                        <Box className="w-3 h-3 text-white/50 light:text-black/50" />
                        Local
                      </span>
                      <ComposerDropdown
                        label="Path"
                        value={contextPath}
                        options={contextPathOptions.map((p) => ({
                          value: p,
                          label: p,
                        }))}
                        onChange={setContextPath}
                        mono
                      />
                      <ComposerDropdown
                        label="Permissions"
                        value={permissionProfile}
                        options={[
                          { value: 'default', label: 'Default' },
                          { value: 'read_only', label: 'Read only' },
                          { value: 'elevated', label: 'Elevated' },
                        ]}
                        onChange={setPermissionProfile}
                      />
                    </div>
                    <div className="flex items-center gap-1.5">
                      <button
                        type="button"
                        onClick={() => setContextEditorOpen((v) => !v)}
                        className="inline-flex items-center gap-1.5 h-7 rounded border border-brand-main-700/70 bg-brand-main-900/55 px-2 text-[11px] text-white/70 hover:text-white/90 hover:border-brand-main-600 transition-colors light:text-black/70 light:hover:text-black/90"
                      >
                        <GitBranch className="w-3 h-3 text-white/50 light:text-black/50" />
                        <span className="font-mono text-[11px]">
                          {branchInput.trim() ||
                            sessionRepoContext.branch ||
                            'default'}
                        </span>
                        <ChevronDown
                          className={`w-3 h-3 text-white/40 transition-transform light:text-black/40${contextEditorOpen ? 'rotate-180' : ''} light:text-black/40`}
                        />
                      </button>
                      {contextSaveStatus === 'saving' && (
                        <Loader2 className="w-3 h-3 text-white/30 animate-spin light:text-black/30" />
                      )}
                      {contextSaveStatus === 'saved' && (
                        <span className="text-[10px] text-brand-secondary-400">
                          Saved
                        </span>
                      )}
                      {contextSaveStatus === 'error' && (
                        <span className="text-[10px] text-red-400">Failed</span>
                      )}
                    </div>
                  </div>
                  {contextEditorOpen &&
                    (isGitHubConnected ? (
                      <div className="mt-2 grid grid-cols-1 sm:grid-cols-2 gap-2 rounded-md border border-brand-main-700/50 bg-brand-main-900/35 p-2">
                        <div className="space-y-1">
                          <span className="text-[10px] text-white/40 light:text-black/40">
                            Repository
                          </span>
                          {gitInstallationId > 0 && gitHubRepos.length > 0 ? (
                            <Select
                              value={parsedRepo?.fullName ?? '__custom__'}
                              onValueChange={(value) => {
                                if (value === '__custom__') return
                                setRepoInput(value)
                                const selectedRepo = gitHubRepos.find(
                                  (r) => r.fullName === value,
                                )
                                if (selectedRepo && !branchInput) {
                                  setBranchInput(
                                    selectedRepo.defaultBranch || '',
                                  )
                                }
                              }}
                            >
                              <SelectTrigger
                                size="sm"
                                className="h-8 w-full border-brand-main-700/70 bg-brand-main-950/70 text-[11px] font-mono text-white/80 light:text-black/80"
                              >
                                <SelectValue placeholder="Select repository" />
                              </SelectTrigger>
                              <SelectContent>
                                {gitHubRepos.map((repo) => (
                                  <SelectItem
                                    key={repo.id}
                                    value={repo.fullName}
                                  >
                                    {repo.fullName}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          ) : (
                            <Select disabled>
                              <SelectTrigger
                                size="sm"
                                className="h-8 w-full border-brand-main-700/70 bg-brand-main-950/70 text-[11px] font-mono text-white/80 opacity-50 light:text-black/80"
                              >
                                <SelectValue placeholder="Select repository" />
                              </SelectTrigger>
                              <SelectContent />
                            </Select>
                          )}
                        </div>
                        <div className="space-y-1">
                          <span className="text-[10px] text-white/40 light:text-black/40">
                            Branch
                          </span>
                          {gitInstallationId > 0 &&
                          parsedRepo &&
                          gitHubBranches.length > 0 ? (
                            <Select
                              value={branchInput || '__default__'}
                              onValueChange={(value) =>
                                setBranchInput(
                                  value === '__default__' ? '' : value,
                                )
                              }
                            >
                              <SelectTrigger
                                size="sm"
                                className="h-8 w-full border-brand-main-700/70 bg-brand-main-950/70 text-[11px] font-mono text-white/80 light:text-black/80"
                              >
                                <SelectValue placeholder="Select branch" />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="__default__">
                                  Default branch
                                </SelectItem>
                                {gitHubBranches.map((branch) => (
                                  <SelectItem
                                    key={branch.name}
                                    value={branch.name}
                                  >
                                    {branch.name}
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          ) : (
                            <Select disabled>
                              <SelectTrigger
                                size="sm"
                                className="h-8 w-full border-brand-main-700/70 bg-brand-main-950/70 text-[11px] font-mono text-white/80 opacity-50 light:text-black/80"
                              >
                                <SelectValue placeholder="Select branch" />
                              </SelectTrigger>
                              <SelectContent />
                            </Select>
                          )}
                        </div>
                      </div>
                    ) : (
                      <div className="mt-2 flex items-center gap-3 rounded-md border border-brand-main-700/50 bg-brand-main-900/35 p-3">
                        <Iconify.Icon
                          icon="mdi:github"
                          className="w-5 h-5 text-white/40 shrink-0 light:text-black/40"
                        />
                        <div className="flex-1 min-w-0">
                          <p className="text-[11px] text-white/60 light:text-black/60">
                            Connect GitHub to select repositories and branches.
                          </p>
                        </div>
                        <Button
                          size="sm"
                          variant="outline"
                          className="h-7 text-[11px] shrink-0"
                          onClick={() =>
                            navigate({
                              to: '/settings/integrations',
                              search: { integration: 'github' },
                            })
                          }
                        >
                          Connect
                        </Button>
                      </div>
                    ))}
                  {/* <div className="mt-1.5 text-[10px] text-white/35 light:text-black/35">
                    {sessionRepoContext.repo
                      ? `Using repo ${sessionRepoContext.repo} at ${contextPath}.`
                      : `Using ${contextPath}.`}
                  </div> */}
                </div>
              </div>
            </div>
          )}

          <Sheet open={activitySheetOpen} onOpenChange={setActivitySheetOpen}>
            <SheetContent
              side="right"
              className="sm:max-w-md"
              overlayClassName="bg-transparent"
            >
              <SheetHeader>
                <SheetTitle className="flex items-center gap-2 text-base">
                  <Terminal className="w-4 h-4 text-brand-secondary-400" />
                  Session Activity
                  <span className="px-1.5 py-0.5 rounded bg-brand-main-800/70 text-white/50 text-xs font-medium light:text-black/50">
                    {activitySummary.items.length}
                  </span>
                </SheetTitle>
              </SheetHeader>
              <SheetBody className="py-4">
                <div className="space-y-4">
                  <div className="flex flex-wrap gap-2">
                    {activitySummary.totalTools > 0 && (
                      <span className="inline-flex items-center gap-1.5 rounded border border-brand-main-600/40 bg-brand-main-800/40 px-2.5 py-1 text-[11px] text-white/55 light:text-black/55">
                        <Terminal className="w-3 h-3 text-brand-secondary-400/70" />
                        {activitySummary.totalTools} tools
                      </span>
                    )}
                    {activitySummary.sandboxOps > 0 && (
                      <button
                        type="button"
                        onClick={() => {
                          setActivitySheetOpen(false)
                          setSandboxSheetOpen(true)
                        }}
                        className="inline-flex items-center gap-1.5 rounded border border-brand-secondary-500/20 bg-brand-secondary-500/5 px-2.5 py-1 text-[11px] text-brand-secondary-300/75"
                      >
                        <Box className="w-3 h-3" />
                        {activitySummary.sandboxOps} sandbox ops
                      </button>
                    )}
                    {activitySummary.spawnCount > 0 && (
                      <span className="inline-flex items-center gap-1.5 rounded border border-violet-500/20 bg-violet-500/5 px-2.5 py-1 text-[11px] text-violet-300/70">
                        <GitBranch className="w-3 h-3" />
                        {activitySummary.spawnCount} sub-agents
                      </span>
                    )}
                  </div>

                  <div className="w-fit rounded border border-brand-main-600 bg-brand-main-800/50 p-1">
                    {[
                      {
                        key: 'tools' as const,
                        label: 'Tools',
                        count: activitySummary.tools.length,
                      },
                      {
                        key: 'sandbox' as const,
                        label: 'Sandbox',
                        count: activitySummary.sandbox.length,
                      },
                      {
                        key: 'spawns' as const,
                        label: 'Sub-agents',
                        count: activitySummary.spawns.length,
                      },
                    ].map((tab) => (
                      <button
                        key={tab.key}
                        type="button"
                        onClick={() => setActivityPanelTab(tab.key)}
                        className={`relative inline-flex items-center gap-2 rounded px-3 py-1 text-[11px] transition-colors ${
                          activityPanelTab === tab.key
                            ? 'border border-brand-secondary-500/30 bg-brand-secondary-600/20 text-brand-secondary-300'
                            : 'text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50'
                        }`}
                      >
                        <span>{tab.label}</span>
                        <span className="rounded bg-black/20 px-1.5 py-0.5 text-[10px] text-white/35 light:text-black/35">
                          {tab.count}
                        </span>
                      </button>
                    ))}
                  </div>

                  {activityPanelTab === 'spawns' &&
                    activitySummary.spawns.length > 0 && (
                      <div>
                        <div className="px-1 pb-1 text-[10px] font-medium uppercase tracking-wider text-violet-400/40">
                          Sub-agents
                        </div>
                        <div className="space-y-1">
                          {activitySummary.spawns.map((spawn) => (
                            <div
                              key={spawn.id}
                              className="flex items-center gap-2.5 rounded-md border border-brand-main-800/30 bg-brand-main-900/35 px-3 py-2"
                            >
                              <span
                                className={`inline-block w-1.5 h-1.5 rounded-full shrink-0 ${
                                  spawn.status === 'running'
                                    ? 'bg-blue-400 animate-pulse'
                                    : spawn.status === 'failed'
                                      ? 'bg-red-400'
                                      : 'bg-emerald-400/60'
                                }`}
                              />
                              <span className="flex-1 truncate text-[11px] text-white/55 light:text-black/55">
                                {spawn.task ?? 'Sub-agent'}
                              </span>
                              {spawn.durationMs != null &&
                                spawn.durationMs > 0 && (
                                  <span className="text-[10px] tabular-nums text-white/25 light:text-black/25">
                                    {spawn.durationMs > 1000
                                      ? `${(spawn.durationMs / 1000).toFixed(1)}s`
                                      : `${spawn.durationMs}ms`}
                                  </span>
                                )}
                            </div>
                          ))}
                        </div>
                      </div>
                    )}

                  {activityPanelTab === 'tools' &&
                    activitySummary.tools.length > 0 && (
                      <div>
                        <div className="px-1 pb-1 text-[10px] font-medium uppercase tracking-wider text-brand-secondary-400/40">
                          Tools
                        </div>
                        <div className="space-y-1">
                          {(() => {
                            const toolGroups = new Map<
                              string,
                              {
                                items: typeof activitySummary.tools
                                running: number
                                failed: number
                              }
                            >()
                            for (const t of activitySummary.tools) {
                              const entry = toolGroups.get(t.name) ?? {
                                items: [],
                                running: 0,
                                failed: 0,
                              }
                              entry.items.push(t)
                              if (t.status === 'running') entry.running++
                              if (t.status === 'failed') entry.failed++
                              toolGroups.set(t.name, entry)
                            }
                            return Array.from(toolGroups.entries())
                              .sort(
                                (a, b) => b[1].items.length - a[1].items.length,
                              )
                              .map(([name, group]) => {
                                const rowKey = `tool-${name}`
                                const isExpanded =
                                  expandedActivityRows.has(rowKey)
                                return (
                                  <div key={rowKey}>
                                    <button
                                      type="button"
                                      onClick={() =>
                                        setExpandedActivityRows((prev) => {
                                          const next = new Set(prev)
                                          if (next.has(rowKey))
                                            next.delete(rowKey)
                                          else next.add(rowKey)
                                          return next
                                        })
                                      }
                                      className="flex w-full items-center gap-2.5 rounded-md border border-brand-main-800/30 bg-brand-main-900/35 px-3 py-2 text-left"
                                    >
                                      <span
                                        className={`inline-block w-1.5 h-1.5 rounded-full shrink-0 ${
                                          group.running > 0
                                            ? 'bg-blue-400 animate-pulse'
                                            : group.failed > 0
                                              ? 'bg-red-400'
                                              : 'bg-emerald-400/60'
                                        }`}
                                      />
                                      <span className="flex-1 truncate font-mono text-[11px] text-white/55 light:text-black/55">
                                        {name}
                                      </span>
                                      <span className="rounded bg-white/[0.03] px-1.5 py-0.5 text-[10px] tabular-nums text-white/25 light:text-black/25">
                                        {group.items.length}
                                      </span>
                                      <ChevronDown
                                        className={`w-2.5 h-2.5 text-white/15 transition-transform light:text-black/15${isExpanded ? 'rotate-180' : ''} light:text-black/15`}
                                      />
                                    </button>
                                    {isExpanded && (
                                      <div className="ml-5 space-y-1 border-l border-white/[0.04] pl-3 py-1">
                                        {group.items.map((t, i) => (
                                          <div
                                            key={t.id}
                                            className="flex items-center gap-2 text-[10px] text-white/28 light:text-black/28"
                                          >
                                            <span
                                              className={`inline-block w-1 h-1 rounded-full shrink-0 ${
                                                t.status === 'running'
                                                  ? 'bg-blue-400 animate-pulse'
                                                  : t.status === 'failed'
                                                    ? 'bg-red-400'
                                                    : 'bg-emerald-400/50'
                                              }`}
                                            />
                                            <span>#{i + 1}</span>
                                            {t.turnNumber != null && (
                                              <span className="text-white/15 light:text-black/15">
                                                T{t.turnNumber}
                                              </span>
                                            )}
                                            {t.durationMs != null &&
                                              t.durationMs > 0 && (
                                                <span className="ml-auto tabular-nums text-white/15 light:text-black/15">
                                                  {t.durationMs > 1000
                                                    ? `${(t.durationMs / 1000).toFixed(1)}s`
                                                    : `${t.durationMs}ms`}
                                                </span>
                                              )}
                                          </div>
                                        ))}
                                      </div>
                                    )}
                                  </div>
                                )
                              })
                          })()}
                        </div>
                      </div>
                    )}

                  {activityPanelTab === 'sandbox' &&
                    activitySummary.sandbox.length > 0 && (
                      <div>
                        <div className="px-1 pb-1 text-[10px] font-medium uppercase tracking-wider text-brand-secondary-400/40">
                          Sandbox
                        </div>
                        <button
                          type="button"
                          onClick={() => {
                            setActivitySheetOpen(false)
                            setSandboxSheetOpen(true)
                          }}
                          className="flex w-full items-center gap-2.5 rounded-md border border-brand-secondary-500/15 bg-brand-secondary-500/5 px-3 py-2 text-left text-[11px] text-brand-secondary-300/75"
                        >
                          <Box className="w-3 h-3" />
                          <span className="flex-1">
                            Open sandbox operations
                          </span>
                          <span className="rounded bg-brand-secondary-500/10 px-1.5 py-0.5 text-[10px] font-medium">
                            {activitySummary.sandbox.length}
                          </span>
                        </button>
                      </div>
                    )}

                  {activityPanelTab === 'tools' &&
                    activitySummary.tools.length === 0 && (
                      <div className="rounded-md border border-brand-main-800/30 bg-brand-main-900/30 px-3 py-6 text-center text-sm text-white/30 light:text-black/30">
                        No tool calls yet
                      </div>
                    )}
                  {activityPanelTab === 'sandbox' &&
                    activitySummary.sandbox.length === 0 && (
                      <div className="rounded-md border border-brand-main-800/30 bg-brand-main-900/30 px-3 py-6 text-center text-sm text-white/30 light:text-black/30">
                        No sandbox operations yet
                      </div>
                    )}
                  {activityPanelTab === 'spawns' &&
                    activitySummary.spawns.length === 0 && (
                      <div className="rounded-md border border-brand-main-800/30 bg-brand-main-900/30 px-3 py-6 text-center text-sm text-white/30 light:text-black/30">
                        No sub-agent activity yet
                      </div>
                    )}
                </div>
              </SheetBody>
            </SheetContent>
          </Sheet>

          {/* Sandbox operations side sheet */}
          <Sheet open={sandboxSheetOpen} onOpenChange={setSandboxSheetOpen}>
            <SheetContent
              side="right"
              className="sm:max-w-lg"
              overlayClassName="bg-transparent"
            >
              <SheetHeader>
                <SheetTitle className="flex items-center gap-2 text-base">
                  <Terminal className="w-4 h-4 text-brand-secondary-400" />
                  Sandbox Operations
                  {sandboxToolCalls.length > 0 && (
                    <span className="px-1.5 py-0.5 rounded-full bg-brand-secondary-500/15 text-brand-secondary-300 text-xs font-medium">
                      {sandboxToolCalls.length}
                    </span>
                  )}
                </SheetTitle>
              </SheetHeader>
              <SheetBody ref={sandboxSheetRef} className="py-4">
                {sandboxToolCalls.length === 0 ? (
                  <div className="flex flex-col items-center justify-center py-16 text-white/20 light:text-black/20">
                    <Terminal className="w-8 h-8 mb-3 text-brand-secondary-500/20" />
                    <p className="text-sm">No sandbox operations yet</p>
                  </div>
                ) : (
                  <div className="space-y-3">
                    {sandboxToolCalls.map((tc, i) => {
                      const prevTurn =
                        i > 0 ? sandboxToolCalls[i - 1].turnNumber : undefined
                      const showTurnLabel =
                        tc.turnNumber != null && tc.turnNumber !== prevTurn
                      return (
                        <div key={tc.key}>
                          {showTurnLabel && (
                            <div className="text-[10px] text-white/20 mb-1.5 pl-1 light:text-black/20">
                              Turn {tc.turnNumber}
                            </div>
                          )}
                          <ToolCallCard
                            toolCallId={tc.toolCallId}
                            toolName={tc.toolName}
                            agentId={session.agentId}
                            toolArgs={tc.toolArgs}
                            toolResult={tc.toolResult}
                            toolSuccess={tc.toolSuccess}
                            toolDurationMs={tc.toolDurationMs}
                            status={tc.status}
                            sandboxId={tc.sandboxId}
                            sandboxExitCode={tc.sandboxExitCode}
                            sandboxDurationMs={tc.sandboxDurationMs}
                            sandboxParentDurationMs={tc.sandboxParentDurationMs}
                            sessionId={tc.sessionId}
                          />
                        </div>
                      )
                    })}
                    {sandboxToolCalls.some((tc) => tc.status === 'running') && (
                      <div className="flex items-center gap-2 px-3 py-2 rounded-md bg-brand-secondary-500/5 border border-brand-secondary-500/10">
                        <Loader2 className="w-3.5 h-3.5 text-brand-secondary-400 animate-spin shrink-0" />
                        <span className="text-xs text-brand-secondary-300/70">
                          Executing in sandbox...
                        </span>
                      </div>
                    )}
                  </div>
                )}
              </SheetBody>
            </SheetContent>
          </Sheet>
        </div>
        {/* Browser side panel — slides in from right when stream is active */}
        {showBrowserPanel && browserStreamSessionId && (
          <div
            className="shrink-0 border-l border-brand-secondary-500/20 flex flex-col h-full bg-brand-main-950 animate-in slide-in-from-right duration-200 relative"
            style={{ width: browserPanelWidth }}
          >
            {/* Resize handle on the left edge */}
            <div
              className="absolute left-0 top-0 bottom-0 w-1 cursor-col-resize z-20 hover:bg-brand-secondary-400/30 active:bg-brand-secondary-400/50 transition-colors"
              onMouseDown={(e) => {
                e.preventDefault()
                const startX = e.clientX
                const startWidth = browserPanelWidth
                const onMouseMove = (ev: MouseEvent) => {
                  const delta = startX - ev.clientX
                  setBrowserPanelWidth(
                    Math.max(320, Math.min(startWidth + delta, 900)),
                  )
                }
                const onMouseUp = () => {
                  document.removeEventListener('mousemove', onMouseMove)
                  document.removeEventListener('mouseup', onMouseUp)
                }
                document.addEventListener('mousemove', onMouseMove)
                document.addEventListener('mouseup', onMouseUp)
              }}
            />
            <div className="flex items-center justify-between px-3 py-1.5 border-b border-brand-secondary-500/20 bg-brand-main-900/80 shrink-0">
              <div className="flex items-center gap-2">
                <Iconify.Icon
                  icon="simple-icons:googlechrome"
                  className="w-3.5 h-3.5 text-brand-secondary-400"
                />
                <span className="text-[11px] font-medium text-brand-secondary-300">
                  Browser
                </span>
              </div>
              <button
                type="button"
                onClick={() => setBrowserViewerDismissed(true)}
                className="text-white/30 hover:text-white/60 transition-colors p-0.5 light:text-black/30 light:hover:text-black/60"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </div>
            <div className="flex-1 min-h-0">
              <BrowserStreamViewer
                sessionId={browserStreamSessionId}
                screenshotBase64={browserScreenshotBase64}
              />
            </div>
          </div>
        )}
        {/* Workflow studio preview panel */}
        {showWorkflowPanel && workflowPanelData && (
          <div
            className="shrink-0 border-l border-violet-500/20 flex flex-col h-full bg-brand-main-950 animate-in slide-in-from-right duration-200 relative"
            style={{ width: workflowPanelWidth }}
          >
            {/* Resize handle */}
            <div
              className="absolute left-0 top-0 bottom-0 w-1 cursor-col-resize z-20 hover:bg-violet-400/30 active:bg-violet-400/50 transition-colors"
              onMouseDown={(e) => {
                e.preventDefault()
                const startX = e.clientX
                const startWidth = workflowPanelWidth
                const onMouseMove = (ev: MouseEvent) => {
                  const delta = startX - ev.clientX
                  setWorkflowPanelWidth(
                    Math.max(280, Math.min(startWidth + delta, 700)),
                  )
                }
                const onMouseUp = () => {
                  document.removeEventListener('mousemove', onMouseMove)
                  document.removeEventListener('mouseup', onMouseUp)
                }
                document.addEventListener('mousemove', onMouseMove)
                document.addEventListener('mouseup', onMouseUp)
              }}
            />
            <div className="flex items-center justify-between px-3 py-1.5 border-b border-violet-500/20 bg-brand-main-900/80 shrink-0">
              <div className="flex items-center gap-2">
                <Workflow className="w-3.5 h-3.5 text-violet-400" />
                <span className="text-[11px] font-medium text-violet-300">
                  Studio Preview
                </span>
              </div>
              <button
                type="button"
                onClick={() => useSidePanelStore.getState().hideWorkflowPanel()}
                className="text-white/30 hover:text-white/60 transition-colors p-0.5 light:text-black/30 light:hover:text-black/60"
              >
                <X className="w-3.5 h-3.5" />
              </button>
            </div>
            <div className="flex-1 min-h-0">
              <WorkflowPreviewPanel data={workflowPanelData} />
            </div>
          </div>
        )}
      </div>
    </TooltipProvider>
  )
}
