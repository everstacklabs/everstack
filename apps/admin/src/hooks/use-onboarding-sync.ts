import { useEffect, useRef } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useSession } from '@/hooks/auth/use-auth'
import { useOnboardingStore } from '@/stores/onboarding-store'
import {
    getOnboardingState,
    updateOnboardingState,
    type OnboardingServerState,
} from '@/server/onboarding'

const PERSIST_DEBOUNCE_MS = 400

const DEFAULT_STATE: OnboardingServerState = {
    dismissed: false,
    celebrationShown: false,
    selectedPath: '',
    sandboxSkipped: false,
}

// Snapshot only the durable fields the server tracks. `minimized` and
// `hydrated` are intentionally excluded so toggling the minimized panel or the
// hydrate itself never triggers a network write.
function durableSnapshot(): OnboardingServerState {
    const s = useOnboardingStore.getState()
    return {
        dismissed: s.dismissed,
        celebrationShown: s.celebrationShown,
        selectedPath: s.selectedPath ?? '',
        sandboxSkipped: s.sandboxSkipped,
    }
}

function durableChanged(
    a: ReturnType<typeof useOnboardingStore.getState>,
    b: ReturnType<typeof useOnboardingStore.getState>,
): boolean {
    return (
        a.dismissed !== b.dismissed ||
        a.celebrationShown !== b.celebrationShown ||
        a.selectedPath !== b.selectedPath ||
        a.sandboxSkipped !== b.sandboxSkipped
    )
}

/**
 * useOnboardingSync wires the onboarding store to the server. Mount it exactly
 * once, inside the authenticated shell.
 *
 * The ordering invariant that protects the data: the first network direction
 * is always a GET. No POST fires until that GET has hydrated the store, and the
 * hydrate itself is never echoed back. Without this, a cleared browser cache
 * would initialise the store to defaults and POST them over the server's real
 * state, permanently resetting onboarding — the exact failure this feature
 * exists to prevent.
 */
export function useOnboardingSync() {
    const { data: session } = useSession()
    const isAuthed = !!session?.user
    const hydrate = useOnboardingStore((s) => s.hydrate)

    // Gates writes. Flipped true only around a hydrate, so the synchronous
    // listener fired by hydrate's set() is skipped while this is false.
    const writeEnabledRef = useRef(false)
    const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)

    const { data, isError } = useQuery({
        queryKey: ['onboarding-state'],
        queryFn: getOnboardingState,
        enabled: isAuthed,
        staleTime: Infinity,
        gcTime: Infinity,
        retry: 1,
        refetchOnWindowFocus: false,
        refetchOnReconnect: false,
    })

    // Hydrate the store once the GET settles. Disable writes across the hydrate
    // so its own state change is not posted back. On error we still hydrate to
    // defaults: a failed GET must never strand the UI on a loader. The cost is
    // that an unreachable server shows fresh onboarding for the session, which
    // is exactly the pre-server-flag behaviour, and self-corrects on reload.
    useEffect(() => {
        if (!data && !isError) return
        writeEnabledRef.current = false
        hydrate(data ?? DEFAULT_STATE)
        // Only enable write-back when the server actually answered. If the GET
        // failed we have no authoritative baseline, so suppress POSTs to avoid
        // clobbering real server state with defaults.
        writeEnabledRef.current = !isError
    }, [data, isError, hydrate])

    // Persist durable changes back to the server, debounced. Set up once.
    useEffect(() => {
        const unsub = useOnboardingStore.subscribe((state, prev) => {
            if (!writeEnabledRef.current) return
            if (!durableChanged(state, prev)) return
            if (debounceRef.current) clearTimeout(debounceRef.current)
            debounceRef.current = setTimeout(() => {
                void updateOnboardingState(durableSnapshot()).catch(() => {
                    // Best-effort: local state stays correct for the session;
                    // auth-shaped failures are handled by the transport's
                    // redirect interceptor.
                })
            }, PERSIST_DEBOUNCE_MS)
        })
        return () => {
            unsub()
            if (debounceRef.current) clearTimeout(debounceRef.current)
        }
    }, [])
}
