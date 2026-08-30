/**
 * Detects whether this admin UI is running in cloud-managed mode.
 *
 * Resolution order:
 * 1. Explicit runtime/build deployment mode.
 * 2. window.__env.CLOUD_URL injected by the Go SPA handler.
 * 3. A last-resort hostname heuristic for local proxy development.
 */
export function isCloudManaged(): boolean {
    if (typeof window === 'undefined') return false
    const env = (window as any).__env || {}

    const mode = String(env.DEPLOYMENT_MODE || import.meta.env.VITE_DEPLOYMENT_MODE || '').toLowerCase()
    if (mode === 'cloud' || mode === 'cloud_managed') return true
    if (mode === 'self_hosted' || mode === 'selfhosted' || mode === 'standalone') return false

    if (env.CLOUD_URL) return true
    // In dev proxy mode, the Go SPA handler doesn't inject __env.
    // Detect by hostname: cloud instances use tenant subdomains like
    // {slug}.{org}.127.0.0.1.sslip.io or {slug}.{org}.everstack.ai
    const host = window.location.hostname
    if (host.includes('sslip.io') || host.includes('nip.io') || host.endsWith('.everstack.ai')) {
        const parts = host.split('.')
        if (parts.length >= 4) {
            console.warn('[cloud-mode] inferring cloud-managed mode from hostname; configure DEPLOYMENT_MODE explicitly')
            return true
        }
    }
    return false
}

/**
 * Returns the cloud console URL, or a fallback.
 */
export function getCloudConsoleUrl(): string {
    if (typeof window === 'undefined') return 'https://app.everstack.ai'
    const env = (window as any).__env || {}
    return env.CLOUD_URL || import.meta.env.VITE_CLOUD_URL || 'https://app.everstack.ai'
}

/**
 * Returns the cloud console billing URL for the current org.
 * Falls back to the generic cloud console URL if org can't be determined.
 */
export function getCloudBillingUrl(): string {
    const base = getCloudConsoleUrl().replace(/\/+$/, '')
    const env = typeof window === 'undefined' ? {} : (window as any).__env || {}
    const orgSlug = String(env.ORGANIZATION_SLUG || '').trim()
    if (orgSlug) {
        return `${base}/${encodeURIComponent(orgSlug)}/settings/billing?tab=overview`
    }
    return base
}
