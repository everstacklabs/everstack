import { useState, useCallback, useMemo, useRef, useEffect } from 'react'
import { createFileRoute, useNavigate } from '@tanstack/react-router'
import { ArrowRight, Loader2, ChevronDown, Bot, AlertTriangle } from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { create } from '@everstack/client'
import { getOrCreatePlatformAgent } from '@/server/platform-agent'
import { createSession } from '@/server/agents'
import { useSession_, useAgents } from '@/hooks/deployments/use-agents'
import { useSession } from '@/hooks/auth/use-auth'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import {
  SessionStatus,
  AgentSessionSchema,
  type AgentDefinition,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { SessionTimeline } from '@/components/deployments/agents/session-timeline'
import {
  MentionComposerInput,
  type MentionComposerInputHandle,
} from '@/components/deployments/agents/mention-composer-input'
import { Button } from '@everstack/ui/components'
import { ContextSelector } from '@/components/home/context-selector'
import { ModelPicker } from '@/components/providers/model-picker'
import { cn } from '@/lib/utils'
import { EverstackLogo } from '@/components/brand/everstack-logo'

export const Route = createFileRoute('/chat')({
  component: ChatPage,
  validateSearch: (search: Record<string, unknown>) => ({
    session: (search.session as string) || undefined,
    model: (search.model as string) || undefined,
  }),
})

const LAST_SESSION_KEY = 'platform-chat:last-session'

function ChatPage() {
  const { session: activeSessionId, model: urlModel } = Route.useSearch()
  const navigate = useNavigate()
  const { data: authSession, isLoading: isAuthLoading } = useSession()
  const orgId = authSession?.user?.organizations?.[0]?.id ?? ''

  // Resolve platform agent. Gated on orgId so the request never fires
  // before `setActiveOrgId` primes the `x-org-id` interceptor — the
  // gateway requires that header for cookie-authed users post-2026-05-06.
  // Key includes orgId so a multi-org switch refetches per tenant.
  // Failure is non-fatal: NewChatView falls back to a user-agent picker.
  const {
    data: platformAgent,
    isLoading: isAgentLoading,
    error: agentError,
  } = useQuery({
    queryKey: ['platform-agent', orgId],
    queryFn: getOrCreatePlatformAgent,
    enabled: !!orgId,
    staleTime: Infinity,
    retry: 2,
  })
  const platformAgentId = platformAgent?.id ?? ''

  // Persist last active session
  useEffect(() => {
    if (activeSessionId) {
      localStorage.setItem(LAST_SESSION_KEY, activeSessionId)
    }
  }, [activeSessionId])

  // Restore last session if no session in URL
  const restoredRef = useRef(false)
  useEffect(() => {
    if (activeSessionId || restoredRef.current) return
    restoredRef.current = true
    const lastSession = localStorage.getItem(LAST_SESSION_KEY)
    if (lastSession) {
      navigate({ to: '/chat', search: { session: lastSession, model: urlModel ?? "" }, replace: true })
    }
  }, [activeSessionId, navigate])

  // Wait only on auth — platform-agent init never blocks the UI.
  if (isAuthLoading || (!orgId && !authSession)) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="size-6 text-brand-secondary-400 animate-spin" />
      </div>
    )
  }

  // Session in URL — load independently of platform agent state.
  if (activeSessionId) {
    return <SessionView key={activeSessionId} sessionId={activeSessionId} agentId={platformAgentId} initialModel={urlModel} />
  }

  // Empty state — composer with agent picker. Picker defaults to platform
  // agent when available; otherwise lets the user pick any of their agents.
  return (
    <NewChatView
      platformAgentId={platformAgentId}
      isPlatformAgentLoading={isAgentLoading}
      platformAgentError={agentError instanceof Error ? agentError : null}
      orgId={orgId}
      onSessionCreated={(id, selectedModel) => navigate({ to: '/chat', search: { session: id, model: selectedModel || undefined }, replace: true })}
    />
  )
}

/**
 * Renders an existing session. Keyed by sessionId so React fully
 * remounts when switching sessions — no stale state.
 *
 * Handles CQRS lag: if the DB hasn't persisted the session yet but
 * the store has live events (just-created session), shows a stub
 * so SessionTimeline can render streaming content.
 */
function SessionView({ sessionId, agentId, initialModel }: { sessionId: string; agentId: string; initialModel?: string }) {
  const { data: session, isLoading, refetch } = useSession_(sessionId)

  // Check store for live events (handles just-created sessions before DB catches up)
  const storeEntry = useAgentSessionStore((s) => s.sessions[sessionId])
  const hasStoreEvents = (storeEntry?.events?.length ?? 0) > 0
  const isStreaming = storeEntry?.isStreaming ?? false

  // Refetch session from DB when streaming ends so new turns appear in history
  const wasStreaming = useRef(false)
  useEffect(() => {
    if (isStreaming) {
      wasStreaming.current = true
    } else if (wasStreaming.current) {
      wasStreaming.current = false
      // Delay refetch slightly to let CQRS projection persist the turn
      const t = setTimeout(() => refetch(), 1000)
      return () => clearTimeout(t)
    }
  }, [isStreaming, refetch])

  // Build effective session: real DB session > stub during CQRS lag
  const effectiveSession = useMemo(() => {
    if (session) return session
    // Store has events (just created, DB hasn't caught up) — show stub
    if (hasStoreEvents || isStreaming) {
      return create(AgentSessionSchema, {
        id: sessionId,
        status: SessionStatus.RUNNING,
        agentId,
        tenantId: '',
        turns: [],
        turnCount: 0,
        totalTokens: 0,
      })
    }
    return null
  }, [session, sessionId, agentId, hasStoreEvents, isStreaming])

  if (isLoading && !effectiveSession) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="size-5 text-brand-secondary-400 animate-spin" />
      </div>
    )
  }

  if (!effectiveSession) {
    return (
      <div className="flex h-full items-center justify-center">
        <p className="text-sm text-white/40 light:text-black/40">Session not found.</p>
      </div>
    )
  }

  return (
    <div className="flex h-full w-full flex-col">
      <div className="flex-1 overflow-hidden">
        <SessionTimeline session={effectiveSession} hideStatusBar initialModel={initialModel} />
      </div>
    </div>
  )
}

/**
 * Empty state with composer. Creates a session on first message
 * and navigates to /chat?session={id}.
 */
const QUICK_TOPICS = [
  { label: 'Agents', icon: 'lucide:bot' },
  { label: 'Observability', icon: 'hugeicons:telescope-02' },
  { label: 'Gateway', icon: 'mingcute:route-line' },
  { label: 'Evaluations', icon: 'lucide:flask-conical' },
  { label: 'Vault', icon: 'stash:vault' },
  { label: 'Sandbox', icon: 'lucide:box' },
]

function NewChatView({
  platformAgentId,
  isPlatformAgentLoading,
  platformAgentError,
  orgId,
  onSessionCreated,
}: {
  platformAgentId: string
  isPlatformAgentLoading: boolean
  platformAgentError: Error | null
  orgId: string
  onSessionCreated: (sessionId: string, model: string) => void
}) {
  const [userInput, setUserInput] = useState('')
  const [context, setContext] = useState('auto')
  const [model, setModel] = useState('')
  const [sending, setSending] = useState(false)
  const [selectedAgentId, setSelectedAgentId] = useState<string>('')
  const composerRef = useRef<MentionComposerInputHandle>(null)
  const queryClient = useQueryClient()

  // User agents fallback list. Hidden agents excluded so __platform__
  // doesn't double up when the platform agent loads via its own query.
  const { data: userAgents = [], isLoading: isUserAgentsLoading } = useAgents()

  // Resolve the effective agent id. Honors explicit selection; otherwise
  // prefers platform agent; otherwise falls back to the first user agent.
  const effectiveAgentId =
    selectedAgentId || platformAgentId || userAgents[0]?.id || ''

  const handleSend = useCallback(async () => {
    const text = userInput.trim()
    if (!text || sending || !effectiveAgentId) return
    setUserInput('')
    setSending(true)

    try {
      const resp = await createSession({ tenantId: orgId, agentId: effectiveAgentId })
      const newSessionId = resp.session?.id
      if (!newSessionId) return

      // Start the turn FIRST so SSE events flow into the store,
      // then navigate so SessionView picks them up on mount.
      // startTurn is fire-and-forget (the await is for the stream setup,
      // not completion), so we don't need to wait for it.
      useAgentSessionStore.getState().startTurn(
        newSessionId, orgId, text, queryClient,
        model ? { modelOverride: model } : undefined,
      )

      // Small delay to let the store register the session entry
      // before SessionView mounts and reads it
      await new Promise((r) => setTimeout(r, 150))

      onSessionCreated(newSessionId, model)
    } finally {
      setSending(false)
    }
  }, [userInput, sending, effectiveAgentId, orgId, model, onSessionCreated, queryClient])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        handleSend()
      }
    },
    [handleSend],
  )

  const handleInputChange = useCallback((value: string, _cursor: number) => {
    setUserInput(value)
  }, [])

  return (
    <div className="flex h-full flex-col items-center justify-center overflow-y-auto scrollbar-macos">
      <div className="w-full max-w-2xl px-6 space-y-6">
        {/* Logo + heading */}
        <div className="text-center space-y-2">
          <EverstackLogo className="mx-auto mb-4" />
          <h1 className="text-2xl font-semibold text-white light:text-brand-main-50">How can I help you?</h1>
          <p className="text-sm text-white/40 light:text-black/40">Manage agents, query traces, and control your platform.</p>
        </div>

        {/* Prompt bar */}
        <div className="w-full rounded-lg border border-brand-main-600 bg-brand-main-800/60 transition-colors focus-within:border-brand-main-500 focus-within:ring-1 focus-within:ring-brand-secondary-500/30">
          <MentionComposerInput
            ref={composerRef}
            value={userInput}
            onValueCursorChange={handleInputChange}
            onKeyDown={handleKeyDown}
            placeholder="Ask a question or / for commands"
            className="min-h-16"
          />
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-1.5">
              <ContextSelector value={context} onChange={setContext} />
              <AgentPicker
                value={effectiveAgentId}
                onChange={setSelectedAgentId}
                platformAgentId={platformAgentId}
                userAgents={userAgents}
                isLoading={isPlatformAgentLoading || isUserAgentsLoading}
              />
            </div>
            <div className="flex items-center gap-1.5">
              <ModelPicker value={model} onChange={setModel} variant="compact" />
              <Button
                variant={userInput.trim() ? 'default' : 'ghost'}
                onClick={handleSend}
                disabled={!userInput.trim() || sending || !effectiveAgentId}
                className="h-6 w-6"
              >
                {sending ? (
                  <Loader2 className="size-4 animate-spin" />
                ) : (
                  <ArrowRight className="size-4" />
                )}
              </Button>
            </div>
          </div>
        </div>

        {platformAgentError && !platformAgentId ? (
          <div className="flex items-start gap-2 rounded border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-xs text-amber-200/80 light:text-amber-700/80">
            <AlertTriangle className="size-3.5 shrink-0 mt-0.5" />
            <div className="min-w-0 space-y-0.5">
              <p className="font-medium text-amber-200 light:text-amber-700">Platform agent unavailable.</p>
              <p className="text-amber-200/60 light:text-amber-700/60 break-words">{platformAgentError.message}</p>
              <p className="text-amber-200/50 light:text-amber-700/50">Pick one of your agents above to keep going.</p>
            </div>
          </div>
        ) : null}

        {!effectiveAgentId && !isPlatformAgentLoading && !isUserAgentsLoading ? (
          <div className="rounded border border-brand-main-600 bg-brand-main-800/40 px-3 py-2 text-xs text-white/50 light:text-black/50 text-center">
            No agents available yet. Create one in Agents to start chatting.
          </div>
        ) : null}

        {/* Quick topic pills */}
        <div className="flex flex-wrap items-center justify-center gap-2">
          {QUICK_TOPICS.map((topic) => (
            <button
              key={topic.label}
              onClick={() => {
                setContext(topic.label.toLowerCase())
                composerRef.current?.focus()
              }}
              className="flex items-center gap-1.5 rounded-full border border-brand-main-600 bg-brand-main-800/40 px-3 py-1.5 text-xs text-white/50 light:text-black/50 transition-colors hover:border-brand-main-500 hover:text-white/70 light:hover:text-black/70 hover:bg-brand-main-800/60"
            >
              <Iconify.Icon icon={topic.icon} className="size-3.5" />
              {topic.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  )
}

function AgentPicker({
  value,
  onChange,
  platformAgentId,
  userAgents,
  isLoading,
}: {
  value: string
  onChange: (id: string) => void
  platformAgentId: string
  userAgents: AgentDefinition[]
  isLoading: boolean
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

  // Build picker options. Platform agent gets a friendly label so users
  // don't see the internal `__platform__` name.
  const options: { id: string; label: string; sublabel?: string }[] = []
  if (platformAgentId) {
    options.push({ id: platformAgentId, label: 'Platform assistant', sublabel: 'default' })
  }
  for (const a of userAgents) {
    if (a.id === platformAgentId) continue
    options.push({ id: a.id, label: a.name || 'Untitled agent', sublabel: a.model })
  }

  const selected = options.find((o) => o.id === value)
  const label = selected?.label ?? (isLoading ? 'Loading…' : 'No agents')

  return (
    <div ref={ref} className="relative">
      <button
        onClick={() => setOpen(!open)}
        disabled={!options.length}
        className="flex items-center gap-1.5 h-7 rounded px-2 text-[11px] font-medium border border-brand-main-700/70 bg-brand-main-900/55 text-white/70 light:text-black/70 transition-colors hover:text-white/90 light:hover:text-black/90 hover:border-brand-main-600 disabled:opacity-50 disabled:cursor-not-allowed"
      >
        <Bot className="size-3.5" />
        <span className="max-w-[140px] truncate">{label}</span>
        <ChevronDown className={cn('size-3 transition-transform', open && 'rotate-180')} />
      </button>

      {open && options.length > 0 && (
        <div className="absolute bottom-full left-0 mb-1 w-56 rounded border border-brand-main-600 bg-brand-main-800 py-1 shadow-xl z-50 p-1 max-h-72 overflow-y-auto scrollbar-macos">
          {options.map((option) => (
            <button
              key={option.id}
              onClick={() => {
                onChange(option.id)
                setOpen(false)
              }}
              className={cn(
                'flex w-full items-center justify-between gap-2 rounded px-3 py-2 text-left transition-colors',
                option.id === value
                  ? 'bg-brand-secondary-600/15 text-brand-secondary-300'
                  : 'text-white/60 light:text-black/60 hover:bg-brand-main-700/50 hover:text-white/80 light:hover:text-black/80',
              )}
            >
              <span className="text-xs font-medium truncate">{option.label}</span>
              {option.sublabel ? (
                <span className="text-[10px] text-white/40 light:text-black/40 truncate shrink-0 max-w-[80px]">{option.sublabel}</span>
              ) : null}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
