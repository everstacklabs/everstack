import { useCallback } from 'react'
import {
    useSandboxShellSessions,
    useKillShellSession,
    SANDBOX_GONE_ERROR,
    type ShellSessionRow,
} from '@/hooks/deployments/use-sandbox'
import { Iconify } from '@everstack/ui/icons'

// Tab strip across the top of the Shell view, one tab per live tmux
// session in the sandbox. Clicking a tab switches the terminal to
// that session; "+" mints a fresh one; "x" on each tab kills the
// session. Persists the active tab via the caller's onSelect callback.
//
// Today the Shell tab can only attach to one tmux session at a time
// (the one persisted in sessionStorage). The user could "see" other
// sessions in the sidebar but had no way to switch to them without
// reaching for the kill button. This strip makes session switching
// a single click and surfaces the multi-session feature that the
// backend already supports.

interface ShellTabStripProps {
    sandboxId: string
    // The currently-mounted shell session ID. Empty string means
    // "the auto-minted default" — the hook's storage-key fallback.
    activeShellSessionId: string
    // Switch the active tab. Caller is responsible for persisting
    // (typically: write to sessionStorage[shell-session:<sbxId>]
    // and bump a React state key that drives the terminal remount).
    onSelect: (shellSessionId: string) => void
    // Mint-a-new-shell action. Caller clears the session storage
    // and remounts so the hook re-mints and reconnects fresh.
    onCreate: () => void
}

export function ShellTabStrip({
    sandboxId,
    activeShellSessionId,
    onSelect,
    onCreate,
}: ShellTabStripProps) {
    const { data: sessions = [], isLoading, error } = useSandboxShellSessions(sandboxId)
    const killMutation = useKillShellSession(sandboxId)
    const isSandboxGone = (error as Error | null)?.message === SANDBOX_GONE_ERROR

    const handleKill = useCallback(
        (e: React.MouseEvent, shellSessionId: string) => {
            // Stop the click from also firing the tab's onSelect.
            e.stopPropagation()
            killMutation.mutate(shellSessionId)
            // If we killed the active tab, the caller's onSelect
            // will get a stale id until the next ListSessions
            // refresh. The terminal will see a session_gone event
            // from the gateway and the hook will clear storage —
            // safe to leave as-is.
        },
        [killMutation],
    )

    // Terminal state: the gateway confirmed the sandbox VM is gone on
    // every fcagent (410). Polling has already stopped; minting a new
    // shell cannot work either, so disable the "+" button too.
    if (isSandboxGone) {
        return (
            <div className="flex items-center gap-1 px-3 py-1 bg-brand-main-900/60 border-b border-brand-main-700">
                <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30 px-2">
                    Sandbox no longer running
                </span>
                <div className="flex-1" />
                <NewShellButton onClick={onCreate} disabled />
            </div>
        )
    }

    // While the session list is loading, render a stable placeholder
    // strip with just the "+" button so the layout doesn't pop in
    // when data arrives.
    if (isLoading) {
        return (
            <div className="flex items-center gap-1 px-3 py-1 bg-brand-main-900/60 border-b border-brand-main-700">
                <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30 px-2">
                    Loading sessions…
                </span>
                <div className="flex-1" />
                <NewShellButton onClick={onCreate} disabled />
            </div>
        )
    }

    return (
        <div className="flex items-center gap-1 px-2 py-1 bg-brand-main-900/60 border-b border-brand-main-700 overflow-x-auto">
            {sessions.length === 0 ? (
                <span className="text-[10px] uppercase tracking-wider text-white/30 light:text-black/30 px-2">
                    No sessions
                </span>
            ) : (
                sessions.map((row, idx) => (
                    <TabButton
                        key={row.id}
                        index={idx}
                        row={row}
                        isActive={row.id === activeShellSessionId}
                        onSelect={() => onSelect(row.id)}
                        onKill={(e) => handleKill(e, row.id)}
                        disabled={killMutation.isPending}
                    />
                ))
            )}
            <div className="flex-1" />
            <NewShellButton onClick={onCreate} />
        </div>
    )
}

interface TabButtonProps {
    index: number
    row: ShellSessionRow
    isActive: boolean
    onSelect: () => void
    onKill: (e: React.MouseEvent) => void
    disabled: boolean
}

function TabButton({ index, row, isActive, onSelect, onKill, disabled }: TabButtonProps) {
    // Numbered label (Shell 1 / Shell 2 / …) keeps the strip
    // readable. Session-id is in the title for support / debug.
    const label = `Shell ${index + 1}`
    const shortId = row.id.length > 8 ? row.id.slice(0, 6) + '…' : row.id

    return (
        <div
            className={`flex items-center gap-1.5 pl-2.5 pr-1 py-1 text-xs rounded border ${
                isActive
                    ? 'bg-brand-secondary-500/15 border-brand-secondary-500/40 text-white light:text-brand-main-50'
                    : 'bg-brand-main-800/50 border-transparent text-white/60 light:text-black/60 hover:text-white/80 light:hover:text-black/80 hover:bg-brand-main-700/50'
            } shrink-0 cursor-pointer transition-colors light:text-brand-main-50`}
            onClick={onSelect}
            title={`${label} · ${row.id}`}
            role="tab"
            aria-selected={isActive}
        >
            <span className="font-medium">{label}</span>
            <span className="text-[10px] text-white/40 light:text-black/40 font-mono">{shortId}</span>
            {row.attached_clients > 0 && (
                <span
                    className="w-1.5 h-1.5 rounded-full bg-green-400"
                    title={`${row.attached_clients} attached`}
                />
            )}
            {/* Kill button. Always render but smaller / less prominent
                so the user doesn't fat-finger it. */}
            <button
                onClick={onKill}
                disabled={disabled}
                className="p-0.5 rounded text-white/30 light:text-black/30 hover:text-red-400 light:hover:text-red-600 hover:bg-red-500/10 disabled:opacity-40 disabled:cursor-not-allowed"
                title="Kill this session"
                aria-label="Kill session"
            >
                <Iconify.Icon icon="heroicons:x-mark" className="size-3" />
            </button>
        </div>
    )
}

function NewShellButton({ onClick, disabled = false }: { onClick: () => void; disabled?: boolean }) {
    return (
        <button
            onClick={onClick}
            disabled={disabled}
            className="flex items-center gap-1 px-2 py-1 text-xs text-brand-secondary-300 hover:text-brand-secondary-200 hover:bg-brand-secondary-500/10 rounded shrink-0 disabled:opacity-40 disabled:cursor-not-allowed"
            title="Open a new shell session"
            aria-label="Open a new shell session"
        >
            <Iconify.Icon icon="heroicons:plus" className="size-3" />
            New shell
        </button>
    )
}
