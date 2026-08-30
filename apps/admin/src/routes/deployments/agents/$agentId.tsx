import {
  createFileRoute,
  Link,
  Outlet,
  useMatchRoute,
} from '@tanstack/react-router'
import {
  useAgent,
  useSession_,
  useCompleteSession,
} from '@/hooks/deployments/use-agents'
import { useAgentChatSession } from '@/hooks/deployments/use-agent-chat-session'
import { useAgentLifecycleStream } from '@/hooks/deployments/use-agent-lifecycle-stream'
import { useAgentSessionStore } from '@/stores/agent-session-store'
import {
  AgentLifecycleMode,
  AgentLifecycleStatus,
  SessionStatus,
} from '@/server/agents'
import { ChatSessionSwitcher } from '@/components/deployments/agents/chat-session-switcher'
import { Loader } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useCallback, useEffect, useState } from 'react'
import { getSandboxInstance } from '@/server/sandbox'
import { useSession } from '@/hooks/auth/use-auth'
import {
  AlertTriangle,
  Moon,
  RefreshCw,
  Terminal,
  Box,
  Workflow,
} from 'lucide-react'
import { useSidePanelStore } from '@/stores/side-panel-store'

export const Route = createFileRoute('/deployments/agents/$agentId')({
  component: AgentDetailLayout,
})

const { Tabs, TabsList, TabsTrigger } = ui
const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

const STATUS_STYLES: Record<number, { label: string; className: string }> = {
  [SessionStatus.CREATED]: {
    label: 'Created',
    className: 'bg-brand-main-700/40 text-brand-main-200',
  },
  [SessionStatus.RUNNING]: {
    label: 'Running',
    className: 'bg-brand-secondary-600/20 text-brand-secondary-300',
  },
  [SessionStatus.WAITING_FOR_INPUT]: {
    label: 'Waiting',
    className: 'bg-yellow-500/15 text-yellow-300 light:text-yellow-700',
  },
  [SessionStatus.WAITING_FOR_APPROVAL]: {
    label: 'Approval',
    className: 'bg-orange-500/15 text-orange-300 light:text-orange-600',
  },
  [SessionStatus.COMPLETED]: {
    label: 'Completed',
    className: 'bg-green-500/15 text-green-300 light:text-green-600',
  },
  [SessionStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-red-500/15 text-red-300 light:text-red-600',
  },
  [SessionStatus.CANCELLED]: {
    label: 'Cancelled',
    className: 'bg-gray-500/20 text-gray-400 light:text-gray-600',
  },
}

// ── Sandbox health state for persistent agents ──────────────────────

type SandboxHealthState =
  | { status: 'healthy' }
  | { status: 'loading'; message?: string }
  | { status: 'reconnecting'; message: string }
  | { status: 'sleeping'; message: string }
  | { status: 'waking'; message: string }
  | { status: 'failed'; message: string }
  | { status: 'terminated'; message: string }

function useSandboxHealth(
  agent:
    | { sandboxId?: string; lifecycleStatus?: number; lifecycleMode?: number }
    | undefined,
): SandboxHealthState {
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''

  const sandboxId = agent?.sandboxId
  const isPersistent = agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
  const lifecycleStatus = agent?.lifecycleStatus

  const {
    data: sandboxInstance,
    error: sandboxError,
    isLoading,
  } = useQuery({
    queryKey: ['sandbox-health', sandboxId],
    queryFn: () => getSandboxInstance(orgId, sandboxId!),
    enabled: isPersistent && !!sandboxId && !!orgId,
    refetchInterval: 30_000,
    retry: false,
    staleTime: 15_000,
  })

  if (!isPersistent) return { status: 'healthy' }

  // Lifecycle status is authoritative
  if (lifecycleStatus === AgentLifecycleStatus.FAILED) {
    return {
      status: 'failed',
      message:
        'The sandbox environment failed to start. You can try re-provisioning.',
    }
  }
  if (lifecycleStatus === AgentLifecycleStatus.TERMINATED) {
    return {
      status: 'terminated',
      message:
        "This agent's sandbox has been terminated. Re-provision to continue.",
    }
  }
  if (lifecycleStatus === AgentLifecycleStatus.SLEEPING) {
    return {
      status: 'sleeping',
      message: 'Sleeping. Will wake automatically when you send a message.',
    }
  }
  if (lifecycleStatus === AgentLifecycleStatus.WAKING) {
    return { status: 'waking', message: 'Waking up...' }
  }
  if (
    lifecycleStatus === AgentLifecycleStatus.PROVISIONING ||
    lifecycleStatus === AgentLifecycleStatus.CREATED
  ) {
    if (sandboxInstance?.status?.toLowerCase() === 'pending') {
      return {
        status: 'loading',
        message: 'Sandbox is still starting in the cluster.',
      }
    }
    return { status: 'loading', message: 'Preparing sandbox environment...' }
  }

  // For idle/running agents, verify the sandbox actually exists
  if (!sandboxId) {
    // No sandbox_id — server is likely auto-reprovisioning after restart
    return {
      status: 'reconnecting',
      message: 'Setting up sandbox environment...',
    }
  }

  if (isLoading) return { status: 'loading' }

  // Sandbox is gone (404 or error) — server auto-reprovisions, show reconnecting
  if (sandboxError) {
    return { status: 'reconnecting', message: 'Reconnecting to sandbox...' }
  }

  if (sandboxInstance) {
    const sbStatus = sandboxInstance.status?.toLowerCase()
    if (sbStatus === 'stopped' || sbStatus === 'exited') {
      return { status: 'reconnecting', message: 'Restarting sandbox...' }
    }
    if (sbStatus === 'failed' || sbStatus === 'error') {
      return {
        status: 'failed',
        message: 'The sandbox is in a failed state. Try re-provisioning.',
      }
    }
  }

  return { status: 'healthy' }
}

// ── Sandbox health indicator ────────────────────────────────────────

function SandboxHealthIndicator({
  health,
  onRetry,
  isRetrying,
}: {
  health: SandboxHealthState
  onRetry: () => void
  isRetrying: boolean
}) {
  if (health.status === 'healthy') return null

  if (health.status === 'loading') {
    return (
      <div className="mx-4 mt-2">
        <div className="flex items-center gap-2.5 rounded-md border border-brand-main-600/30 bg-brand-main-800/40 px-3 py-2">
          <span className="h-3.5 w-3.5 rounded-full border-[1.5px] border-brand-secondary-400 border-t-transparent animate-spin shrink-0" />
          <span className="text-xs text-white/50 light:text-black/50">
            {health.message ?? 'Preparing sandbox environment...'}
          </span>
        </div>
      </div>
    )
  }

  // Reconnecting / waking — subtle inline bar, no action needed
  if (health.status === 'reconnecting' || health.status === 'waking') {
    return (
      <div className="mx-4 mt-2">
        <div className="flex items-center gap-2.5 rounded-md border border-brand-main-600/30 bg-brand-main-800/40 px-3 py-2">
          <span className="h-3.5 w-3.5 rounded-full border-[1.5px] border-brand-secondary-400 border-t-transparent animate-spin shrink-0" />
          <span className="text-xs text-white/50 light:text-black/50">{health.message}</span>
        </div>
      </div>
    )
  }

  // Sleeping — subtle pill
  if (health.status === 'sleeping') {
    return (
      <div className="mx-4 mt-2">
        <div className="flex items-center gap-2.5 rounded-md border border-brand-main-600/30 bg-brand-main-800/40 px-3 py-2">
          <Moon className="w-3.5 h-3.5 text-white/30 light:text-black/30 shrink-0" />
          <span className="text-xs text-white/50 light:text-black/50">{health.message}</span>
        </div>
      </div>
    )
  }

  // Failed / terminated — needs user action
  return (
    <div className="mx-4 mt-2">
      <div className="flex items-center gap-3 rounded-md border border-red-500/20 bg-red-500/5 px-3 py-2.5">
        <AlertTriangle className="w-4 h-4 text-red-400/70 light:text-red-600/70 shrink-0" />
        <span className="text-xs text-red-300/80 light:text-red-600/80 flex-1">{health.message}</span>
        <button
          type="button"
          onClick={onRetry}
          disabled={isRetrying}
          className="inline-flex items-center gap-1.5 px-2.5 py-1 rounded text-[11px] font-medium transition-colors
                        border border-brand-main-600/40 bg-brand-main-900/80 hover:bg-brand-main-800 text-white/70 light:text-black/70 hover:text-white light:hover:text-brand-main-50
                        disabled:opacity-50 disabled:cursor-not-allowed shrink-0"
        >
          <RefreshCw
            className={`w-3 h-3 ${isRetrying ? 'animate-spin' : ''}`}
          />
          {isRetrying ? 'Checking' : 'Retry'}
        </button>
      </div>
    </div>
  )
}

// ── Main layout ─────────────────────────────────────────────────────

function AgentDetailLayout() {
  const { agentId } = Route.useParams()
  const navigate = Route.useNavigate()
  const { data: agent, isLoading, error } = useAgent(agentId)
  const matchRoute = useMatchRoute()
  const queryClient = useQueryClient()
  const { data: session } = useSession()
  const orgId = session?.user?.organizations?.[0]?.id ?? ''

  // Subscribe to backend-emitted lifecycle transitions so status badges
  // and recovery messages update live (no 30s polling delay) when the
  // reconciler attempts/finishes a recovery, sleeps the sandbox, etc.
  useAgentLifecycleStream(agentId, orgId, { enabled: !!agentId && !!orgId })

  const isPersistent = agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT
  const isProvisioning =
    isPersistent &&
    (agent?.lifecycleStatus === AgentLifecycleStatus.PROVISIONING ||
      agent?.lifecycleStatus === AgentLifecycleStatus.CREATED)

  const sandboxHealth = useSandboxHealth(agent)
  const [isRetrying, setIsRetrying] = useState(false)

  const handleRetry = useCallback(async () => {
    setIsRetrying(true)
    await queryClient.invalidateQueries({ queryKey: ['sandbox-health'] })
    await queryClient.invalidateQueries({ queryKey: ['agents'] })
    setTimeout(() => setIsRetrying(false), 1000)
  }, [queryClient])

  // Poll faster when reconnecting/waking/provisioning/sleeping (sleeping polls
  // so that after auto-wake the UI updates promptly)
  useEffect(() => {
    const shouldPoll =
      isProvisioning ||
      sandboxHealth.status === 'waking' ||
      sandboxHealth.status === 'reconnecting' ||
      sandboxHealth.status === 'sleeping'
    if (!shouldPoll) return
    const interval = setInterval(() => {
      queryClient.invalidateQueries({ queryKey: ['agents'] })
      queryClient.invalidateQueries({ queryKey: ['sandbox-health'] })
    }, 3000)
    return () => clearInterval(interval)
  }, [isProvisioning, sandboxHealth.status, queryClient, agentId])

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading agent..." />
      </div>
    )
  }

  if (error || !agent) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center">
        <div className="relative mb-6">
          <div className="absolute inset-0 bg-red-500/20 rounded-full blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <AlertTriangle className="size-8 text-red-400 light:text-red-600" />
          </div>
        </div>
        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
          {error?.message ?? 'Agent not found'}
        </h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
          The agent may have been deleted or you may not have access.
        </p>
        <Link
          to="/deployments/agents"
          className="text-sm text-brand-secondary-400 hover:text-brand-secondary-300"
        >
          Back to agents
        </Link>
      </div>
    )
  }

  const isOnChatTab = !!matchRoute({
    to: '/deployments/agents/$agentId/chat',
    params: { agentId },
  })
  const showHealthIndicator =
    isOnChatTab &&
    isPersistent &&
    sandboxHealth.status !== 'healthy' &&
    sandboxHealth.status !== 'loading'

  const tabs: {
    value:
      | 'chat'
      | 'overview'
      | 'settings'
      | 'memory'
      | 'automations'
      | 'api'
      | 'a2a'
    to:
      | '/deployments/agents/$agentId/chat'
      | '/deployments/agents/$agentId/overview'
      | '/deployments/agents/$agentId/settings'
      | '/deployments/agents/$agentId/memory'
      | '/deployments/agents/$agentId/automations'
      | '/deployments/agents/$agentId/api'
      | '/deployments/agents/$agentId/a2a'
    label: string
  }[] = [
    { value: 'chat', to: '/deployments/agents/$agentId/chat', label: 'Chat' },
    {
      value: 'overview',
      to: '/deployments/agents/$agentId/overview',
      label: 'Overview',
    },
    ...(isPersistent
      ? [
          {
            value: 'settings' as const,
            to: '/deployments/agents/$agentId/settings' as const,
            label: 'Settings',
          },
        ]
      : []),
    {
      value: 'memory',
      to: '/deployments/agents/$agentId/memory',
      label: 'Memory',
    },
    {
      value: 'automations',
      to: '/deployments/agents/$agentId/automations',
      label: 'Automations',
    },
    { value: 'api', to: '/deployments/agents/$agentId/api', label: 'API' },
    { value: 'a2a', to: '/deployments/agents/$agentId/a2a', label: 'A2A' },
  ]

  const currentTab: (typeof tabs)[number]['value'] =
    tabs.find((tab) => !!matchRoute({ to: tab.to, params: { agentId } }))
      ?.value ?? 'chat'

  return (
    <div className="flex flex-col h-full w-full">
      <div className="shrink-0 px-3 py-2">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <Tabs
              value={currentTab}
              onValueChange={(value) => {
                const selected = tabs.find((tab) => tab.value === value)
                if (!selected) return
                navigate({ to: selected.to, params: { agentId } })
              }}
            >
              <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                {tabs.map((tab) => (
                  <TabsTrigger
                    key={tab.to}
                    value={tab.value}
                    className={TAB_TRIGGER_CLASS}
                  >
                    {tab.label}
                  </TabsTrigger>
                ))}
              </TabsList>
            </Tabs>
          </div>
          {isOnChatTab && !isProvisioning && (
            <ChatStatusBar
              agentId={agentId}
              primarySessionId={agent.primarySessionId}
            />
          )}
        </div>
      </div>

      {showHealthIndicator && (
        <SandboxHealthIndicator
          health={sandboxHealth}
          onRetry={handleRetry}
          isRetrying={isRetrying}
        />
      )}

      <div className="flex-1 min-h-0 overflow-hidden">
        {isProvisioning && isOnChatTab ? (
          <div className="flex flex-col items-center justify-center h-full gap-4 text-center">
            <div className="flex items-center gap-3">
              <span className="h-5 w-5 rounded-full border-2 border-blue-400 border-t-transparent animate-spin" />
              <span className="text-white/80 light:text-black/80 text-sm font-medium">
                Provisioning agent...
              </span>
            </div>
            <p className="text-white/40 light:text-black/40 text-xs max-w-sm">
              Setting up the sandbox environment. This may take a moment. Chat
              will be available once provisioning completes.
            </p>
          </div>
        ) : (
          <Outlet />
        )}
      </div>
    </div>
  )
}

function ChatStatusBar({
  agentId,
  primarySessionId,
}: {
  agentId: string
  primarySessionId?: string
}) {
  const { sessionId, switchSession, resetSessionPointer, isStreaming } =
    useAgentChatSession(agentId, primarySessionId)

  const { data: session } = useSession_(sessionId ?? '')
  const completeMutation = useCompleteSession()

  const storeEntry = useAgentSessionStore((s) =>
    sessionId ? s.sessions[sessionId] : undefined,
  )
  const storeStreaming = storeEntry?.isStreaming ?? false

  // Side panel state
  const hasWorkflowPanel = useSidePanelStore((s) =>
    sessionId ? !!s.workflowPanels[sessionId] : false,
  )
  const workflowPanelVisible = useSidePanelStore((s) => s.workflowPanelVisible)
  const toggleWorkflowPanel = useSidePanelStore((s) => s.toggleWorkflowPanel)

  const effectiveStatus =
    storeStreaming || isStreaming ? SessionStatus.RUNNING : session?.status
  const statusStyle =
    effectiveStatus != null
      ? (STATUS_STYLES[effectiveStatus] ?? {
          label: 'Unknown',
          className: 'bg-gray-500/20 text-gray-400 light:text-gray-600',
        })
      : null

  const isActive = session ? session.status !== SessionStatus.CANCELLED : false
  const isDormant = session
    ? session.status === SessionStatus.COMPLETED ||
      session.status === SessionStatus.FAILED
    : false
  const showClose = isActive && !isStreaming && !storeStreaming && !isDormant

  return (
    <div className="flex items-center gap-2.5">
      {sessionId && statusStyle && (
        <span
          className={`px-2 py-0.5 rounded-full text-[10px] font-medium ${statusStyle.className}`}
        >
          {statusStyle.label}
        </span>
      )}
      {sessionId && (isStreaming || storeStreaming) && (
        <span className="flex items-center gap-1.5 text-[10px] text-blue-400 light:text-blue-600">
          <span className="inline-block w-1.5 h-1.5 rounded-full bg-blue-400 animate-pulse" />
          Streaming
        </span>
      )}
      {sessionId && showClose && (
        <button
          type="button"
          onClick={() => completeMutation.mutate(sessionId)}
          disabled={completeMutation.isPending}
          className="text-[10px] text-white/40 light:text-black/40 hover:text-white/60 light:hover:text-black/60 transition-colors px-2 py-0.5 rounded-md border border-brand-main-700/30 hover:border-brand-main-600/50"
        >
          {completeMutation.isPending ? 'Closing...' : 'Close'}
        </button>
      )}
      {sessionId && (
        <>
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(
                new Event(`session-timeline:open-activity:${sessionId}`),
              )
            }
            className="inline-flex items-center gap-1.5 rounded border border-brand-main-700/40 bg-brand-main-900/50 px-2 py-1 text-[10px] text-white/55 light:text-black/55 transition-colors hover:border-brand-secondary-500/30 hover:text-white/75 light:hover:text-black/75"
          >
            <Terminal className="w-3 h-3 text-brand-secondary-400/75" />
            Activity
          </button>
          <button
            type="button"
            onClick={() =>
              window.dispatchEvent(
                new Event(`session-timeline:open-sandbox:${sessionId}`),
              )
            }
            className="inline-flex items-center gap-1.5 rounded border border-brand-main-700/40 bg-brand-main-900/50 px-2 py-1 text-[10px] text-white/55 light:text-black/55 transition-colors hover:border-brand-secondary-500/30 hover:text-brand-secondary-200"
          >
            <Box className="w-3 h-3 text-brand-secondary-400/75" />
            Sandbox
          </button>
        </>
      )}
      {hasWorkflowPanel && (
        <button
          type="button"
          onClick={toggleWorkflowPanel}
          className={`flex items-center gap-1 text-[10px] px-2 py-1 rounded-md border transition-colors ${
            workflowPanelVisible
              ? 'text-violet-300 light:text-violet-600 border-violet-500/30 bg-violet-500/10'
              : 'text-white/30 light:text-black/30 border-brand-main-700/30 hover:text-white/50 light:hover:text-black/50 hover:border-brand-main-600/50'
          }`}
          title="Toggle Studio preview"
        >
          <Workflow className="w-3 h-3" />
        </button>
      )}
      <ChatSessionSwitcher
        agentId={agentId}
        currentSessionId={sessionId}
        onSwitch={switchSession}
        onNewSession={resetSessionPointer}
      />
    </div>
  )
}
