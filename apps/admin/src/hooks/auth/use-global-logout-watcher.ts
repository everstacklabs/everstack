import { useEffect, useRef } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { isCloudManaged, getCloudConsoleUrl } from '@/lib/cloud-mode'
import { resetPostHog } from '@/lib/posthog'
import { setActiveOrgId } from '@/lib/active-org'
import { useAgentSessionStore } from '@/stores/agent-session-store'

/**
 * Name of the cross-tab logout marker cookie set by the cloud control
 * plane's `POST /api/auth/signout`. Same parent domain as the cloud
 * session cookie, **not** HttpOnly (we read it via document.cookie),
 * short MaxAge. The value is a unix-ms timestamp — not a secret, just
 * "the world changed at this time."
 *
 * Mirrors `service.GlobalLogoutMarkerCookieName` on the server.
 */
const GLOBAL_LOGOUT_MARKER = 'evs_global_logout_at'

/**
 * Polling interval for the marker check. 1s is enough to feel
 * instantaneous in the foreground; browsers throttle background-tab
 * timers harder (often 1Hz min), but background tabs are already
 * server-side invalid after a cascade so the worst case is "the user
 * tabs back into a dead instance and gets redirected on their next
 * action via the AuthGuard fallback."
 */
const POLL_MS = 1000

/**
 * Reads a specific cookie's value from `document.cookie`. Returns ""
 * when the cookie is absent or the document/cookies aren't available
 * (SSR, restricted contexts).
 */
function readCookie(name: string): string {
    if (typeof document === 'undefined' || !document.cookie) return ''
    const prefix = name + '='
    for (const part of document.cookie.split(';')) {
        const trimmed = part.trim()
        if (trimmed.startsWith(prefix)) {
            return trimmed.slice(prefix.length)
        }
    }
    return ''
}

/**
 * useGlobalLogoutWatcher polls for the `evs_global_logout_at` cookie
 * set by the cloud control plane during a global signout cascade. When
 * the marker value changes from the baseline recorded at mount, the
 * watcher:
 *
 *  - Clears the React Query cache and the agent session store so no
 *    stale tenant data bleeds across the logout boundary.
 *  - Resets analytics state.
 *  - Drops the active-org pointer.
 *  - Navigates the browser to `${cloudConsoleUrl}/login` so the user
 *    lands on the canonical sign-in surface.
 *
 * Only runs in cloud-managed mode. In self-hosted mode the cookie
 * doesn't exist, and the cross-tab semantics don't apply (single tenant,
 * single user typically).
 *
 * Mount this once near the top of the auth tree (e.g. inside AuthGuard
 * for protected routes) — running it more than once is safe but wasteful.
 */
export function useGlobalLogoutWatcher() {
    const queryClient = useQueryClient()
    const baselineRef = useRef<string | null>(null)

    useEffect(() => {
        if (!isCloudManaged()) return
        if (typeof window === 'undefined') return

        // Capture the marker value at mount as the baseline. A logout
        // that happened before the page loaded would have set a marker
        // that's already in the jar; we don't redirect on that one
        // because the cookie clear at logout time means the next API
        // call will already 401 and AuthGuard will redirect via its
        // own path.
        baselineRef.current = readCookie(GLOBAL_LOGOUT_MARKER)

        const id = window.setInterval(() => {
            const current = readCookie(GLOBAL_LOGOUT_MARKER)
            if (!current) return
            if (current === baselineRef.current) return

            // Marker changed → a fresh signout happened in another tab.
            // Clear local state and navigate. We deliberately do NOT
            // call queryClient.cancelQueries first — racing with
            // in-flight requests is fine because the cookie is already
            // dead server-side; in-flight requests will 401 and the
            // navigation supersedes whatever they would have rendered.
            resetPostHog()
            setActiveOrgId(null)
            queryClient.clear()
            useAgentSessionStore.getState().clearAll()
            const target = `${getCloudConsoleUrl()}/login`
            window.location.href = target
        }, POLL_MS)

        return () => {
            window.clearInterval(id)
        }
    }, [queryClient])
}
