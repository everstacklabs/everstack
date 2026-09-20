import { useMemo, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useAgents, useCancelSession } from '@/hooks/deployments/use-agents'
import type { AgentSession } from '@/server/agents'
import { SessionStatus } from '@/server/agents'
import type { OpenAISession } from '@/server/openai-agents'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { PlatformSourceBadge } from '@/components/deployments/channels/channel-status-badge'

export type SessionRow =
  | { runtime: 'everstack'; id: string; native: AgentSession; created: number }
  | { runtime: 'openai'; id: string; openai: OpenAISession; created: number }

const nativeStatus: Record<number, { label: string; className: string }> = {
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
    className: 'bg-brand-secondary-500/15 text-brand-secondary-200',
  },
  [SessionStatus.WAITING_FOR_APPROVAL]: {
    label: 'Approval',
    className: 'bg-brand-secondary-700/25 text-brand-secondary-300',
  },
  [SessionStatus.COMPLETED]: {
    label: 'Completed',
    className: 'bg-brand-secondary-400 text-brand-secondary-800',
  },
  [SessionStatus.FAILED]: {
    label: 'Failed',
    className: 'bg-red-500/15 text-red-400 light:text-red-600',
  },
  [SessionStatus.CANCELLED]: {
    label: 'Cancelled',
    className: 'bg-brand-main-700/30 text-brand-main-300',
  },
}
const openAIStatus = (status: string) => ({
  label: status.replaceAll('_', ' '),
  className: /fail|error/.test(status)
    ? 'bg-red-500/15 text-red-400 light:text-red-600'
    : /complete/.test(status)
      ? 'bg-brand-secondary-400 text-brand-secondary-800'
      : /cancel|delete/.test(status)
        ? 'bg-brand-main-700/30 text-brand-main-300'
        : 'bg-brand-secondary-600/20 text-brand-secondary-300',
})

export function SessionsList({
  sessions,
  openAISessions = [],
  mixedRuntimes = false,
}: {
  sessions: AgentSession[]
  openAISessions?: OpenAISession[]
  mixedRuntimes?: boolean
}) {
  const cancelMutation = useCancelSession()
  const [runtime, setRuntime] = useState<'all' | SessionRow['runtime']>('all')
  const { data: agents = [] } = useAgents()
  const navigate = useNavigate()
  const names = useMemo(
    () => new Map(agents.map((agent) => [agent.id, agent.name])),
    [agents],
  )
  const rows = useMemo(
    () => buildSessionRows(sessions, openAISessions),
    [openAISessions, sessions],
  )
  const visibleRows = filterSessionRows(rows, runtime)
  const cancel = async (event: React.MouseEvent, id: string) => {
    event.stopPropagation()
    try {
      await cancelMutation.mutateAsync(id)
      toast.success('Session cancelled')
    } catch {
      toast.error('Failed to cancel session')
    }
  }
  const columns: ColumnConfig<SessionRow>[] = [
    {
      id: 'id',
      header: 'Session ID',
      width: 160,
      minWidth: 140,
      render: (row) => (
        <span className="font-mono text-xs text-brand-secondary-400">
          {row.id.slice(0, 12)}…
        </span>
      ),
    },
    {
      id: 'runtime',
      header: 'Runtime',
      width: 105,
      minWidth: 90,
      render: (row) => (
        <span
          className={`rounded px-1.5 py-0.5 text-[10px] font-medium ${row.runtime === 'openai' ? 'bg-brand-secondary-600/20 text-brand-secondary-300 light:text-brand-secondary-700' : 'bg-brand-main-700/50 text-brand-main-200'}`}
        >
          {row.runtime === 'openai' ? 'OpenAI' : 'Everstack'}
        </span>
      ),
    },
    {
      id: 'agent',
      header: 'Agent / model',
      width: 190,
      minWidth: 130,
      render: (row) => (
        <span className="text-sm text-white/90 light:text-black/90">
          {row.runtime === 'openai'
            ? row.openai.model
            : (names.get(row.native.agentId) ??
              `${row.native.agentId.slice(0, 12)}…`)}
        </span>
      ),
    },
    {
      id: 'status',
      header: 'Status',
      width: 110,
      minWidth: 90,
      render: (row) => {
        const style =
          row.runtime === 'openai'
            ? openAIStatus(row.openai.status)
            : (nativeStatus[row.native.status] ?? {
                label: 'Unknown',
                className: 'bg-brand-main-700/30 text-brand-main-300',
              })
        return (
          <span
            className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-medium capitalize ${style.className}`}
          >
            {style.label}
          </span>
        )
      },
    },
    {
      id: 'source',
      header: 'Source',
      width: 105,
      minWidth: 85,
      render: (row) =>
        row.runtime === 'openai' ? (
          <span className="text-xs text-white/55">Agents API</span>
        ) : (
          <PlatformSourceBadge
            source={
              (row.native as AgentSession & { source?: string }).source ??
              'admin_ui'
            }
          />
        ),
    },
    {
      id: 'activity',
      header: 'Activity',
      width: 120,
      minWidth: 100,
      render: (row) => (
        <span className="text-xs text-white/55 light:text-black/55">
          {row.runtime === 'openai'
            ? row.openai.environment.type === 'self_hosted'
              ? 'Sandbox'
              : 'No sandbox'
            : `${row.native.turnCount} ${row.native.turnCount === 1 ? 'turn' : 'turns'}`}
        </span>
      ),
    },
    {
      id: 'tokens',
      header: 'Tokens',
      width: 90,
      minWidth: 75,
      render: (row) => (
        <span className="text-sm tabular-nums text-white/60 light:text-black/60">
          {row.runtime === 'everstack'
            ? row.native.totalTokens.toLocaleString()
            : 'Unavailable'}
        </span>
      ),
    },
    {
      id: 'created',
      header: 'Created',
      width: 170,
      minWidth: 140,
      render: (row) => (
        <span className="text-sm text-white/50 light:text-black/50">
          {row.runtime === 'openai'
            ? row.openai.created_at
              ? formatTimestamp(row.openai.created_at)
              : '—'
            : formatTimestamp(row.native.createdAt)}
        </span>
      ),
    },
    {
      id: 'actions',
      header: '',
      width: 80,
      minWidth: 80,
      maxWidth: 80,
      resizable: false,
      render: (row) => {
        if (
          row.runtime === 'openai' ||
          ![
            SessionStatus.RUNNING,
            SessionStatus.WAITING_FOR_INPUT,
            SessionStatus.WAITING_FOR_APPROVAL,
          ].includes(row.native.status)
        )
          return null
        return (
          <div data-row-actions>
            <button
              type="button"
              className="rounded px-2 py-1 text-xs text-red-400 hover:bg-red-500/10"
              onClick={(event) => cancel(event, row.id)}
              disabled={cancelMutation.isPending}
            >
              Cancel
            </button>
          </div>
        )
      },
    },
  ]
  return (
    <div className="flex h-full min-h-0 w-full flex-1 flex-col overflow-hidden">
      {mixedRuntimes && (
        <div className="flex shrink-0 items-center gap-1 border-b border-brand-main-800/40 px-3 py-2">
          {(['all', 'everstack', 'openai'] as const).map((value) => (
            <button
              key={value}
              type="button"
              onClick={() => setRuntime(value)}
              className={`rounded px-2 py-1 text-xs capitalize ${runtime === value ? 'bg-brand-secondary-600/20 text-brand-secondary-300 light:text-brand-secondary-700' : 'text-white/50 hover:bg-brand-main-800/60 hover:text-white light:text-black/50 light:hover:text-black'}`}
            >
              {value === 'all'
                ? 'All runtimes'
                : value === 'openai'
                  ? 'OpenAI'
                  : 'Everstack'}
            </button>
          ))}
        </div>
      )}
      <ResponsiveTable
        columns={columns}
        data={visibleRows}
        enableResizing
        minTableWidth="100%"
        emptyMessage={
          <div className="flex flex-col items-center justify-center py-12">
            <div className="relative mb-6">
              <div className="absolute inset-0 rounded-full bg-brand-secondary-500/20 blur-xl" />
              <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                <Iconify.Icon
                  icon="heroicons:chat-bubble-left-right"
                  className="size-8 text-brand-secondary-400"
                />
              </div>
            </div>
            <h3 className="mb-2 text-base font-medium text-white light:text-brand-main-50">
              No sessions found
            </h3>
            <p className="max-w-sm text-center text-sm text-white/50 light:text-black/50">
              Sessions from Everstack and connected runtimes will appear here.
            </p>
          </div>
        }
        onRowClick={(row) => navigate(sessionDestination(row))}
        rowKey={(row) => `${row.runtime}:${row.id}`}
      />
    </div>
  )
}

export function buildSessionRows(
  sessions: AgentSession[],
  openAISessions: OpenAISession[],
): SessionRow[] {
  return [
    ...sessions.map(
      (native): SessionRow => ({
        runtime: 'everstack',
        id: native.id,
        native,
        created: native.createdAt?.seconds
          ? Number(native.createdAt.seconds) * 1000
          : 0,
      }),
    ),
    ...openAISessions.map(
      (openai): SessionRow => ({
        runtime: 'openai',
        id: openai.id,
        openai,
        created: openai.created_at ? Date.parse(openai.created_at) : 0,
      }),
    ),
  ].sort((a, b) => b.created - a.created)
}

export function sessionDestination(row: SessionRow) {
  return row.runtime === 'openai'
    ? {
        to: '/deployments/agents/openai-sessions/$sessionId' as const,
        params: { sessionId: row.id },
      }
    : {
        to: '/deployments/agents/sessions/$sessionId' as const,
        params: { sessionId: row.id },
      }
}

export function filterSessionRows(
  rows: SessionRow[],
  runtime: 'all' | SessionRow['runtime'],
) {
  return runtime === 'all'
    ? rows
    : rows.filter((row) => row.runtime === runtime)
}
