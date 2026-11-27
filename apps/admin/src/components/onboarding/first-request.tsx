import { useEffect, useRef, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { Iconify } from '@everstack/ui/icons'
import { Badge, Button, Textarea } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'
import { useCreateApiKey, useDeleteApiKey } from '@/hooks/vault/use-api-keys'
import {
  bareModel,
  sendAgentTestRequest,
  sendTestRequest,
  type KeyProvider,
  type TestRequestFailure,
} from '@/lib/first-request'
import {
  addKeyCopy,
  applyTrialRefusal,
  FIRST_KEY_TITLE,
  isRunningLow,
  ownKeyChip,
  runningLowLine,
  trialRefusalFromCode,
  trialStatusLine,
  trialTermsLine,
  type ModelTrial,
  type TrialUpdate,
} from '@/lib/model-trial'
import type { OnboardingPath } from '@/stores/onboarding-store'
import { AddKeyCard, type AddedKey } from './add-key-card'
import { gatewayBase } from './shared'
import { TEST_PROMPT } from './snippets'

type ChatTurn =
  | { role: 'user'; content: string }
  | { role: 'assistant'; content: string; model: string; latencyMs: number }
  // A refused free request renders the add-your-key card in place of the
  // reply; `resolved` is set once a key was added from it.
  | { role: 'error'; failure: TestRequestFailure; resolved?: AddedKey }

const PROVIDERS_HREF = '/vault/llm-providers'

// An API key minted during onboarding: the id to revoke it by, and the secret,
// which is shown once and only ever held in memory.
export interface TestKey {
  id: string
  secret: string
}

// Errors a provider-page visit fixes, versus a fresh API key.
const PROVIDER_FIXES: readonly TestRequestFailure['kind'][] = [
  'no-provider',
  'provider-auth',
  'model-unavailable',
]

// Errors where sending the same message again is the fix: a pause, a blip
// on the network, or an upstream error.
function isRetryable(failure: TestRequestFailure): boolean {
  if (failure.retryable === false) return false
  return (
    failure.kind === 'rate-limited' ||
    failure.kind === 'unreachable' ||
    (failure.kind === 'failed' &&
      (failure.status === undefined || failure.status >= 500))
  )
}

// InlineFirstRequest lets the user send a real request to their new agent (or
// the gateway) straight from the console and see the reply, the model that
// answered, and the latency, instead of copying a snippet elsewhere. It
// creates an "Onboarding key" on first send (unless the API key step already
// made one), then reuses it. The secret lives only in component state; it is
// never logged or persisted. A key the gateway rejects is revoked before a
// replacement is created, so retries do not pile up unused keys.
// The gateway path sends the running transcript so it feels like a
// conversation. The agent path runs a turn through the sessions API with the
// same key (the calls the SDK quickstart makes) and keeps the session, so
// follow-ups land in one conversation the agent remembers.
//
// Free requests (model trial): when `usingTrial` is set, `model` is the trial
// model and the line under the composer shows what is left. A refusal from
// the gateway ("model_trial_*") renders the add-your-key card in place of the
// reply; saving a key there resends the refused message on the new provider.
// With `trial` null the component behaves as if the feature did not exist.
export function InlineFirstRequest({
  activePath,
  firstAgentId,
  agentModel,
  model,
  testKey,
  onKey,
  trial = null,
  usingTrial = false,
  onTrialUpdate,
  loading = false,
  keyProvider,
  onKeyProviderChange,
}: {
  activePath: OnboardingPath
  firstAgentId: string | null
  // The model the agent runs on, shown next to its reply.
  agentModel?: string
  // Gateway model reference the instance can serve, e.g.
  // "@anthropic/claude-haiku-4-5", or the bare trial model while free
  // requests are in use. Null when nothing is routable yet, in which case no
  // request is sent.
  model: string | null
  testKey: TestKey | null
  onKey: (key: TestKey | null) => void
  trial?: ModelTrial | null
  usingTrial?: boolean
  // What the last send learned about the free requests (count, refusal, or
  // "re-read"), so the caller keeps the trial state in step with the UI.
  onTrialUpdate?: (update: TrialUpdate) => void
  // The reads that decide `model` (providers, free requests) are still in
  // flight: hold the composer's place rather than flash "add a key".
  loading?: boolean
  // The provider picked on the add-key card, lifted so the quickstart
  // snippet names the model that card will enable.
  keyProvider?: KeyProvider
  onKeyProviderChange?: (provider: KeyProvider) => void
}) {
  const navigate = useNavigate()
  const createApiKey = useCreateApiKey()
  const deleteApiKey = useDeleteApiKey()
  const [turns, setTurns] = useState<ChatTurn[]>([])
  const [draft, setDraft] = useState(TEST_PROMPT)
  const [sending, setSending] = useState(false)
  // A provider added from the inline card, until the providers query
  // catches up and `model` carries it.
  const [addedModel, setAddedModel] = useState<string | null>(null)
  const [lastSent, setLastSent] = useState<string | null>(null)
  // The agent session the test conversation runs on. Dropped after a failed
  // send, so a retry starts clean instead of continuing a broken session.
  const [agentSession, setAgentSession] = useState<string | null>(null)

  const openProviders = () => navigate({ to: PROVIDERS_HREF })
  const openBilling = () =>
    navigate({
      to: '/settings/billing',
      search: { upgrade_success: false, plan: undefined },
    })
  // A key just added here wins until the providers query catches up.
  const activeModel = addedModel ?? model
  const onTrialModel = usingTrial && addedModel === null
  const logRef = useRef<HTMLDivElement>(null)

  // Keep the newest turn in view.
  useEffect(() => {
    const el = logRef.current
    if (el) el.scrollTop = el.scrollHeight
  }, [turns.length, sending])

  const keyCardProps = {
    provider: keyProvider,
    onProviderChange: onKeyProviderChange,
    onOtherProvider: openProviders,
  }
  // The add-key card's headline when no refusal is on screen: why free
  // requests cannot serve the next message, or a plain first step when
  // there were none.
  const keyTitle =
    trial && trial.state !== 'active'
      ? addKeyCopy(trial.state, trial).title
      : FIRST_KEY_TITLE

  if (activeModel === null && turns.length === 0) {
    // Hold the composer's place while the model is still being resolved.
    if (loading) return <RunnerSkeleton />
    // Nothing to route to yet: say so up front instead of sending a request
    // that can only fail. On the gateway path a key of your own is the fix,
    // so the card is right here; an agent runs on its own model, which
    // needs its provider connected.
    if (activePath === 'gateway') {
      return (
        <AddKeyCard
          title={keyTitle}
          saveLabel="Add key"
          onSaved={(added) => setAddedModel(added.model)}
          {...keyCardProps}
        />
      )
    }
    return <NoModelNotice onConnect={openProviders} />
  }

  const send = () => {
    const text = draft.trim()
    if (!text || sending || activeModel === null) return
    setDraft('')
    return sendMessage(text, { model: activeModel, echoUser: true })
  }

  const sendMessage = async (
    text: string,
    opts: { model: string; echoUser: boolean },
  ) => {
    if (sending) return
    setSending(true)
    setLastSent(text)
    const history = turns.flatMap((t) =>
      t.role === 'error' ? [] : [{ role: t.role, content: t.content }],
    )
    if (opts.echoUser) {
      setTurns((prev) => [...prev, { role: 'user', content: text }])
    } else if (history.at(-1)?.role === 'user') {
      // A resend: the refused message is already the last turn.
      history.pop()
    }
    const model = opts.model
    // Free requests are bounded on the whole input (about 4,000 bytes), so a
    // trial send carries only the latest message; a key of your own gets the
    // running transcript.
    const onTrial = onTrialModel && model === activeModel
    try {
      let key = testKey
      if (!key) {
        const res = await createApiKey.mutateAsync({
          name: 'Onboarding key',
          type: ApiKeyType.USER,
        })
        key =
          res.apiKey?.id && res.apiKey.hash
            ? { id: res.apiKey.id, secret: res.apiKey.hash }
            : null
        if (key) onKey(key)
      }
      if (!key) throw new Error('Could not create an API key for the test')

      const base = gatewayBase()
      let result
      if (activePath === 'gateway') {
        result = await sendTestRequest({
          url: `${base}/v1/chat/completions`,
          apiKey: key.secret,
          body: {
            model,
            messages: onTrial
              ? [{ role: 'user', content: text }]
              : [...history, { role: 'user', content: text }],
          },
          requestedModel: model,
        })
      } else {
        if (!firstAgentId) throw new Error('Create your agent first')
        const sent = await sendAgentTestRequest({
          base,
          agentId: firstAgentId,
          apiKey: key.secret,
          message: text,
          sessionId: agentSession,
          agentModel,
        })
        setAgentSession(sent.ok ? (sent.sessionId ?? null) : null)
        result = sent
      }
      setTurns((prev) => [
        ...prev,
        result.ok
          ? {
              role: 'assistant',
              content: result.reply,
              model: result.model,
              latencyMs: result.latencyMs,
            }
          : { role: 'error', failure: result },
      ])
      // Keep the free-request state honest: a refusal moves it at once (so
      // the status line agrees with the card), the header gives the exact
      // count, and without either the caller re-reads it.
      if (!result.ok && result.kind === 'trial') {
        onTrialUpdate?.({
          kind: 'refused',
          reason: trialRefusalFromCode(result.code) ?? 'not_eligible',
        })
      } else if (result.trialRemaining !== undefined) {
        onTrialUpdate?.({ kind: 'remaining', remaining: result.trialRemaining })
      } else if (usingTrial) {
        onTrialUpdate?.({ kind: 'reread' })
      }
    } catch (err) {
      setTurns((prev) => [
        ...prev,
        {
          role: 'error',
          failure: {
            ok: false,
            kind: 'failed',
            title: 'Could not send the test request',
            detail:
              err instanceof Error ? err.message : 'The request did not go out.',
          },
        },
      ])
    } finally {
      setSending(false)
    }
  }

  // Revoke the rejected key first, then clear it so the next send creates a
  // fresh one. If revoking fails the key is kept, so no second key is minted.
  const resetKey = async () => {
    if (!testKey) return
    try {
      await deleteApiKey.mutateAsync(testKey.id)
      onKey(null)
    } catch {
      setTurns((prev) => [
        ...prev,
        {
          role: 'error',
          failure: {
            ok: false,
            kind: 'failed',
            title: 'Could not remove the rejected key',
            detail: 'Delete it from API keys, then try again.',
            retryable: false,
          },
        },
      ])
    }
  }

  // A key added from a refusal card: remember the provider, mark the card
  // done, and resend the refused message on the new provider.
  const resolveRefusal = (index: number, added: AddedKey) => {
    setAddedModel(added.model)
    setTurns((prev) =>
      prev.map((t, i) =>
        i === index && t.role === 'error' ? { ...t, resolved: added } : t,
      ),
    )
    if (lastSent) {
      void sendMessage(lastSent, { model: added.model, echoUser: false })
    }
  }

  // Resend the last message after a retryable error.
  const retry = () => {
    if (lastSent && activeModel !== null) {
      void sendMessage(lastSent, { model: activeModel, echoUser: false })
    }
  }

  const ownKey = trial !== null && !onTrialModel
  // An unanswered free-request refusal: its card is the next step, so the
  // footer does not repeat it.
  const pendingRefusal = [...turns]
    .reverse()
    .find((t) => t.role === 'error' && t.failure.kind === 'trial' && !t.resolved)
  const refusalPending = pendingRefusal !== undefined
  // A refusal that ends the free requests (used up, expired, paused) closes
  // the message box at once, before the re-read reaches the parent, so the
  // "N left" line can never sit under a card that says none are left.
  // Input-shaped refusals (too long, text only) leave it open.
  const refusalReason =
    pendingRefusal?.role === 'error'
      ? (trialRefusalFromCode(pendingRefusal.failure.code) ?? 'not_eligible')
      : null
  const closedByRefusal =
    onTrialModel &&
    trial !== null &&
    refusalReason !== null &&
    applyTrialRefusal(trial, refusalReason).state !== 'active'
  const canSend = activeModel !== null && !closedByRefusal
  const lastIndex = turns.length - 1

  return (
    <div className="space-y-2.5">
      {turns.length > 0 && (
        <div
          ref={logRef}
          role="log"
          aria-label="Test conversation"
          aria-live="polite"
          className="max-h-72 space-y-2 overflow-auto pr-1 scrollbar-macos"
        >
          {turns.map((turn, i) => (
            <ChatBubble
              key={i}
              turn={turn}
              trial={trial}
              isLast={i === lastIndex}
              sending={sending}
              keyCardProps={keyCardProps}
              onOpenProviders={openProviders}
              onOpenBilling={openBilling}
              onResetKey={() => void resetKey()}
              onRetry={retry}
              onKeyAdded={(added) => resolveRefusal(i, added)}
            />
          ))}
          {sending && (
            <div className="flex items-center gap-1.5 px-1 text-xs text-brand-main-300">
              <Iconify.Icon
                icon="lucide:loader-2"
                className="size-3.5 animate-spin"
              />
              Waiting for the reply
            </div>
          )}
        </div>
      )}
      {!canSend || activeModel === null ? (
        // Free requests ran out mid-conversation (the last one succeeded,
        // so no refusal card is showing): the key card takes the message
        // box's place.
        refusalPending ? null : activePath === 'gateway' ? (
          <AddKeyCard
            title={keyTitle}
            saveLabel="Add key"
            autoFocus
            onSaved={(added) => setAddedModel(added.model)}
            {...keyCardProps}
          />
        ) : (
          <NoModelNotice onConnect={openProviders} />
        )
      ) : (
        <>
          <div className="flex items-end gap-2">
            <Textarea
              aria-label="Test message"
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !e.shiftKey) {
                  e.preventDefault()
                  void send()
                }
              }}
              rows={1}
              placeholder={
                activePath === 'gateway'
                  ? 'Send a message to the gateway'
                  : 'Send your agent a message'
              }
              className="min-h-9 flex-1 resize-none border-brand-main-700 bg-brand-main-900 text-xs text-brand-main-50 placeholder:text-brand-main-400"
            />
            <Button
              onClick={() => void send()}
              disabled={sending || !draft.trim()}
              aria-busy={sending || undefined}
              className="gap-2"
            >
              {sending ? (
                <Iconify.Icon
                  icon="lucide:loader-2"
                  className="size-4 animate-spin"
                />
              ) : (
                <Iconify.Icon icon="lucide:send" className="size-4" />
              )}
              {turns.length === 0 ? 'Send test request' : 'Send'}
            </Button>
          </div>
          {onTrialModel && trial ? (
            <div className="space-y-1 text-[11px] leading-4">
              {isRunningLow(trial) ? (
                <>
                  <p className="flex items-center gap-1.5 text-warning">
                    <Iconify.Icon
                      icon="lucide:triangle-alert"
                      className="size-3 shrink-0"
                    />
                    {runningLowLine(trial)}
                  </p>
                  <p className="text-brand-main-300">{trialTermsLine(trial)}</p>
                </>
              ) : (
                <p className="text-brand-main-300">{trialStatusLine(trial)}</p>
              )}
            </div>
          ) : (
            <p className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-brand-main-300">
              {ownKey && (
                <Badge variant="secondary">{ownKeyChip(activeModel)}</Badge>
              )}
              <span>
                {activePath === 'gateway'
                  ? testKey
                    ? `Sends a real request to ${bareModel(activeModel) ?? 'your model'} with your onboarding API key.`
                    : `Creates an API key named Onboarding key, then sends a real request to ${bareModel(activeModel) ?? 'your model'}.`
                  : testKey
                    ? 'Runs a real turn on your agent with your onboarding API key.'
                    : 'Creates an API key named Onboarding key, then runs a real turn on your agent.'}
              </span>
            </p>
          )}
        </>
      )}
    </div>
  )
}

// Holds the message box's place while the model is being resolved, at the
// same height, so nothing jumps when it arrives.
function RunnerSkeleton() {
  return (
    <div
      role="status"
      aria-busy="true"
      aria-label="Loading"
      className="space-y-2.5"
    >
      <div className="flex items-end gap-2">
        <div className="h-9 flex-1 animate-pulse rounded bg-brand-main-800/30" />
        <div className="h-9 w-36 animate-pulse rounded bg-brand-main-800/30" />
      </div>
      <div className="h-4 w-2/3 animate-pulse rounded bg-brand-main-800/30" />
    </div>
  )
}

function NoModelNotice({ onConnect }: { onConnect: () => void }) {
  return (
    <div
      role="status"
      className="flex items-start gap-3 rounded border border-warning-border bg-warning-surface px-3 py-2.5"
    >
      <Iconify.Icon
        icon="lucide:plug-zap"
        className="mt-0.5 size-4 shrink-0 text-warning"
      />
      <div className="min-w-0 flex-1 space-y-2">
        <div>
          <p className="text-xs font-medium text-brand-main-50">
            No model to send to yet
          </p>
          <p className="mt-0.5 text-xs leading-5 text-brand-main-200">
            Connect OpenAI, Anthropic, or another provider and enable a model.
            The test request and the snippet above switch to it.
          </p>
        </div>
        <Button size="sm" onClick={onConnect} className="gap-2">
          <Iconify.Icon icon="hugeicons:ai-lock" className="size-4" />
          Connect a provider
        </Button>
      </div>
    </div>
  )
}

type KeyCardProps = Pick<
  Parameters<typeof AddKeyCard>[0],
  'provider' | 'onProviderChange' | 'onOtherProvider'
>

function ChatBubble({
  turn,
  trial,
  isLast,
  sending,
  keyCardProps,
  onOpenProviders,
  onOpenBilling,
  onResetKey,
  onRetry,
  onKeyAdded,
}: {
  turn: ChatTurn
  trial: ModelTrial | null
  // Only the newest error offers a retry, and a fresh refusal takes focus.
  isLast: boolean
  sending: boolean
  keyCardProps: KeyCardProps
  onOpenProviders: () => void
  onOpenBilling: () => void
  onResetKey: () => void
  onRetry: () => void
  onKeyAdded: (added: AddedKey) => void
}) {
  if (turn.role === 'user') {
    return (
      <div className="ml-auto w-fit max-w-[85%] whitespace-pre-wrap break-words rounded border border-brand-secondary-500/30 bg-brand-secondary-500/10 px-3 py-1.5 text-xs leading-5 text-brand-secondary-100">
        {turn.content}
      </div>
    )
  }
  if (turn.role === 'error' && turn.failure.kind === 'trial') {
    if (turn.resolved) {
      return (
        <p className="flex items-center gap-1.5 px-1 text-[11px] text-brand-main-300">
          <Iconify.Icon icon="lucide:check" className="size-3 text-success" />
          {ownKeyChip(turn.resolved.model)}. Sending again.
        </p>
      )
    }
    const reason = trialRefusalFromCode(turn.failure.code) ?? 'not_eligible'
    return (
      <AddKeyCard
        title={addKeyCopy(reason, trial, turn.failure.detail).title}
        autoFocus={isLast}
        onSaved={onKeyAdded}
        {...keyCardProps}
      />
    )
  }
  if (turn.role === 'error') {
    return (
      <ErrorCard
        failure={turn.failure}
        onOpenProviders={onOpenProviders}
        onOpenBilling={onOpenBilling}
        onResetKey={onResetKey}
        onRetry={isLast && isRetryable(turn.failure) ? onRetry : undefined}
        retryDisabled={sending}
      />
    )
  }
  return (
    <div className="max-w-[85%] space-y-1.5 rounded border border-brand-main-700 bg-brand-main-900 px-3 py-2 text-xs leading-5 text-brand-main-100">
      <p className="whitespace-pre-wrap break-words">{turn.content}</p>
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant="success">
          <Iconify.Icon icon="lucide:check" className="size-3" />
          Works
        </Badge>
        {turn.model && <Badge variant="secondary">{turn.model}</Badge>}
        <Badge variant="secondary">{turn.latencyMs} ms</Badge>
      </div>
    </div>
  )
}

// Seconds left before a retry is allowed, counting down from Retry-After.
function useCountdown(seconds: number | undefined): number {
  const [left, setLeft] = useState(seconds ?? 0)
  useEffect(() => {
    if (!seconds) return
    setLeft(seconds)
    const timer = window.setInterval(() => {
      setLeft((n) => {
        if (n <= 1) window.clearInterval(timer)
        return Math.max(0, n - 1)
      })
    }, 1000)
    return () => window.clearInterval(timer)
  }, [seconds])
  return left
}

function ErrorCard({
  failure,
  onOpenProviders,
  onOpenBilling,
  onResetKey,
  onRetry,
  retryDisabled = false,
  className,
}: {
  failure: TestRequestFailure
  onOpenProviders: () => void
  onOpenBilling: () => void
  onResetKey: () => void
  // Set when sending the same message again is the fix.
  onRetry?: () => void
  retryDisabled?: boolean
  className?: string
}) {
  const wait = useCountdown(onRetry ? failure.retryAfterSeconds : undefined)
  const action = PROVIDER_FIXES.includes(failure.kind) ? (
    <Button size="xs" variant="outline" onClick={onOpenProviders}>
      Open providers
    </Button>
  ) : failure.kind === 'key-rejected' ? (
    <Button size="xs" variant="outline" onClick={onResetKey}>
      Use a new key
    </Button>
  ) : failure.kind === 'plan-limit' ? (
    <Button size="xs" variant="outline" onClick={onOpenBilling}>
      Open billing
    </Button>
  ) : onRetry ? (
    <Button
      size="xs"
      variant="outline"
      onClick={onRetry}
      disabled={retryDisabled || wait > 0}
      className="tabular-nums"
    >
      {wait > 0 ? `Try again in ${wait}s` : 'Try again'}
    </Button>
  ) : null
  return (
    <div
      role="alert"
      className={cn(
        'max-w-[85%] rounded border border-danger-border bg-danger-surface px-3 py-2.5 text-xs leading-5',
        className,
      )}
    >
      <div className="flex items-start gap-2">
        <Iconify.Icon
          icon="lucide:triangle-alert"
          className="mt-0.5 size-3.5 shrink-0 text-danger"
        />
        <div className="min-w-0 flex-1">
          <p className="font-medium text-danger">{failure.title}</p>
          <p className="mt-0.5 text-brand-main-200">
            {/* The retry button counts the wait down, so the copy does not
                quote a number that goes stale. */}
            {onRetry && failure.retryAfterSeconds && failure.detailWithoutWait
              ? failure.detailWithoutWait
              : failure.detail}
          </p>
        </div>
      </div>
      {(action || failure.raw) && (
        <div className="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1.5 pl-5.5">
          {action}
          {failure.raw && (
            <details className="min-w-0 basis-full">
              <summary className="w-fit cursor-pointer select-none text-[11px] text-brand-main-300 transition-colors hover:text-brand-main-100">
                Gateway response
                {failure.status ? ` (${failure.status})` : ''}
              </summary>
              <pre className="mt-1.5 max-h-32 overflow-auto whitespace-pre-wrap break-all rounded border border-brand-main-800 bg-brand-main-950 p-2 text-[11px] leading-4 text-brand-main-200 scrollbar-macos">
                {failure.raw}
              </pre>
            </details>
          )}
        </div>
      )}
    </div>
  )
}
