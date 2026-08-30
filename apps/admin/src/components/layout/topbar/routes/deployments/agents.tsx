import { useState } from 'react'
import { Link, useLocation } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { Download } from '@everstack/ui/icons'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { useAgent } from '@/hooks/deployments/use-agents'
import { AgentFormDialog } from '@/components/deployments/agents'
import { AgentLifecycleMode } from '@/server/agents'
import { LifecycleStatusBadge } from '@/components/deployments/agents/lifecycle-status-badge'
import { requestAgentSessionJsonDownload } from '@/components/deployments/agents/session-json-export-events'

function AgentBreadcrumb() {
  const { pathname } = useLocation()
  const segments = pathname.split('/').filter(Boolean)
  // pathname: /deployments/agents/{agentId}/chat (or /overview, etc.)
  const agentId = segments.length > 2 ? segments[2] : ''

  const { data: agent, isLoading } = useAgent(agentId)

  const isPersistent = agent?.lifecycleMode === AgentLifecycleMode.PERSISTENT

  return (
    <div className="flex items-center gap-1.5">
      <Link
        to="/deployments/agents"
        className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors"
      >
        Agents
      </Link>
      {agentId && (
        <>
          <span className="text-brand-main-400 text-sm">/</span>
          {isLoading ? (
            <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
          ) : (
            <Link
              to="/deployments/agents/$agentId/chat"
              params={{ agentId }}
              className="text-sm text-white light:text-brand-main-50 font-normal hover:text-white/80 light:hover:text-black/80 transition-colors"
            >
              {agent?.name ?? agentId}
            </Link>
          )}
          {/* Status badges */}
          {agent && (
            <span
              className={`ml-2 px-1.5 py-0.5 rounded text-[10px] font-medium ${agent.enabled ? 'bg-green-500/20 text-green-300 light:text-green-600' : 'bg-gray-500/20 text-gray-400 light:text-gray-600'}`}
            >
              {agent.enabled ? 'Enabled' : 'Disabled'}
            </span>
          )}
          {isPersistent && (
            <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-blue-500/15 text-blue-300 light:text-blue-600">
              Persistent
            </span>
          )}
          {isPersistent && agent?.lifecycleStatus != null && (
            <LifecycleStatusBadge status={agent.lifecycleStatus} />
          )}
        </>
      )}
    </div>
  )
}

function SessionBreadcrumb() {
  const { pathname } = useLocation()
  const segments = pathname.split('/').filter(Boolean)
  // pathname: /deployments/agents/sessions/{sessionId}
  const sessionId = segments.length > 3 ? segments[3] : ''

  return (
    <div className="flex items-center gap-1.5">
      <Link
        to="/deployments/agents"
        className="text-sm text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors font-normal"
      >
        Agents
      </Link>
      <span className="text-brand-main-400 text-sm">/</span>
      <span className="text-sm text-brand-main-300 font-normal">Sessions</span>
      {sessionId && (
        <>
          <span className="text-brand-main-400 text-sm">/</span>
          <span className="text-sm text-white light:text-brand-main-50 font-normal font-mono">
            {sessionId}
          </span>
        </>
      )}
    </div>
  )
}

export const DeploymentsAgentsActions: ActionGroup[] = [
  {
    title: 'Agents',
    actions: [
      {
        type: 'button',
        key: 'create-agent',
        requiredPermission: 'resource:create',
        label: 'Create Agent',
        variant: 'default',
        onClick: (setDialogOpen: (open: boolean) => void) => () =>
          setDialogOpen(true),
      },
    ],
  },
]

function EditAgentButton() {
  const { pathname } = useLocation()
  const agentId = pathname.split('/').filter(Boolean)[2] ?? ''
  const { data: agent } = useAgent(agentId)
  const [editOpen, setEditOpen] = useState(false)

  return (
    <>
      <Button
        variant="outline"
        onClick={() => setEditOpen(true)}
        disabled={!agent}
      >
        Edit
      </Button>
      {agent && (
        <AgentFormDialog
          open={editOpen}
          onOpenChange={setEditOpen}
          agent={agent}
        />
      )}
    </>
  )
}

function DownloadSessionJsonButton() {
  const { pathname } = useLocation()
  const sessionDetailMatch = pathname.match(
    /^\/deployments\/agents\/sessions\/([^/]+)$/,
  )
  const isAgentChat = /^\/deployments\/agents\/[^/]+\/chat$/.test(pathname)

  if (!sessionDetailMatch && !isAgentChat) return null

  return (
    <Button
      variant="outline"
      title="Download session JSON"
      onClick={() => requestAgentSessionJsonDownload(sessionDetailMatch?.[1])}
      className="gap-2 border-brand-main-600 text-white hover:bg-brand-main-800 hover:text-white light:text-brand-main-50 light:hover:text-brand-main-50"
    >
      <Download size={16} />
      Download JSON
    </Button>
  )
}

export const DeploymentsAgentsDetailActions: ActionGroup[] = [
  {
    title: <AgentBreadcrumb />,
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'edit-agent',
        label: 'Edit',
        component: EditAgentButton,
      },
      {
        type: 'custom',
        key: 'download-session-json',
        label: 'Download JSON',
        component: DownloadSessionJsonButton,
      },
    ],
  },
]

export const DeploymentsAgentsSessionDetailActions: ActionGroup[] = [
  {
    title: <SessionBreadcrumb />,
  },
  {
    actions: [
      {
        type: 'custom',
        key: 'download-session-json',
        label: 'Download JSON',
        component: DownloadSessionJsonButton,
      },
    ],
  },
]
