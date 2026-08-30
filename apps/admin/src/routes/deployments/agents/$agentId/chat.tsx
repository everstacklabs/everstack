import { useState, useCallback, useMemo, useEffect, useRef } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { Sparkles, ArrowUp, Paperclip, ImageIcon } from 'lucide-react'
import { create } from '@everstack/client'
import { useAgent, useSession_ } from '@/hooks/deployments/use-agents'
import { AgentLifecycleMode, AgentLifecycleStatus } from '@/server/agents'
import { useAgentChatSession } from '@/hooks/deployments/use-agent-chat-session'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import {
  SessionStatus,
  AgentSessionSchema,
} from '@everstack/proto/everstack/agents/v1/agents_pb'
import { SessionTimeline } from '@/components/deployments/agents/session-timeline'
import {
  MentionComposerInput,
  type MentionComposerInputHandle,
} from '@/components/deployments/agents/mention-composer-input'
import { Button } from '@everstack/ui/components'

export const Route = createFileRoute('/deployments/agents/$agentId/chat')({
  component: ChatRoute,
})

function ChatRoute() {
  const { agentId } = Route.useParams()
  const { data: agent } = useAgent(agentId)

  if (!agent) return null

  // Don't render anything while provisioning — the parent layout shows
  // the provisioning overlay. Without this guard the session timeline
  // flashes briefly during tab transitions (isOnChatTab is momentarily
  // false in the parent, so <Outlet /> renders before the overlay).
  const isPersistent = agent.lifecycleMode === AgentLifecycleMode.PERSISTENT
  const isProvisioning =
    isPersistent &&
    (agent.lifecycleStatus === AgentLifecycleStatus.PROVISIONING ||
      agent.lifecycleStatus === AgentLifecycleStatus.CREATED)
  if (isProvisioning) return null

  return (
    <ChatView
      agentId={agentId}
      agentName={agent.name}
      primarySessionId={agent.primarySessionId}
    />
  )
}

function ChatView({
  agentId,
  agentName,
  primarySessionId,
}: {
  agentId: string
  agentName: string
  primarySessionId?: string
}) {
  const {
    sessionId,
    startSession,
    sendMessage,
    isStreaming,
    resetSessionPointer,
  } = useAgentChatSession(agentId, primarySessionId)

  const [userInput, setUserInput] = useState('')
  const composerRef = useRef<MentionComposerInputHandle>(null)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Load the real session from DB (if sessionId is set).
  const {
    data: session,
    isLoading: isSessionLoading,
    isError: isSessionError,
    error: sessionError,
  } = useSession_(sessionId ?? '')

  // Check if the store has events for this session (handles CQRS lag).
  const storeEntry = useAgentSessionStore((s) =>
    sessionId ? s.sessions[sessionId] : undefined,
  )
  const hasStoreEvents = (storeEntry?.events?.length ?? 0) > 0

  // Reset pointer ONLY for a stale pointer to a session that never loaded —
  // e.g. a localStorage id for a session that was deleted server-side. We must
  // never abandon an *active* session: right after a turn completes the store
  // invalidates the session query, and a transient getSession failure (CQRS
  // lag / refetch blip) would otherwise fork a brand-new session and wipe the
  // history + context. If the session has ever loaded successfully (`session`
  // is retained by React Query across refetch errors), keep it.
  useEffect(() => {
    if (!sessionId || !isSessionError || isStreaming) return
    if (hasStoreEvents || session) return
    const errorText = String(sessionError?.message ?? '').toLowerCase()
    const isNotFound =
      errorText.includes('not found') ||
      errorText.includes('404') ||
      errorText.includes('unknown session')
    if (!isNotFound) return
    resetSessionPointer()
  }, [
    sessionId,
    isSessionError,
    isStreaming,
    hasStoreEvents,
    session,
    resetSessionPointer,
    sessionError?.message,
  ])

  // Stub session pattern: keep SessionTimeline mounted while DB catches up.
  const effectiveSession = useMemo(() => {
    if (session) return session
    if (!sessionId) return null
    if (hasStoreEvents || isStreaming || isSessionLoading) {
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
  }, [session, sessionId, isSessionLoading, isStreaming, hasStoreEvents])

  const handleSend = useCallback(async () => {
    const text = userInput.trim()
    if (!text) return
    setUserInput('')

    // Continue the existing session whenever we have a pointer to one. Only
    // start a new session when there is genuinely none (fresh chat, or the
    // user explicitly chose "New Session" — both leave sessionId null). Keying
    // on the racy effectiveSession could fork a new session mid-conversation.
    if (sessionId) {
      await sendMessage(text)
    } else {
      await startSession(text)
    }
  }, [userInput, sessionId, sendMessage, startSession])

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

  // Render session timeline when we have an effective session
  if (effectiveSession) {
    return (
      <div className="flex h-full w-full flex-col">
        <div className="flex-1 overflow-hidden">
          <SessionTimeline session={effectiveSession} hideStatusBar />
        </div>
      </div>
    )
  }

  // Empty state — no session yet, full composer
  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 flex-col items-center justify-center text-brand-main-300">
        <div className="relative mb-6">
          <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Sparkles className="size-8 text-brand-secondary-400" />
          </div>
        </div>
        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
          Message {agentName}
        </h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
          Start a conversation below to begin a session.
        </p>
      </div>
      <div className="shrink-0 border-t border-brand-main-800/30">
        <div className="mx-auto max-w-3xl px-4 py-3">
          <div className="relative rounded border border-brand-main-600 bg-brand-main-950 transition-colors focus-within:ring-1 focus-within:ring-brand-secondary-500">
            <MentionComposerInput
              ref={composerRef}
              value={userInput}
              onValueCursorChange={handleInputChange}
              onKeyDown={handleKeyDown}
              placeholder={`Message ${agentName}... (type @ for files or subagents)`}
            />
            <div className="flex items-center justify-between px-2 pb-2">
              <div className="flex items-center gap-0.5">
                <input
                  ref={fileInputRef}
                  type="file"
                  multiple
                  className="hidden"
                />
                <Button
                  size="xs"
                  variant="transparent"
                  title="Attach files"
                  onClick={() => fileInputRef.current?.click()}
                  className="rounded-sm text-white/35 light:text-black/35 transition-colors hover:bg-brand-main-800/50 hover:text-white/60 light:hover:text-black/60"
                >
                  <Paperclip className="h-4 w-4" />
                </Button>
                <Button
                  size="xs"
                  variant="transparent"
                  title="Attach images"
                  onClick={() => {
                    const input = document.createElement('input')
                    input.type = 'file'
                    input.accept = 'image/*'
                    input.multiple = true
                    input.click()
                  }}
                  className="rounded-sm text-white/35 light:text-black/35 transition-colors hover:bg-brand-main-800/50 hover:text-white/60 light:hover:text-black/60"
                >
                  <ImageIcon className="h-4 w-4" />
                </Button>
              </div>
              <div className="flex items-center gap-2">
                {userInput.trim() && (
                  <span className="hidden select-none items-center gap-1 text-[11px] font-light text-brand-main-300 sm:flex">
                    <kbd className="rounded bg-white/10 light:bg-black/10 px-1.5 py-0.5 text-[10px] font-mono opacity-50">
                      &crarr;
                    </kbd>
                    to send &middot;
                    <kbd className="rounded bg-white/10 light:bg-black/10 px-1.5 py-0.5 text-[10px] font-mono opacity-50">
                      &uArr;&crarr;
                    </kbd>
                    for newline
                  </span>
                )}
                {userInput.trim() && (
                  <Button
                    size="xs"
                    variant="default"
                    type="button"
                    onClick={handleSend}
                    disabled={isStreaming}
                  >
                    <ArrowUp className="h-4 w-4" />
                  </Button>
                )}
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
