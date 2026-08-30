import { lazy, Suspense, useCallback, useState } from 'react'
import { useSandboxContext, SandboxSessionPicker } from './sandbox-context'
import { Loader } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { isSandboxRunning } from './lifecycle'
import { ShellSessionsPanel } from './shell-sessions-panel'
import { ShellIdleWarning } from './shell-idle-warning'
import { ShellTabStrip } from './shell-tab-strip'
import { useNotifyShellSessionCreated } from '@/hooks/deployments/use-sandbox'

// sessionStorage key used by useSandboxShell for the active tmux
// session id. We piggyback on it so a tab switch persists across
// page reload and the hook keeps using the same id on its next
// connect. Kept in lockstep with the key in use-sandbox.ts.
const shellSessionStorageKey = (sandboxId: string) =>
    `everstack:shell-session:${sandboxId}`

function readActiveShellSession(sandboxId: string): string {
    try {
        return sessionStorage.getItem(shellSessionStorageKey(sandboxId)) ?? ''
    } catch {
        return ''
    }
}

function writeActiveShellSession(sandboxId: string, shellSessionId: string) {
    try {
        if (shellSessionId) {
            sessionStorage.setItem(shellSessionStorageKey(sandboxId), shellSessionId)
        } else {
            sessionStorage.removeItem(shellSessionStorageKey(sandboxId))
        }
    } catch {
        // sessionStorage can throw in private modes — non-fatal.
    }
}

// Lazy-load xterm to save ~200KB from the main bundle
const XTerminal = lazy(() => import('./shell-terminal').then((mod) => ({ default: mod.ShellTerminal })))

export function ShellTab() {
    const { instances, activeSessionId, activeSandboxId } = useSandboxContext()
    const runningInstances = instances.filter(isSandboxRunning)
    const activeInstance = activeSandboxId
        ? runningInstances.find((i) => i.id === activeSandboxId)
        : runningInstances.find((i) => i.sessionId === activeSessionId)

    return (
        <div className="flex flex-col h-full">
            {/* Sandbox selector */}
            <div className="flex items-center gap-3 px-4 py-2 border-b border-brand-main-600">
                <SandboxSessionPicker />
            </div>

            {/* Idle-timeout warning. Sits above the terminal so a user
                mid-output keeps reading content while still seeing the
                banner. Hidden entirely when not within the warning
                window (T-5min from auto-stop). */}
            <ShellIdleWarning instance={activeInstance} />

            {/* Terminal */}
            <div className="flex-1 min-h-0 overflow-hidden">
                {activeInstance ? (
                    // key={instance.id} forces a full remount of the
                    // pane (and the XTerminal underneath) whenever the
                    // user picks a different sandbox from the dropdown.
                    // Without this, ActiveShellPane's useState
                    // initializer runs only on the FIRST mount, so:
                    //   - activeShellSessionId state leaks from the
                    //     previous sandbox (highlights the wrong tab)
                    //   - mountToken stays at whatever it was (so
                    //     "+ New shell" handler still bumps a counter
                    //     scoped to the old sandbox's history)
                    //   - the XTerminal eventually remounts (its key
                    //     does include instance.id) but useSandboxShell
                    //     then connects with the previous sandbox's
                    //     persisted session id from storage and gets a
                    //     session_gone reply, leaving the terminal
                    //     showing only the banner.
                    // Remount-on-key cleanly resets all of this.
                    <ActiveShellPane key={activeInstance.id} instance={activeInstance} />
                ) : (
                    <div className="flex flex-col items-center justify-center h-full pb-16">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon icon="heroicons:command-line" className="size-8 text-brand-secondary-400" />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No sandbox selected</h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Select a running sandbox to open a shell.
                        </p>
                    </div>
                )}
            </div>
        </div>
    )
}

// ActiveShellPane is the per-instance subtree that owns the active-tab
// state. Lives in its own component so React unmounts/remounts it
// whenever the active sandbox changes, which is what we want: each
// sandbox has its own active-tab persistence (storage key includes the
// sandbox id), so the state should reset on sandbox switch.
//
// The "active shell session id" is initialised from sessionStorage.
// Empty string means "let the hook auto-mint" — used both on first
// visit and after the user clicks "+ New shell."
function ActiveShellPane({ instance }: { instance: NonNullable<ReturnType<typeof useSandboxContext>['instances']>[number] }) {
    // Two pieces of state, deliberately separate:
    //
    //   activeShellSessionId — what the tab strip should highlight.
    //     Updated reactively from the terminal once the gateway
    //     reports back the assigned shell-session id. Does NOT drive
    //     the terminal's mount key.
    //
    //   mountToken — bumped only on explicit user action (tab switch
    //     or "+ New shell"). Drives the terminal's React key so it
    //     remounts and reconnects fresh.
    //
    // Keeping them separate avoids a feedback loop where the
    // session-resolution callback would re-key the terminal and
    // tear down the connection that just resolved it.
    const [activeShellSessionId, setActiveShellSessionId] = useState(() =>
        readActiveShellSession(instance.id),
    )
    const [mountToken, setMountToken] = useState(0)

    // Storage writes have to happen BEFORE the XTerminal remounts.
    // The hook in use-sandbox.ts reads the same sessionStorage key
    // during the child's first render — before any parent useEffect
    // fires — so we write synchronously inside the setter.
    const handleSelect = useCallback(
        (shellSessionId: string) => {
            if (shellSessionId === activeShellSessionId) return // no-op
            writeActiveShellSession(instance.id, shellSessionId)
            setActiveShellSessionId(shellSessionId)
            setMountToken((t) => t + 1)
        },
        [instance.id, activeShellSessionId],
    )

    const handleCreate = useCallback(() => {
        // Clear storage so useSandboxShell's auto-mint path mints a
        // fresh uuid on the new connection. The terminal will report
        // the resolved id back via onShellSessionResolved, which
        // updates activeShellSessionId for the tab highlight.
        writeActiveShellSession(instance.id, '')
        setActiveShellSessionId('')
        setMountToken((t) => t + 1)
    }, [instance.id])

    // Receive the resolved session id from the terminal. The terminal
    // calls this when its "session" event lands from the gateway —
    // typically once on initial connect, plus once after each
    // session_gone → reattach. We update only the highlight state,
    // not the mount token, so this does NOT cause a remount.
    //
    // We also notify the shell-sessions query cache so the tab strip
    // shows the new tab in the same frame instead of waiting up to
    // 15s for the next poll. The notifier optimistically inserts a
    // placeholder row AND triggers a fresh fetch so the server's
    // authoritative list reconciles within one round-trip.
    const notifyShellSessionCreated = useNotifyShellSessionCreated(instance.id)
    const handleShellSessionResolved = useCallback(
        (shellSessionId: string) => {
            if (!shellSessionId) return
            setActiveShellSessionId((current) =>
                current === shellSessionId ? current : shellSessionId,
            )
            notifyShellSessionCreated(shellSessionId)
        },
        [notifyShellSessionCreated],
    )

    return (
        <Suspense fallback={<div className="flex items-center justify-center h-full"><Loader loaderText="Loading terminal..." /></div>}>
            <div className="flex h-full flex-col">
                <ShellTabStrip
                    sandboxId={instance.id}
                    activeShellSessionId={activeShellSessionId}
                    onSelect={handleSelect}
                    onCreate={handleCreate}
                />
                <div className="flex flex-1 min-h-0">
                    <div className="flex-1 min-w-0">
                        <XTerminal
                            key={`${instance.id}:${mountToken}`}
                            image={instance.image ?? ''}
                            sandboxId={instance.id}
                            sessionId={instance.sessionId}
                            onShellSessionResolved={handleShellSessionResolved}
                        />
                    </div>
                    {/* Sessions panel sits alongside the terminal so
                        operators can see/kill orphaned tmux sessions
                        without leaving the shell view. Narrow gutter
                        so it doesn't dominate at typical viewport
                        widths. */}
                    <aside className="w-72 shrink-0 border-l border-brand-main-700 p-3 overflow-y-auto">
                        <ShellSessionsPanel sandboxId={instance.id} />
                    </aside>
                </div>
            </div>
        </Suspense>
    )
}
