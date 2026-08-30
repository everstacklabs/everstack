import { createFileRoute } from '@tanstack/react-router'
import { useState } from 'react'
import { useSessions } from '@/hooks/deployments/use-agents'
import {
  AgentList,
  AgentFormDialog,
  SessionsList,
  HitlReviewQueue,
  AgentWorkspace,
} from '@/components/deployments/agents'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { ui } from '@everstack/ui'
import type { AgentDefinition } from '@/server/agents'
import { z } from 'zod'

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
    .enum(['agents', 'runtime', 'sessions', 'approvals'])
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
            <HitlReviewQueue />
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
  const { data: sessions = [], isLoading, error } = useSessions()

  if (isLoading) {
    return (
      <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
        <Loader loaderText="Loading sessions..." />
      </div>
    )
  }

  if (error) {
    return (
      <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
        Error loading sessions: {error.message}
      </div>
    )
  }

  if (sessions.length === 0) {
    return (
      <div className="flex-1 flex flex-col items-center justify-center">
        <div className="relative mb-6">
          <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
          <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
            <Iconify.Icon
              icon="heroicons:chat-bubble-left-right"
              className="size-8 text-brand-secondary-400"
            />
          </div>
        </div>
        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
          No sessions yet
        </h3>
        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
          Sessions are created when agents are run via the API or chat.
        </p>
      </div>
    )
  }

  return <SessionsList sessions={sessions} />
}
