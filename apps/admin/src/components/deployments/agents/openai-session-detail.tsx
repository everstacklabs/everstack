import { useEffect, useMemo, useRef, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ui } from '@everstack/ui'
import {
  coalesceOpenAIHistory,
  eventText,
  openAIEventRole,
  openAIRequest,
  OpenAIRequestError,
  type OpenAICapabilities,
  type OpenAIEvent,
  type OpenAISession,
} from '@/server/openai-agents'
import { OpenAISessionResources } from './openai-session-resources'
import { useOpenAIAgentsScope } from './openai-start-session-dialog'
import { AgentMarkdown } from './agent-markdown'
import {
  ChatAssistantMessage,
  ChatComposer,
  ChatTranscript,
  ChatUserMessage,
} from './chat-presentation'
import {
  openAIInputDisabled,
  openAITurnActive,
} from './openai-session-controls'

const {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  Button,
} = ui
const errorText = (error: unknown) =>
  error instanceof Error
    ? error.message
    : 'Request failed. Refresh to recover the current state.'

export function OpenAISessionDetail({
  sessionId,
  onDeleted,
}: {
  sessionId: string
  onDeleted?: () => void
}) {
  const { authenticated, scope } = useOpenAIAgentsScope()
  const cache = useQueryClient()
  const [followup, setFollowup] = useState('')
  const [deleteOpen, setDeleteOpen] = useState(false)
  const [intent, setIntent] = useState<{
    idempotency_key: string
    input: string
  } | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)
  const followingRef = useRef(true)
  const key = ['openai-agents', scope]
  const capabilities = useQuery({
    queryKey: [...key, 'capabilities'],
    queryFn: () => openAIRequest<OpenAICapabilities>('/capabilities'),
    enabled: authenticated,
    retry: false,
  })
  const detail = useQuery({
    queryKey: [...key, 'session', sessionId],
    queryFn: () =>
      openAIRequest<OpenAISession>(
        `/sessions/${encodeURIComponent(sessionId)}`,
      ),
    enabled: authenticated && !!sessionId,
    refetchInterval: 3000,
    retry: false,
  })
  const events = useQuery({
    queryKey: [...key, 'events', sessionId],
    queryFn: async () => {
      const all: OpenAIEvent[] = []
      let cursor = 0
      for (let page = 0; page < 100; page++) {
        const result = await openAIRequest<{
          data: OpenAIEvent[]
          next_cursor: number
          has_more: boolean
        }>(
          `/sessions/${encodeURIComponent(sessionId)}/events?after=${cursor}&limit=100`,
        )
        all.push(...result.data)
        if (!result.has_more) return all
        if (result.next_cursor <= cursor)
          throw new Error('History cursor did not advance. Refresh to recover.')
        cursor = result.next_cursor
      }
      throw new Error(
        'This session has more history than this view can load. Export it through the SDK.',
      )
    },
    enabled: authenticated && !!sessionId,
    refetchInterval: 3000,
    retry: false,
  })
  const control = useMutation({
    mutationFn: ({
      action,
      body,
    }: {
      action: 'input' | 'cancel' | 'delete'
      body?: unknown
    }) =>
      openAIRequest<OpenAISession>(
        `/sessions/${encodeURIComponent(sessionId)}${action === 'delete' ? '' : `/${action}`}`,
        action === 'delete' ? 'DELETE' : 'POST',
        body,
      ),
    retry: false,
    onError: (error) => {
      if (
        error instanceof OpenAIRequestError &&
        [400, 403, 404].includes(error.status)
      )
        setIntent(null)
    },
    onSuccess: (_, variables) => {
      if (variables.action === 'input') {
        setFollowup('')
        setIntent(null)
      }
      if (variables.action === 'delete') onDeleted?.()
      void cache.invalidateQueries({ queryKey: key })
    },
  })
  const current = detail.data
  const history = useMemo(
    () =>
      coalesceOpenAIHistory(events.data ?? []).filter(
        (event) => eventText(event) || event.tool_failed,
      ),
    [events.data],
  )
  useEffect(() => {
    const node = scrollRef.current
    if (node && followingRef.current) node.scrollTop = node.scrollHeight
  }, [history])
  if (detail.isPending && !current)
    return (
      <div className="flex h-full items-center justify-center text-sm text-white/55">
        Loading session…
      </div>
    )
  if (!current)
    return (
      <Failure
        error={detail.error ?? new Error('Session not found')}
        onRetry={() => detail.refetch()}
      />
    )
  const environmentTerminal =
    !!current.environment_cleanup_status &&
    current.environment_cleanup_status !== 'active'
  const turnActive = openAITurnActive(current.status)
  const controlsAvailable =
    !capabilities.isPending &&
    !capabilities.error &&
    capabilities.data?.can_run_sessions === true
  const effectiveCapabilities = capabilities.data
    ? {
        ...capabilities.data,
        can_run_sessions: controlsAvailable,
        can_resolve_approvals:
          !capabilities.isPending &&
          !capabilities.error &&
          capabilities.data.can_resolve_approvals,
      }
    : undefined
  const inputDisabled = openAIInputDisabled({
    environmentTerminal,
    ambiguousIntent: !!intent,
    capabilitiesReady: controlsAvailable,
    pending: control.isPending,
  })
  const submit = () => {
    if (inputDisabled || !followup.trim()) return
    const body = { idempotency_key: crypto.randomUUID(), input: followup }
    setIntent(body)
    control.mutate({ action: 'input', body })
  }
  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="shrink-0 border-b border-brand-main-800/50 px-4 py-3">
        <div className="mx-auto flex max-w-3xl flex-wrap items-center justify-between gap-3">
          <div>
            <div className="flex items-center gap-2">
              <h2 className="text-sm font-medium">{current.model}</h2>
              <RuntimeBadge />
            </div>
            <p className="mt-0.5 text-xs text-white/45 light:text-black/45">
              {current.status} ·{' '}
              {current.environment.type === 'self_hosted'
                ? 'Everstack sandbox'
                : 'No sandbox'}{' '}
              · {current.readiness}
            </p>
          </div>
          <div className="flex gap-2">
            <Button
              size="sm"
              variant="outline"
              disabled={control.isPending || !controlsAvailable || !turnActive}
              onClick={() => control.mutate({ action: 'cancel' })}
            >
              Cancel turn
            </Button>
            <Button
              size="sm"
              variant="destructive"
              disabled={control.isPending || !controlsAvailable}
              onClick={() => setDeleteOpen(true)}
            >
              Delete
            </Button>
          </div>
        </div>
      </div>
      <div
        ref={scrollRef}
        onScroll={(event) => {
          const node = event.currentTarget
          followingRef.current =
            node.scrollHeight - node.scrollTop - node.clientHeight < 60
        }}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        <ChatTranscript>
          {detail.error && (
            <Failure error={detail.error} onRetry={() => detail.refetch()} />
          )}
          {current.last_error && (
            <p
              role="alert"
              className="rounded-md border border-red-500/20 bg-red-500/10 p-3 text-sm text-red-400"
            >
              {current.last_error}
            </p>
          )}
          {events.error && (
            <Failure error={events.error} onRetry={() => events.refetch()} />
          )}
          {capabilities.isPending && (
            <p
              role="status"
              className="text-xs text-white/45 light:text-black/45"
            >
              Checking available session controls…
            </p>
          )}
          {capabilities.error && (
            <div
              role="alert"
              className="flex items-center justify-between gap-3 rounded-md border border-amber-500/20 bg-amber-500/10 p-3 text-xs text-amber-200 light:text-amber-800"
            >
              <span>
                Session controls are unavailable because runtime capabilities
                could not be loaded.
              </span>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => capabilities.refetch()}
              >
                Refresh
              </Button>
            </div>
          )}
          <div
            className="min-h-64 space-y-6"
            aria-label="Saved session history"
          >
            {history.map((event) => {
              const text = eventText(event)
              const failed = event.tool_failed || /failed/.test(event.type)
              const role = openAIEventRole(event)
              const content = (
                <div
                  className={failed ? 'text-red-400 light:text-red-700' : ''}
                >
                  {text &&
                    (role === 'user' ? (
                      <p className="whitespace-pre-wrap text-sm">{text}</p>
                    ) : (
                      <AgentMarkdown variant="chat">{text}</AgentMarkdown>
                    ))}
                  {event.tool_failed && (
                    <p className="text-xs">
                      Tool failed
                      {eventFailure(event) ? `: ${eventFailure(event)}` : ''}
                    </p>
                  )}
                </div>
              )
              return role === 'user' ? (
                <ChatUserMessage key={event.cursor}>{content}</ChatUserMessage>
              ) : (
                <ChatAssistantMessage key={event.cursor}>
                  <div className="py-3 text-sm text-white/90 light:text-black/90">
                    {content}
                  </div>
                </ChatAssistantMessage>
              )
            })}
            {!events.isPending && events.data?.length === 0 && (
              <p className="text-sm text-white/45">No saved events yet.</p>
            )}
          </div>
          {control.error && (
            <div
              role="alert"
              className="flex items-center gap-3 text-sm text-red-400"
            >
              <span>{errorText(control.error)}</span>
              {intent && (
                <Button
                  size="sm"
                  variant="outline"
                  disabled={
                    control.isPending ||
                    !controlsAvailable ||
                    environmentTerminal
                  }
                  onClick={() =>
                    control.mutate({ action: 'input', body: intent })
                  }
                >
                  Recover input
                </Button>
              )}
            </div>
          )}
          {effectiveCapabilities && (
            <OpenAISessionResources
              sessionId={current.id}
              scope={scope}
              sandbox={current.environment.type === 'self_hosted'}
              capabilities={effectiveCapabilities}
            />
          )}
          <details className="rounded-md border border-brand-main-800/50 bg-brand-main-950/20 p-3 text-xs text-white/55 light:text-black/55">
            <summary className="cursor-pointer font-medium">
              Runtime details
            </summary>
            <dl className="mt-3 grid gap-2 sm:grid-cols-2">
              <div>
                <dt className="text-white/40 light:text-black/40">Readiness</dt>
                <dd>{current.readiness}</dd>
              </div>
              <div>
                <dt className="text-white/40 light:text-black/40">Cleanup</dt>
                <dd>{current.cleanup_status || 'Active'}</dd>
              </div>
              {current.sandbox_id && (
                <div>
                  <dt className="text-white/40 light:text-black/40">Sandbox</dt>
                  <dd className="break-all font-mono">{current.sandbox_id}</dd>
                </div>
              )}
            </dl>
          </details>
        </ChatTranscript>
      </div>
      <div className="shrink-0 border-t border-brand-main-800/30">
        <div className="mx-auto max-w-3xl px-4 py-3">
          <ChatComposer
            id="openai-followup"
            value={followup}
            onChange={setFollowup}
            onSubmit={submit}
            disabled={inputDisabled}
            submitting={control.isPending}
            placeholder={
              environmentTerminal
                ? 'This sandbox is no longer available'
                : control.isPending && control.variables?.action === 'input'
                  ? 'Sending message…'
                  : intent
                    ? 'Input outcome is ambiguous; use Recover input above'
                    : 'Message agent…'
            }
          />
          {environmentTerminal && (
            <p className="mt-2 text-xs text-white/55 light:text-black/55">
              This session’s sandbox is no longer available. Start a new session
              to continue.
            </p>
          )}
        </div>
      </div>
      <AlertDialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <AlertDialogContent className="border-brand-main-700 bg-brand-main-900 text-brand-main-100">
          <AlertDialogHeader>
            <AlertDialogTitle>Delete OpenAI session?</AlertDialogTitle>
            <AlertDialogDescription>
              Export any retained files you need first. This removes the saved
              session and releases its environment.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={control.isPending}>
              Cancel
            </AlertDialogCancel>
            <AlertDialogAction
              className="bg-red-500/90 text-white hover:bg-red-500"
              disabled={control.isPending || !controlsAvailable}
              onClick={(event) => {
                event.preventDefault()
                control.mutate({ action: 'delete' })
                setDeleteOpen(false)
              }}
            >
              Delete session
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  )
}

function eventFailure(event: OpenAIEvent): string {
  const value = event.payload.error ?? event.payload.message
  if (typeof value === 'string') return value
  if (
    value &&
    typeof value === 'object' &&
    'message' in value &&
    typeof value.message === 'string'
  )
    return value.message
  return ''
}

export function RuntimeBadge() {
  return (
    <span className="rounded bg-brand-secondary-600/20 px-1.5 py-0.5 text-[10px] font-medium text-brand-secondary-300 light:text-brand-secondary-700">
      OpenAI
    </span>
  )
}
function Failure({
  error,
  onRetry,
}: {
  error: unknown
  onRetry: () => unknown
}) {
  return (
    <div
      role="alert"
      className="flex h-full items-center justify-center gap-3 p-6 text-sm text-red-400"
    >
      <span>{errorText(error)}</span>
      <Button size="sm" variant="ghost" onClick={() => onRetry()}>
        Refresh
      </Button>
    </div>
  )
}
