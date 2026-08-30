import { cn } from '@/lib/utils'
import { Iconify } from '@everstack/ui/icons'
import { PlatformSourceBadge } from '@/components/deployments/channels/channel-status-badge'

type SessionTileProps = {
  session: {
    id: string
    agentId: string
    agentName?: string
    status: string
    source?: string
    platformUserName?: string
    lastMessage?: string
    turnCount: number
    totalTokens: number
    updatedAt: string
  }
  onClick?: () => void
  isSelected?: boolean
}

const STATUS_COLORS: Record<string, string> = {
  running: 'border-brand-secondary-500/35 bg-brand-secondary-600/10',
  created: 'border-brand-main-600 bg-brand-main-900/50',
  completed: 'border-emerald-500/25 bg-emerald-500/5',
  failed: 'border-red-500/30 bg-red-500/5',
  cancelled: 'border-brand-main-700 bg-brand-main-900/40',
  hibernated: 'border-brand-main-600 bg-brand-main-900/50',
  waiting_for_input: 'border-amber-500/30 bg-amber-500/5',
  waiting_for_approval: 'border-orange-500/30 bg-orange-500/5',
}

const STATUS_DOT: Record<string, string> = {
  running: 'bg-brand-secondary-300 animate-pulse',
  created: 'bg-brand-main-300',
  completed: 'bg-emerald-500',
  failed: 'bg-red-500',
  cancelled: 'bg-brand-main-400',
  hibernated: 'bg-violet-400',
  waiting_for_input: 'bg-amber-500 animate-pulse',
  waiting_for_approval: 'bg-orange-400 animate-pulse',
}

const STATUS_LABELS: Record<string, string> = {
  running: 'Running',
  created: 'Created',
  completed: 'Completed',
  failed: 'Failed',
  cancelled: 'Cancelled',
  hibernated: 'Hibernated',
  waiting_for_input: 'Waiting for input',
  waiting_for_approval: 'Waiting for approval',
}

export function SessionTile({
  session,
  onClick,
  isSelected,
}: SessionTileProps) {
  const statusColor = STATUS_COLORS[session.status] ?? STATUS_COLORS.created
  const dotColor = STATUS_DOT[session.status] ?? STATUS_DOT.created

  const timeAgo = formatTimeAgo(session.updatedAt)

  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        'w-full rounded border p-3 text-left transition-colors hover:border-brand-secondary-500/45 hover:bg-brand-main-800/70',
        statusColor,
        isSelected && 'ring-1 ring-brand-secondary-400',
      )}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className={cn('h-2 w-2 shrink-0 rounded-full', dotColor)} />
            <span className="truncate text-sm font-medium text-white light:text-brand-main-50">
              {session.agentName ?? 'Agent'}
            </span>
          </div>

          {session.platformUserName && (
            <div className="mt-1 flex items-center gap-1.5 text-xs text-white/45 light:text-black/45">
              <Iconify.Icon icon="lucide:user" className="h-3 w-3" />
              {session.platformUserName}
            </div>
          )}

          {session.lastMessage && (
            <p className="mt-2 line-clamp-2 min-h-8 text-xs leading-relaxed text-white/55 light:text-black/55">
              {session.lastMessage}
            </p>
          )}
        </div>

        {session.source && session.source !== 'admin_ui' && (
          <PlatformSourceBadge source={session.source} />
        )}
      </div>

      <div className="mt-3 flex items-center justify-between border-t border-brand-main-700/60 pt-2 text-[11px] text-white/40 light:text-black/40">
        <span className="inline-flex items-center gap-1.5">
          <span>{STATUS_LABELS[session.status] ?? 'Unknown'}</span>
          <span>·</span>
          <span>{session.turnCount} turns</span>
          {session.totalTokens > 0 && (
            <>
              <span>·</span>
              <span>{session.totalTokens.toLocaleString()} tokens</span>
            </>
          )}
        </span>
        <span className="shrink-0">{timeAgo}</span>
      </div>
    </button>
  )
}

function formatTimeAgo(dateStr: string): string {
  const date = new Date(dateStr)
  const now = new Date()
  const diffMs = now.getTime() - date.getTime()
  const diffMin = Math.floor(diffMs / 60000)

  if (diffMin < 1) return 'just now'
  if (diffMin < 60) return `${diffMin}m ago`
  const diffHr = Math.floor(diffMin / 60)
  if (diffHr < 24) return `${diffHr}h ago`
  const diffDay = Math.floor(diffHr / 24)
  return `${diffDay}d ago`
}
