import { useMemo, useState } from 'react'
import { ArrowRight, Plus } from 'lucide-react'
import { useSessions } from '@/hooks/deployments/use-agents'
import { SessionStatus } from '@/server/agents'
import { Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'

const { Sheet, SheetContent, SheetHeader, SheetTitle, SheetBody } = ui

const STATUS_LABEL: Record<number, { label: string; className: string }> = {
    [SessionStatus.CREATED]: { label: 'Created', className: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
    [SessionStatus.RUNNING]: { label: 'Running', className: 'bg-green-500/20 text-green-300 light:text-green-600' },
    [SessionStatus.WAITING_FOR_INPUT]: { label: 'Waiting', className: 'bg-amber-500/20 text-amber-300 light:text-amber-700' },
    [SessionStatus.WAITING_FOR_APPROVAL]: { label: 'Approval', className: 'bg-amber-500/20 text-amber-300 light:text-amber-700' },
    [SessionStatus.COMPLETED]: { label: 'Done', className: 'bg-brand-secondary-400/20 text-brand-secondary-300' },
    [SessionStatus.FAILED]: { label: 'Failed', className: 'bg-red-500/20 text-red-400 light:text-red-600' },
    [SessionStatus.CANCELLED]: { label: 'Cancelled', className: 'bg-gray-500/20 text-gray-400 light:text-gray-600' },
}

function formatRelativeTime(ts: { seconds?: bigint; nanos?: number } | string | undefined): string {
    if (!ts) return ''
    const now = Date.now()
    const then = typeof ts === 'string'
        ? new Date(ts).getTime()
        : Number(ts.seconds ?? 0n) * 1000 + Math.floor((ts.nanos ?? 0) / 1_000_000)
    const diffMs = now - then
    const mins = Math.floor(diffMs / 60_000)
    if (mins < 1) return 'just now'
    if (mins < 60) return `${mins}m ago`
    const hours = Math.floor(mins / 60)
    if (hours < 24) return `${hours}h ago`
    const days = Math.floor(hours / 24)
    return `${days}d ago`
}

interface ChatSessionSwitcherProps {
    agentId: string
    currentSessionId: string | null
    onSwitch: (sessionId: string) => void
    onNewSession: () => void
}

export function ChatSessionSwitcher({ agentId, currentSessionId, onSwitch, onNewSession }: ChatSessionSwitcherProps) {
    const [open, setOpen] = useState(false)
    const { data: sessions = [] } = useSessions({ agentId })
    const sortedSessions = useMemo(() => {
        return [...sessions].sort((a, b) => {
            const aTime = a.updatedAt?.seconds ? Number(a.updatedAt.seconds) : 0
            const bTime = b.updatedAt?.seconds ? Number(b.updatedAt.seconds) : 0
            return bTime - aTime
        })
    }, [sessions])

    return (
        <Sheet open={open} onOpenChange={setOpen}>
            <Button
                type="button"
                size="xs"
                variant="ghost"
                className="text-xs text-brand-main-300 hover:text-white light:hover:text-brand-main-50 gap-1"
                onClick={() => setOpen(true)}
            >
                Sessions
                <ArrowRight className="w-3 h-3" />
            </Button>
            <SheetContent
                side="right"
                className="w-full sm:max-w-[420px] max-h-[100vh] overflow-hidden border-brand-main-700 bg-brand-main-900"
            >
                <SheetHeader>
                    <SheetTitle className="text-sm text-white light:text-brand-main-50">Sessions</SheetTitle>
                </SheetHeader>
                <SheetBody className="py-4 flex h-full min-h-0 flex-col gap-3">
                    <button
                        type="button"
                        onClick={() => { onNewSession(); setOpen(false) }}
                        className="flex items-center gap-2 w-full rounded-md border border-brand-main-700 bg-brand-main-800/60 px-3 py-2 text-xs text-brand-secondary-300 hover:bg-brand-main-800 transition-colors"
                    >
                        <Plus className="w-3.5 h-3.5" />
                        New Session
                    </button>

                    <div className="text-[11px] text-brand-main-400 px-1">
                        {sortedSessions.length} session{sortedSessions.length === 1 ? '' : 's'}
                    </div>

                    <div className="min-h-0 flex-1 overflow-y-auto pr-1 space-y-1">
                        {sortedSessions.length === 0 && (
                            <div className="px-3 py-4 text-xs text-brand-main-400 text-center">No sessions</div>
                        )}
                        {sortedSessions.map((s) => {
                            const isCurrent = s.id === currentSessionId
                            const status = STATUS_LABEL[s.status] ?? STATUS_LABEL[SessionStatus.CREATED]
                            const label = s.summary || s.id.slice(0, 12)
                            return (
                                <button
                                    type="button"
                                    key={s.id}
                                    onClick={() => { onSwitch(s.id); setOpen(false) }}
                                    className={`flex w-full flex-col gap-1 rounded-md border px-3 py-2 text-left text-xs transition-colors ${
                                        isCurrent
                                            ? 'border-brand-secondary-500/40 bg-brand-secondary-600/10 text-white light:text-brand-main-50'
                                            : 'border-brand-main-700/60 text-brand-main-200 hover:bg-brand-main-800/70'
                                    } light:text-brand-main-50`}
                                >
                                    <span className="truncate">{label}</span>
                                    <div className="flex items-center gap-2 text-brand-main-400">
                                        <span className={`shrink-0 px-1 py-0.5 rounded text-[9px] font-medium ${status.className}`}>
                                            {status.label}
                                        </span>
                                        {(s.turnCount ?? 0) > 0 && (
                                            <span>{s.turnCount} turn{s.turnCount === 1 ? '' : 's'}</span>
                                        )}
                                        {s.updatedAt && (
                                            <span>{formatRelativeTime(s.updatedAt)}</span>
                                        )}
                                    </div>
                                </button>
                            )
                        })}
                    </div>
                </SheetBody>
            </SheetContent>
        </Sheet>
    )
}
