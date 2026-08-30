import { useState, useCallback, useRef } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { ArrowRight, Loader2 } from 'lucide-react'
import { Iconify } from '@everstack/ui/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth/use-auth'
import { Button } from '@everstack/ui/components'
import { ContextSelector } from './context-selector'
import { ModelPicker } from '@/components/providers/model-picker'
import {
  MentionComposerInput,
  type MentionComposerInputHandle,
} from '@/components/deployments/agents/mention-composer-input'
import { PinnedAgents } from './pinned-agents'
import { RecentSessions } from './recent-sessions'
import { getOrCreatePlatformAgent } from '@/server/platform-agent'
import { createSession } from '@/server/agents'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import { LaunchCenter } from '@/components/onboarding/launch-center'
import { useOnboarding } from '@/hooks/use-onboarding'
import { EverstackLogo } from '@/components/brand/everstack-logo'

const quickTopics = [
  { label: 'Agents', icon: 'lucide:bot' },
  { label: 'Observability', icon: 'hugeicons:telescope-02' },
  { label: 'Gateway', icon: 'mingcute:route-line' },
  { label: 'Evaluations', icon: 'lucide:flask-conical' },
  { label: 'Vault', icon: 'stash:vault' },
  { label: 'Sandbox', icon: 'lucide:box' },
]

export function HomePage() {
  const { data: session } = useSession()
  const {
    launchAllComplete,
    dismissed: onboardingDismissed,
    celebrationShown,
    hydrated: onboardingHydrated,
  } = useOnboarding()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const composerRef = useRef<MentionComposerInputHandle>(null)

  const orgId = session?.user?.organizations?.[0]?.id ?? ''

  const { data: platformAgent } = useQuery({
    queryKey: ['platform-agent'],
    queryFn: getOrCreatePlatformAgent,
    staleTime: Infinity,
  })

  const [userInput, setUserInput] = useState('')
  const [context, setContext] = useState('auto')
  const [model, setModel] = useState('')
  const [sending, setSending] = useState(false)

  const userName =
    session?.user?.user?.name?.split(' ')[0] ||
    session?.user?.user?.email?.split('@')[0] ||
    ''

  const handleSubmit = useCallback(async () => {
    const text = userInput.trim()
    if (!text || sending || !platformAgent?.id) return
    setUserInput('')
    setSending(true)

    try {
      // Create session
      const resp = await createSession({ tenantId: orgId, agentId: platformAgent.id })
      const newSessionId = resp.session?.id
      if (!newSessionId) return

      // Start turn first so events flow into the store
      useAgentSessionStore.getState().startTurn(
        newSessionId, orgId, text, queryClient,
        model ? { modelOverride: model } : undefined,
      )

      // Small delay for store to register, then navigate
      await new Promise((r) => setTimeout(r, 50))
      navigate({ to: '/chat', search: { session: newSessionId, model: model || undefined } })
    } finally {
      setSending(false)
    }
  }, [userInput, sending, platformAgent?.id, orgId, model, queryClient, navigate])

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
            Manage agents, query traces, and control your platform.
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
          <div className="flex items-center justify-between px-3 pb-2.5">
            <div className="flex items-center gap-1.5">
              <ContextSelector value={context} onChange={setContext} />
            </div>
            <div className="flex items-center gap-1.5">
              <ModelPicker value={model} onChange={setModel} variant="compact" />
              <Button
                variant={userInput.trim() ? 'default' : 'ghost'}
                onClick={handleSubmit}
                disabled={!userInput.trim() || sending}
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

        {/* Quick topic pills */}
        <div className="flex flex-wrap items-center justify-center gap-2">
          {quickTopics.map((topic) => (
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

        {/* Agents + Recent sessions */}
        <div className="grid grid-cols-2 gap-6 pt-2 max-w-lg mx-auto">
          <PinnedAgents />
          <RecentSessions />
        </div>
      </div>
    </div>
  )
}
