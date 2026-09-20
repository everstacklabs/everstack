import { createFileRoute } from '@tanstack/react-router'
import { useEffect, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSessions } from '@/hooks/deployments/use-agents'
import {
  AgentList,
  AgentFormDialog,
  SessionsList,
  HitlReviewQueue,
  AgentWorkspace,
} from '@/components/deployments/agents'
import { Loader } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import type { AgentDefinition } from '@/server/agents'
import { z } from 'zod'
import {
  OpenAIStartSessionDialog,
  useOpenAIAgentsScope,
} from '@/components/deployments/agents/openai-start-session-dialog'
import { openAIRequest, type OpenAISession } from '@/server/openai-agents'
import { OpenAIPendingApprovals } from '@/components/deployments/agents/openai-pending-approvals'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

const CREATE_TABS = [
  'basics',
  'tools',
  'mcp',
  'behavior',
  'sandbox',
  'memory',
  'skills',
  'identity',
  'peers',
] as const

const agentsSearchSchema = z.object({
  tab: z
    .enum(['agents', 'runtime', 'sessions', 'approvals', 'openai'])
    .optional()
    .default('agents'),
  mode: z.enum(['all', 'ephemeral', 'persistent']).optional().default('all'),
  createTab: z.enum(CREATE_TABS).optional(),
})

export const Route = createFileRoute('/deployments/agents/')({
  component: AgentsPage,
  validateSearch: agentsSearchSchema,
})

function AgentsPage() {
  const { tab, mode } = Route.useSearch()
  const navigate = Route.useNavigate()
  const lifecycleMode =
    mode === 'persistent'
      ? ('persistent' as const)
      : mode === 'ephemeral'
        ? ('ephemeral' as const)
        : undefined
  const [editDialogOpen, setEditDialogOpen] = useState(false)
  const [editingAgent, setEditingAgent] = useState<AgentDefinition | null>(null)

  useEffect(() => {
    if (tab === 'openai')
      navigate({ replace: true, search: { tab: 'sessions', mode } })
  }, [mode, navigate, tab])

  const handleEdit = (agent: AgentDefinition) => {
    setEditingAgent(agent)
    setEditDialogOpen(true)
  }

  const handleEditDialogClose = (open: boolean) => {
    setEditDialogOpen(open)
    if (!open) {
      setEditingAgent(null)
    }
  }

  return (
    <div className="flex flex-col h-full w-full">
      <Tabs
        value={tab}
        onValueChange={(value) =>
          navigate({
            search: {
              tab: value as 'agents' | 'runtime' | 'sessions' | 'approvals',
              mode,
            },
          })
        }
        className="flex-1 flex flex-col overflow-hidden "
      >
        <div className="px-3 pt-2">
          <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
            <TabsTrigger
              className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
              value="agents"
            >
              Agents
            </TabsTrigger>
            <TabsTrigger
              className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
              value="runtime"
            >
              Runtime
            </TabsTrigger>
            <TabsTrigger
              className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
              value="sessions"
            >
              Sessions
            </TabsTrigger>
            <TabsTrigger
              className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
              value="approvals"
            >
              Approvals
            </TabsTrigger>
          </TabsList>
        </div>

        <div className="flex-1 overflow-hidden">
          <TabsContent
            value="agents"
            className="h-full overflow-hidden flex flex-col"
          >
            <AgentsTab onEdit={handleEdit} lifecycleMode={lifecycleMode} />
          </TabsContent>
          <TabsContent
            value="runtime"
            className="h-full overflow-hidden flex flex-col"
          >
            <AgentWorkspace />
          </TabsContent>
          <TabsContent
            value="sessions"
            className="h-full overflow-hidden flex flex-col"
          >
            <SessionsTab />
          </TabsContent>
          <TabsContent
            value="approvals"
            className="h-full overflow-hidden flex flex-col"
          >
            <OpenAIPendingApprovals />
            <div className="min-h-0 flex-1">
              <HitlReviewQueue />
            </div>
          </TabsContent>
        </div>
      </Tabs>

      <AgentFormDialog
        open={editDialogOpen}
        onOpenChange={handleEditDialogClose}
        agent={editingAgent}
      />
    </div>
  )
}

function AgentsTab({
  onEdit,
  lifecycleMode,
}: {
  onEdit: (agent: AgentDefinition) => void
  lifecycleMode?: 'ephemeral' | 'persistent'
}) {
  return <AgentList onEdit={onEdit} lifecycleMode={lifecycleMode} />
}

function SessionsTab() {
  const navigate = Route.useNavigate()
  const { authenticated, scope } = useOpenAIAgentsScope()
  const [runtimeOpen, setRuntimeOpen] = useState(false)
  const [openAIFormOpen, setOpenAIFormOpen] = useState(false)
  const { data: sessions = [], isLoading, error } = useSessions()
  const openAI = useQuery({
    queryKey: ['openai-agents', scope, 'sessions'],
    queryFn: () => openAIRequest<{ data: OpenAISession[] }>('/sessions'),
    enabled: authenticated,
    refetchInterval: 5000,
    retry: false,
  })

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading sessions..." />
      </div>
    )
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex shrink-0 items-center justify-between gap-3 border-b border-brand-main-800/40 bg-brand-main-900/20 px-3 py-2">
        <div>
          <div className="text-xs font-medium text-white/80 light:text-black/80">
            Agent sessions
          </div>
          <div className="text-[11px] text-white/45 light:text-black/45">
            Everstack and connected agent runtimes
          </div>
        </div>
        <ui.Button size="sm" onClick={() => setRuntimeOpen(true)}>
          Start session
        </ui.Button>
      </div>
      {error && (
        <div
          role="alert"
          className="border-b border-red-500/20 bg-red-500/10 px-3 py-2 text-xs text-red-400 light:text-red-700"
        >
          Everstack sessions could not load: {error.message}
        </div>
      )}
      {openAI.error && (
        <div
          role="alert"
          className="border-b border-amber-500/20 bg-amber-500/10 px-3 py-2 text-xs text-amber-200 light:text-amber-800"
        >
          OpenAI sessions are temporarily unavailable. Everstack sessions are
          still shown.
        </div>
      )}
      <SessionsList
        sessions={sessions}
        openAISessions={openAI.data?.data ?? []}
        mixedRuntimes
      />
      <ui.Dialog open={runtimeOpen} onOpenChange={setRuntimeOpen}>
        <ui.DialogContent className="border-brand-main-700 bg-brand-main-900 text-brand-main-100 sm:max-w-lg">
          <ui.DialogHeader>
            <ui.DialogTitle>Start a session</ui.DialogTitle>
            <ui.DialogDescription>
              Choose who manages the agent runtime. All resulting sessions
              appear together here.
            </ui.DialogDescription>
          </ui.DialogHeader>
          <div className="grid gap-3 sm:grid-cols-2">
            <button
              type="button"
              className="rounded-md border border-brand-main-700 bg-brand-main-950/40 p-4 text-left hover:border-brand-secondary-500/50 hover:bg-brand-main-800/60"
              onClick={() => {
                setRuntimeOpen(false)
                navigate({ search: { tab: 'agents', mode: 'all' } })
              }}
            >
              <span className="block text-sm font-medium">Everstack Agent</span>
              <span className="mt-1 block text-xs text-white/50 light:text-black/50">
                Choose a saved agent with Everstack tools, memory, skills, and
                lifecycle.
              </span>
            </button>
            <button
              type="button"
              className="rounded-md border border-brand-main-700 bg-brand-main-950/40 p-4 text-left hover:border-brand-secondary-500/50 hover:bg-brand-main-800/60"
              onClick={() => {
                setRuntimeOpen(false)
                setOpenAIFormOpen(true)
              }}
            >
              <span className="block text-sm font-medium">
                OpenAI Agents API
              </span>
              <span className="mt-1 block text-xs text-white/50 light:text-black/50">
                Start an OpenAI-managed session with optional Everstack compute
                and tools.
              </span>
            </button>
          </div>
        </ui.DialogContent>
      </ui.Dialog>
      <OpenAIStartSessionDialog
        open={openAIFormOpen}
        onOpenChange={setOpenAIFormOpen}
        onCreated={(session) =>
          navigate({
            to: '/deployments/agents/openai-sessions/$sessionId',
            params: { sessionId: session.id },
          })
        }
      />
    </div>
  )
}
