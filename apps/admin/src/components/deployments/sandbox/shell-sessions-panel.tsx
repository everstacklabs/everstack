import { useState, useMemo } from 'react'
import {
    useSandboxShellSessions,
    useKillShellSession,
    readCurrentShellSessionId,
    SANDBOX_GONE_ERROR,
    type ShellSessionRow,
} from '@/hooks/deployments/use-sandbox'

interface ShellSessionsPanelProps {
    sandboxId: string
}

// ShellSessionsPanel renders the persistent shell sessions for a
// sandbox. Persistent means the underlying tmux session inside the
// VM survives WebSocket disconnects, browser tab close, and gateway
// restarts — see the persistent-sandbox-sessions PR for the full
// design. Operators use this panel to spot leaked sessions and kill
// them; users use it to confirm which session their current tab is
// attached to (highlighted row).
export function ShellSessionsPanel({ sandboxId }: ShellSessionsPanelProps) {
    const { data: sessions, isLoading, isError, error, refetch } = useSandboxShellSessions(sandboxId)
    const killMutation = useKillShellSession(sandboxId)
    const isSandboxGone = (error as Error | null)?.message === SANDBOX_GONE_ERROR

    // Track which row is in the "click to confirm" state. One at a
    // time — clicking Kill on a second row reverts the first.
    const [pendingKillId, setPendingKillId] = useState<string | null>(null)

    // Read the current tab's attached shell session ID so we can
    // visually mark the row that "is you." Read once per render —
    // sessionStorage is synchronous and cheap.
    const currentShellSessionId = useMemo(
        () => readCurrentShellSessionId(sandboxId),
        [sandboxId],
    )

    if (isLoading) {
        return (
            <div className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
                <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-2">Active shell sessions</h3>
                <p className="text-xs text-white/40 light:text-black/40">Loading…</p>
            </div>
        )
    }

    // Terminal state: sandbox is gone on every fcagent. No amount of
    // retrying will surface a VM that doesn't exist. Show a clean
    // "no longer running" panel instead of an error wall.
    if (isSandboxGone) {
        return (
            <div className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
                <h3 className="text-sm font-medium text-white/80 light:text-black/80 mb-2">Active shell sessions</h3>
                <p className="text-xs text-white/50 light:text-black/50 leading-relaxed">
                    This sandbox is no longer running. Reprovision it from the
                    Instances tab to start a new shell.
                </p>
            </div>
        )
    }

    if (isError) {
        return (
            <div className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
                <div className="flex items-center justify-between mb-2">
                    <h3 className="text-sm font-medium text-white/80 light:text-black/80">Active shell sessions</h3>
                    <button
                        onClick={() => refetch()}
                        className="text-xs text-brand-secondary-400 hover:text-brand-secondary-300"
                    >
                        Retry
                    </button>
                </div>
                <p className="text-xs text-red-400 light:text-red-600">Couldn't load sessions.</p>
            </div>
        )
    }

    const rows = sessions ?? []

    return (
        <div className="rounded-lg border border-brand-main-700 bg-brand-main-900/50 p-4">
            <div className="flex items-center justify-between mb-3">
                <h3 className="text-sm font-medium text-white/80 light:text-black/80">Active shell sessions</h3>
                <span className="text-xs text-white/40 light:text-black/40">{rows.length} active</span>
            </div>

            {rows.length === 0 ? (
                <p className="text-xs text-white/40 light:text-black/40 leading-relaxed">
                    No active shell sessions. Sessions persist across browser
                    reloads and are automatically cleaned up after 24 hours of
                    inactivity.
                </p>
            ) : (
                <ul className="space-y-2">
                    {rows.map((row) => (
                        <ShellSessionRowItem
                            key={row.id}
                            row={row}
                            isCurrent={row.id === currentShellSessionId}
                            isPendingKill={pendingKillId === row.id}
                            disabled={killMutation.isPending}
                            onPendingKill={() => setPendingKillId(row.id)}
                            onConfirmKill={() => {
                                setPendingKillId(null)
                                killMutation.mutate(row.id)
                            }}
                            onCancelKill={() => setPendingKillId(null)}
                        />
                    ))}
                </ul>
            )}
        </div>
    )
}

interface RowProps {
    row: ShellSessionRow
    isCurrent: boolean
    isPendingKill: boolean
    disabled: boolean
    onPendingKill: () => void
    onConfirmKill: () => void
    onCancelKill: () => void
}

function ShellSessionRowItem({
    row,
    isCurrent,
    isPendingKill,
    disabled,
    onPendingKill,
    onConfirmKill,
    onCancelKill,
}: RowProps) {
    return (
        <li
            className={`flex items-center justify-between gap-3 rounded px-3 py-2 text-xs ${
                isCurrent
                    ?'bg-brand-secondary-500/10 border border-brand-secondary-500/30'
                    : 'bg-brand-main-800/50 border border-transparent'
            }`}
        >
            <div className="flex flex-col min-w-0">
                <div className="flex items-center gap-2">
                    <span className="font-mono text-white/80 light:text-black/80 truncate">
                        {row.id.slice(0, 8)}…{row.id.slice(-4)}
                    </span>
                    {isCurrent && (
                        <span className="text-[10px] uppercase tracking-wide text-brand-secondary-300">
                            this tab
                        </span>
                    )}
                    <SessionStatusBadge attachedClients={row.attached_clients} />
                </div>
                <span className="text-white/40 light:text-black/40 mt-0.5">
                    Created {formatRelativeTime(row.created_unix)} · Last
                    active {formatIdle(row.idle_seconds)}
                </span>
            </div>
            {isPendingKill ? (
                <div className="flex items-center gap-2 shrink-0">
                    <button
                        onClick={onConfirmKill}
                        disabled={disabled}
                        className="text-xs text-red-400 light:text-red-600 hover:text-red-300 light:hover:text-red-600 disabled:opacity-50"
                    >
                        Confirm
                    </button>
                    <button
                        onClick={onCancelKill}
                        disabled={disabled}
                        className="text-xs text-white/50 light:text-black/50 hover:text-white/70 light:hover:text-black/70 disabled:opacity-50"
                    >
                        Cancel
                    </button>
                </div>
            ) : (
                <button
                    onClick={onPendingKill}
                    disabled={disabled}
                    className="text-xs text-white/50 light:text-black/50 hover:text-red-400 light:hover:text-red-600 disabled:opacity-50 shrink-0"
                >
                    Kill
                </button>
            )}
        </li>
    )
}

function SessionStatusBadge({ attachedClients }: { attachedClients: number }) {
    if (attachedClients > 0) {
        return (
            <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-green-400 light:text-green-600">
                <span className="w-1.5 h-1.5 rounded-full bg-green-400" />
                Attached
            </span>
        )
    }
    return (
        <span className="inline-flex items-center gap-1 text-[10px] uppercase tracking-wide text-white/40 light:text-black/40">
            <span className="w-1.5 h-1.5 rounded-full bg-white/30 light:bg-black/30" />
            Idle
        </span>
    )
}

// formatRelativeTime renders an absolute Unix timestamp as a coarse
// "5m ago" / "2h ago" / "3d ago" string. Operators don't need
// second-level precision in the session list and the table stays
// readable at narrow widths. Returns "—" for zero/negative input,
// matching the "unknown" backend signal.
function formatRelativeTime(unix: number): string {
    if (!unix || unix < 0) return '—'
    const now = Math.floor(Date.now() / 1000)
    const ageSec = now - unix
    if (ageSec < 60) return `${Math.max(0, ageSec)}s ago`
    if (ageSec < 3600) return `${Math.floor(ageSec / 60)}m ago`
    if (ageSec < 86400) return `${Math.floor(ageSec / 3600)}h ago`
    return `${Math.floor(ageSec / 86400)}d ago`
}

// formatIdle renders the backend's idle_seconds (guest-clock
// computed) as a human string. -1 (unknown) shows as "unknown" so
// the user understands the gap rather than seeing a misleading "0s".
function formatIdle(idleSec: number): string {
    if (idleSec < 0) return 'unknown'
    if (idleSec < 60) return `${idleSec}s ago`
    if (idleSec < 3600) return `${Math.floor(idleSec / 60)}m ago`
    if (idleSec < 86400) return `${Math.floor(idleSec / 3600)}h ago`
    return `${Math.floor(idleSec / 86400)}d ago`
}
