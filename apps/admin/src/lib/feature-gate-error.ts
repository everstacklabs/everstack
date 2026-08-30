/**
 * Helpers for turning backend feature-gate denials into friendly UI.
 *
 * The gateway's FeatureGateInterceptor rejects a gated RPC with
 * connect.CodeFailedPrecondition and a message like:
 *   "Voice requires a higher plan. feature not available: voice"   (EE tier gate)
 *   "Voice is an Enterprise feature. Upgrade at ..."                (CE build)
 *
 * These are not errors the user can act on by retrying — they mean the
 * feature isn't in the current plan. So the UI should show the upgrade
 * prompt, not a raw red error string.
 */

const FAILED_PRECONDITION_MESSAGE = /^\[failed_precondition\]/i
const FEATURE_GATE_REASON =
    /(requires a higher plan|is an Enterprise feature|feature not available)/i

/**
 * Returns true when `err` is a feature-gate denial (a FailedPrecondition
 * carrying one of the interceptor's plan/edition messages). We require both
 * the FailedPrecondition code AND a gate-shaped message so an unrelated
 * FailedPrecondition (e.g. "configure X first") doesn't get mistaken for a
 * plan upsell.
 */
export function isFeatureGateError(err: unknown): boolean {
    if (!err || typeof err !== 'object') return false
    const e = err as { code?: unknown; message?: unknown }
    const message = typeof e.message === 'string' ? e.message : ''
    const isFailedPrecondition =
        e.code === 9 ||
        e.code === 'failed_precondition' ||
        FAILED_PRECONDITION_MESSAGE.test(message)
    return isFailedPrecondition && FEATURE_GATE_REASON.test(message)
}

/**
 * Strip Connect's `[code]` prefix from an error message so a fallback error
 * card reads like a sentence instead of `[internal] ...`.
 */
export function cleanErrorMessage(err: unknown): string {
    const raw =
        err instanceof Error
            ? err.message
            : typeof err === 'string'
              ? err
              : ((err as { message?: unknown })?.message as string) || ''
    const cleaned = raw.replace(/^\[[a-z_]+\]\s*/i, '').trim()
    return cleaned || 'Something went wrong.'
}
