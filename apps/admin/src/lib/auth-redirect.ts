import { isCloudManaged, getCloudConsoleUrl } from '@/lib/cloud-mode'

// Once a redirect is in flight, drop subsequent calls. A burst of parallel
// queries that all fail with the same auth error would otherwise pile up
// navigations; the first one wins and the rest no-op. Module-scoped so the
// guard survives across React renders without needing a ref.
let redirecting = false

/**
 * Bounce the browser to the cloud control plane's /login. Called from
 * transport interceptors when an instance API call returns an auth-shaped
 * Connect error — usually because the cloud session is gone, or because
 * the user holds a valid cloud session but no org membership for this
 * instance. Either way, the SPA can't recover from here — only the cloud
 * can re-auth the user (and re-evaluate membership for this instance).
 *
 * Cloud-managed mode only. Self-hosted instances have their own local
 * /login form that AuthGuard already targets via tanstack-router; bouncing
 * to a cloud URL would be wrong there.
 */
export function redirectToCloudLogin(): void {
    if (redirecting) return
    if (typeof window === 'undefined') return
    if (!isCloudManaged()) return
    const target = `${getCloudConsoleUrl()}/login`
    if (window.location.href === target) return
    redirecting = true
    window.location.href = target
}

/**
 * Returns true only for an *authentication* failure — `Unauthenticated`
 * (16) — meaning the session itself is gone/invalid, so re-login is the
 * correct recovery.
 *
 * We deliberately do NOT treat `PermissionDenied` (7) as a logout trigger.
 * PermissionDenied means "you are authenticated but not allowed to do this
 * specific thing" — a plan/feature gate, a per-resource authz denial, or a
 * tenant not resolved for one request. Bouncing to /login on those logged
 * users out for no reason when they merely visited a gated or restricted
 * page (e.g. /observability/alerts, /deployments/voice, the LLM providers
 * page). Those must surface as an in-app error/upgrade prompt; only a real
 * lost session (Unauthenticated) warrants re-auth, and AuthGuard's session
 * poll is the authority for that.
 *
 * Accepts numeric codes (Connect's wire format) and the string aliases
 * some SDK builds surface so the check survives a transport upgrade.
 */
export function isAuthError(err: unknown): boolean {
    if (!err || typeof err !== 'object') return false
    const code = (err as { code?: unknown }).code
    return code === 16 || code === 'unauthenticated'
}
