import { isCloudManaged, getCloudConsoleUrl } from '@/lib/cloud-mode'

// Once a redirect is in flight, drop subsequent calls. A burst of parallel
// queries that all fail with the same auth error would otherwise pile up
// navigations; the first one wins and the rest no-op. Module-scoped so the
// guard survives across React renders without needing a ref.
let redirecting = false

/** sessionStorage key: when this tab last bounced to the cloud sign-in. */
export const CLOUD_LOGIN_BOUNCE_AT_KEY = 'evs_cloud_login_bounce_at'

/**
 * Two bounces inside this window mean the cloud relayed the user straight
 * back and this instance refused them again. The second bounce drops the
 * return target so the two sides cannot ping-pong; the user lands on the
 * cloud console instead, as before.
 */
export const CLOUD_LOGIN_BOUNCE_WINDOW_MS = 60_000

type BounceStorage = Pick<Storage, 'getItem' | 'setItem'>

/**
 * The cloud sign-in URL for a bounce from this instance.
 *
 * `returnTo` names this instance so the cloud can bring the user back: a
 * live cloud session relays into the console without another click, and a
 * fresh sign-in lands on this instance instead of on the cloud home. The
 * cloud keeps only the origin and opens the console root. Without it the
 * cloud has no idea where the user was going, so a lost session on the
 * instance ended on a sign-in page that led nowhere. The cloud honours a
 * return target only when it is on its tenant allowlist (the configured
 * instance base domains), so this cannot send anyone anywhere else.
 *
 * `storage` records the bounce; when it is missing or fails the bare URL is
 * returned, because a return target without the loop guard is not safe.
 */
export function cloudLoginURL(
    cloudBase: string,
    returnTo: string,
    storage: BounceStorage | null,
    now: number,
): string {
    const bare = `${cloudBase.replace(/\/+$/, '')}/login`
    if (!returnTo || !storage) return bare
    let last = 0
    try {
        last = Number(storage.getItem(CLOUD_LOGIN_BOUNCE_AT_KEY)) || 0
        storage.setItem(CLOUD_LOGIN_BOUNCE_AT_KEY, String(now))
    } catch {
        return bare
    }
    if (last > 0 && now - last < CLOUD_LOGIN_BOUNCE_WINDOW_MS) return bare
    return `${bare}?returnUrl=${encodeURIComponent(returnTo)}`
}

// The bounce target of this page load, computed once. Several callers can
// bounce in the same tick (a failed data request and the session poll when a
// tab is refocused after expiry, or effects running twice in development);
// they must all agree, and only a real second bounce, on a later page load
// after a completed round trip, counts for the loop guard.
let pageBounceTarget: string | null = null

/**
 * cloudLoginURL for the running page: returns to this origin and path. The
 * query and hash stay behind; they are not needed to come back and would
 * otherwise end up in the cloud's URL and history.
 */
export function cloudLoginURLForPage(): string {
    if (pageBounceTarget !== null) return pageBounceTarget
    const base = getCloudConsoleUrl()
    if (typeof window === 'undefined') return cloudLoginURL(base, '', null, 0)
    let storage: BounceStorage | null = null
    try {
        storage = window.sessionStorage
    } catch {
        storage = null
    }
    pageBounceTarget = cloudLoginURL(
        base,
        window.location.origin + window.location.pathname,
        storage,
        Date.now(),
    )
    return pageBounceTarget
}

/** Forgets the page's bounce target. Tests only; a real page never needs it. */
export function resetCloudLoginURLForPage(): void {
    pageBounceTarget = null
}

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
    const target = cloudLoginURLForPage()
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
