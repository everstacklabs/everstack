// Minimal connection status strip for the Shell tab.
//
// Shows the user only what they need to act on: am I connected,
// am I reconnecting, am I disconnected. Anything operationally
// internal — uptime, transport (vsock/ws), session id, WS close
// code, reconnect attempt counter — is hidden. Those signals
// live in dev tools and metrics for operators; they have no
// place in a user-facing surface.

interface ShellStatusPanelProps {
    isConnected: boolean
    isReconnecting: boolean
    // Terminal state: the gateway reported the sandbox VM is gone on
    // every fcagent. Reconnecting cannot help; the user has to
    // reprovision. Suppresses the Reconnect affordance and the
    // yellow "Reconnecting" treatment.
    isGone?: boolean
    // Non-terminal recovery: the VM died but the platform is
    // auto-restoring it (lifecycle still desires running). Distinct from
    // isReconnecting (a transient socket drop) and isGone (terminal): we
    // tell the user the sandbox is restarting and let the reconnect loop
    // reattach once the new VM is up.
    isRecovering?: boolean
    onReconnect: () => void
}

export function ShellStatusPanel({
    isConnected,
    isReconnecting,
    isGone = false,
    isRecovering = false,
    onReconnect,
}: ShellStatusPanelProps) {
    const stateColor = isGone
        ? 'bg-red-500'
        : isConnected
          ? 'bg-green-500'
          : isRecovering
            ? 'bg-sky-400 animate-pulse'
            : isReconnecting
              ? 'bg-yellow-500 animate-pulse'
              : 'bg-red-500'

    const stateLabel = isGone
        ? 'Sandbox no longer running'
        : isConnected
          ? 'Connected'
          : isRecovering
            ? 'Restarting sandbox…'
            : isReconnecting
              ? 'Reconnecting…'
              : 'Disconnected'

    return (
        <div className="flex items-center gap-2 px-4 py-1 bg-brand-main-900 border-b border-brand-main-700">
            <div className={`w-2 h-2 rounded-full ${stateColor}`} />
            <span className="text-xs text-white/70 light:text-black/70 font-medium">{stateLabel}</span>
            {isGone && (
                <span className="text-xs text-white/40 light:text-black/40">
                    Reprovision it from the Instances tab to start a new shell.
                </span>
            )}
            {isRecovering && !isConnected && (
                <span className="text-xs text-white/40 light:text-black/40">
                    The VM restarted; your shell will reconnect automatically.
                </span>
            )}

            <div className="flex-1" />

            {/* Reconnect affordance only when fully stopped (not retrying,
                not auto-recovering, and not terminally gone). */}
            {!isConnected && !isReconnecting && !isRecovering && !isGone && (
                <button
                    onClick={onReconnect}
                    className="text-xs text-brand-secondary-400 hover:text-brand-secondary-300"
                >
                    Reconnect
                </button>
            )}
        </div>
    )
}
