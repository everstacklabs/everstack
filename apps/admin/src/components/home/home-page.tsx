import { useState, useCallback, useRef } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ArrowRight, Loader2 } from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth/use-auth'
import { Button } from '@everstack/ui/components'
import { ModelPicker } from '@/components/providers/model-picker'
import {
  MentionComposerInput,
  type MentionComposerInputHandle,
} from '@/components/deployments/agents/mention-composer-input'
import { PinnedAgents } from './pinned-agents'
import { RecentSessions } from './recent-sessions'
import { QuickTopics } from './quick-topics'
import { AssistantSetupCard, useStickyError } from './assistant-setup'
import {
  getOrCreatePlatformAgent,
  NoModelProviderError,
} from '@/server/platform-agent'
import { createSession } from '@/server/agents'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import { LaunchCenter } from '@/components/onboarding/launch-center'
import { useOnboarding } from '@/hooks/use-onboarding'
import { EverstackLogo } from '@/components/brand/everstack-logo'

export function HomePage() {
  const { data: session } = useSession()
  const {
    launchAllComplete,
    dismissed: onboardingDismissed,
    celebrationShown,
    hydrated: onboardingHydrated,
    firstAgentId,
  } = useOnboarding()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const composerRef = useRef<MentionComposerInputHandle>(null)

  const orgId = session?.user?.organizations?.[0]?.id ?? ''

  // Same key and gate as the sidebar and /chat, so the three share one
  // request (and never race each other into creating the assistant twice),
  // and it never fires before the org is known.
  const {
    data: platformAgent,
    isLoading: assistantLoading,
    error: assistantError,
    refetch: refetchAssistant,
  } = useQuery({
    queryKey: ['platform-agent', orgId],
    queryFn: getOrCreatePlatformAgent,
    enabled: !!orgId,
    staleTime: Infinity,
    // A missing provider does not fix itself: skip the retries so the key
    // step shows at once instead of after three failed attempts.
    retry: (count, err) => !(err instanceof NoModelProviderError) && count < 2,
  })

  // The assistant, or the user's own first agent when the assistant could
  // not start: a message always has somewhere to go if anything can take it.
  const agentId = platformAgent?.id || firstAgentId || ''
  // Held across refetches, so the setup card survives its own retry.
  const setupError = useStickyError(assistantError, !!agentId)

  const [userInput, setUserInput] = useState('')
  const [model, setModel] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState<string | null>(null)

  const userName =
    session?.user?.user?.name?.split(' ')[0] ||
    session?.user?.user?.email?.split('@')[0] ||
    ''

  const handleSubmit = useCallback(async () => {
    const text = userInput.trim()
    if (!text || sending || !agentId || !orgId) return
    setUserInput('')
    setSendError(null)
    setSending(true)

    try {
      // Create session
      const resp = await createSession({ tenantId: orgId, agentId })
      const newSessionId = resp.session?.id
      if (!newSessionId) throw new Error('No session was returned.')

      // Start turn first so events flow into the store
      useAgentSessionStore.getState().startTurn(
        newSessionId, orgId, text, queryClient,
        model ? { modelOverride: model } : undefined,
      )

      // Small delay for store to register, then navigate
      await new Promise((r) => setTimeout(r, 50))
      navigate({ to: '/chat', search: { session: newSessionId, model: model || undefined } })
    } catch (err) {
      // Nothing was sent: give the message back instead of losing it.
      setUserInput(text)
      setSendError(
        err instanceof Error && err.message
          ? err.message
          : 'The message could not be sent.',
      )
    } finally {
      setSending(false)
    }
  }, [userInput, sending, agentId, orgId, model, queryClient, navigate])

  const handleKeyDown = useCallback(
    (e: React.KeyboardEvent) => {
      if (e.key === 'Enter' && !e.shiftKey) {
        e.preventDefault()
        handleSubmit()
      }
    },
    [handleSubmit],
  )

  const handleInputChange = useCallback((value: string, _cursor: number) => {
    setUserInput(value)
  }, [])

  const seedPrompt = useCallback((prompt: string) => {
    setUserInput(prompt)
    composerRef.current?.focus()
  }, [])

  // Hold the onboarding decision until server state has hydrated. Guessing
  // before the GET resolves would flash the wrong screen (and on a fresh device
  // could show the launch center to an already-onboarded user).
  if (!onboardingHydrated) {
    return (
      <div className="flex h-full items-center justify-center">
        <Loader2 className="size-5 animate-spin text-white/40 light:text-black/40" />
      </div>
    )
  }

  // Keep the launch center mounted through its completion screen: hide it only
  // once the user has dismissed it, or once setup is complete AND the "you're
  // live" moment has been acknowledged (celebrationShown).
  if (!onboardingDismissed && !(launchAllComplete && celebrationShown)) {
    return <LaunchCenter />
  }

  // Still resolving the assistant: the send button waits rather than
  // swallowing the message.
  const resolving = !agentId && (assistantLoading || !orgId)

  return (
    <div className="flex h-full flex-col items-center justify-center overflow-y-auto scrollbar-macos">
      <div className="w-full max-w-2xl px-6 space-y-6 -mt-8">
        {/* Logo + greeting */}
        <div className="text-center space-y-2">
          <EverstackLogo className="mx-auto mb-4" />
          <h1 className="text-2xl font-semibold text-white light:text-brand-main-50">
            {userName ? `Hello ${userName}` : 'How can I help you?'}
          </h1>
          <p className="text-sm text-white/40 light:text-black/40">
            Create and manage agents by chat, or jump to any part of the platform.
          </p>
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
          <div className="flex items-center justify-end gap-1.5 px-3 pb-2.5">
            <ModelPicker value={model} onChange={setModel} variant="compact" />
            <Button
              variant={userInput.trim() ? 'default' : 'ghost'}
              onClick={handleSubmit}
              disabled={!userInput.trim() || sending || !agentId}
              aria-busy={sending || resolving || undefined}
              className="h-6 w-6"
            >
              {sending || resolving ? (
                <Loader2 className="size-4 animate-spin" />
              ) : (
                <ArrowRight className="size-4" />
              )}
            </Button>
          </div>
        </div>

        {setupError ? (
          <AssistantSetupCard
            error={setupError}
            retry={async () => Boolean((await refetchAssistant()).data)}
          />
        ) : null}

        {sendError ? (
          <p role="alert" className="text-center text-xs text-danger">
            {sendError}
          </p>
        ) : null}

        {/* Quick topic pills */}
        <QuickTopics onPrompt={seedPrompt} />

        {/* Agents + Recent sessions */}
        <div className="grid grid-cols-2 gap-6 pt-2 max-w-lg mx-auto">
          <PinnedAgents />
          <RecentSessions />
        </div>
      </div>
    </div>
  )
}
