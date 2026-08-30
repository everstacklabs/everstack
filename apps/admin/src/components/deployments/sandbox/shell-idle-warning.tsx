import { useEffect, useState, useCallback } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { renewSandboxExpiration, type SandboxInstance } from '@/server/sandbox'

// Idle-timeout warning + extend-now button for the Shell tab.
//
// Sandboxes have an idle-retention window (idle_retention_secs from
// the plan tier or per-sandbox override). The reaper stops them when
// (now - last_used_at) exceeds that window. The shell tab is one of
// the few user-facing surfaces where someone might be actively
// reading a long-running command's output WITHOUT producing input
// that touches last_used_at — so they can hit the cliff without
// realising it.
//
// This banner appears at T-5min and escalates at T-1min, with a
// "Stay open" button that calls /renew-expiration (which extends by
// 30 min AND resets last_used_at to now).
//
// Hidden entirely when:
//   - idleRetentionSecs is undefined or 0 (sandbox doesn't expire)
//   - lastUsedAt is missing (no data to compute against)
//   - deadline > 5 minutes away
//
// The banner does NOT prevent the user from reading the terminal —
// it's an inline strip above it so a user mid-output keeps seeing
// content. We picked inline-banner over toast because toasts are
// dismissible and the user might dismiss-and-forget the warning;
// the banner stays until the timeout is extended or the deadline
// passes.

interface ShellIdleWarningProps {
    instance: SandboxInstance | undefined
}

// Extend by 30 minutes when the user clicks. Chosen because:
//   - Long enough that an active user isn't pestered every 5 min
//   - Short enough that idle-cleanup still happens within an hour
//     when the user walks away after extending
//   - Round number / familiar (matches common SaaS "extend session"
//     defaults)
const EXTEND_SECONDS = 30 * 60

// Banner thresholds. T-5min = soft warning (neutral colors).
// T-1min = hard warning (red/urgent, animated).
const WARN_THRESHOLD_MS = 5 * 60 * 1000
const URGENT_THRESHOLD_MS = 60 * 1000

export function ShellIdleWarning({ instance }: ShellIdleWarningProps) {
    const queryClient = useQueryClient()
    const [now, setNow] = useState(() => Date.now())

    // 10s tick is sufficient — the banner only changes visual state
    // at the 5min and 1min boundaries. requestAnimationFrame would
    // be wasteful for a near-static UI element.
    useEffect(() => {
        const id = setInterval(() => setNow(Date.now()), 10_000)
        return () => clearInterval(id)
    }, [])

    const extendMutation = useMutation({
        mutationFn: () => renewSandboxExpiration(instance!.id, EXTEND_SECONDS),
        onSuccess: () => {
            toast.success('Idle timeout extended by 30 minutes')
            // Force a fresh fetch of the instance list so lastUsedAt
            // updates everywhere this sandbox is rendered (including
            // the picker and any other tabs sharing the same query).
            queryClient.invalidateQueries({ queryKey: ['sandbox-instances'] })
        },
        onError: (err: Error) => {
            toast.error(`Couldn't extend: ${err.message}`)
        },
    })

    const handleExtend = useCallback(() => {
        extendMutation.mutate()
    }, [extendMutation])

    // Gate everything on having the data we need.
    if (
        !instance ||
        !instance.idleRetentionSecs ||
        instance.idleRetentionSecs <= 0 ||
        !instance.lastUsedAt
    ) {
        return null
    }

    const lastUsedMs = new Date(instance.lastUsedAt).getTime()
    if (Number.isNaN(lastUsedMs)) return null

    const deadlineMs = lastUsedMs + instance.idleRetentionSecs * 1000
    const remainingMs = deadlineMs - now

    // Outside the warning window — render nothing.
    if (remainingMs > WARN_THRESHOLD_MS) return null

    // Past the deadline — the reaper is about to stop the sandbox.
    // We still render the banner so the user sees what just
    // happened, but the "Stay open" call will probably race the
    // reaper. Either it lands first and saves the sandbox, or the
    // gateway returns an error which the toast surfaces.
    const isUrgent = remainingMs <= URGENT_THRESHOLD_MS
    const isPastDeadline = remainingMs <= 0

    const containerClass = isUrgent
        ? 'bg-red-500/15 border-b border-red-500/40 text-red-200 light:text-red-600'
        : 'bg-yellow-500/10 border-b border-yellow-500/30 text-yellow-200 light:text-yellow-700'
    const iconClass = isUrgent ? 'text-red-400 light:text-red-600 animate-pulse' : 'text-yellow-400 light:text-yellow-700'

    return (
        <div
            className={`flex items-center gap-2 px-4 py-1.5 text-xs ${containerClass}`}
            role="status"
            aria-live={isUrgent ? 'assertive' : 'polite'}
        >
            <Iconify.Icon
                icon={isUrgent ? 'heroicons:exclamation-triangle-solid' : 'heroicons:clock'}
                className={`size-3.5 ${iconClass}`}
            />
            <span className="flex-1 truncate">
                {isPastDeadline
                    ? 'Idle timeout reached — sandbox will stop shortly.'
                    : `Sandbox will stop in ${formatRemaining(remainingMs)} unless you extend the timeout.`}
            </span>
            <button
                onClick={handleExtend}
                disabled={extendMutation.isPending}
                className="text-xs font-medium px-2 py-0.5 rounded border border-current/40 hover:bg-white/5 light:hover:bg-black/5 disabled:opacity-50 disabled:cursor-not-allowed"
            >
                {extendMutation.isPending ? 'Extending…' : 'Stay open (+30m)'}
            </button>
        </div>
    )
}

// formatRemaining produces "Mm Ss" / "Ms" copy for the countdown.
// Stays human-readable down to seconds without flooding the UI with
// constant micro-changes (the 10s tick means we step in 10s chunks
// most of the time anyway).
function formatRemaining(ms: number): string {
    if (ms <= 0) return '0s'
    const totalSeconds = Math.ceil(ms / 1000)
    const minutes = Math.floor(totalSeconds / 60)
    const seconds = totalSeconds % 60
    if (minutes <= 0) return `${seconds}s`
    if (seconds === 0) return `${minutes}m`
    return `${minutes}m ${seconds}s`
}
