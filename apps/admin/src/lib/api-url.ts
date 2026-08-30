/**
 * Utility to get the API base URL for the admin UI.
 * 
 * Priority order:
 * 1. VITE_API_BASE_URL environment variable (explicit override for dev/production)
 * 2. Runtime-injected env from the server (for production deployments)
 * 3. Auto-detect from current window location (dynamic, for embedded admin UI)
 * 4. Fallback to localhost (development)
 */
function isLocalHost(host: string): boolean {
    const h = host.toLowerCase()
    return h === 'localhost' || h === '127.0.0.1' || h === '::1'
}

function shouldUseCurrentOriginForApiUrl(candidate: string): boolean {
    if (typeof window === 'undefined' || !window.location) return false

    try {
        const candidateUrl = new URL(candidate)
        const pageHost = window.location.hostname.toLowerCase()
        const candidateHost = candidateUrl.hostname.toLowerCase()

        // Avoid hardcoded localhost when page is opened on a tenant/domain host.
        if (isLocalHost(candidateHost) && !isLocalHost(pageHost)) {
            return true
        }

        // If page is on a tenant subdomain but API points at the parent host,
        // force same-origin so tenant routing/auth context is preserved.
        if (candidateHost !== pageHost && pageHost.endsWith(`.${candidateHost}`)) {
            return true
        }
    } catch {
        // If candidate is invalid, caller should use it as-is.
    }

    return false
}

function currentOrigin(): string {
    if (typeof window !== 'undefined' && window.location) {
        return `${window.location.protocol}//${window.location.host}`
    }

    return 'http://localhost:8089'
}

function normalizeApiBaseUrl(candidate: string): string {
    const trimmed = candidate.trim()

    if (trimmed === '' || trimmed === '/') {
        return currentOrigin()
    }

    return trimmed.endsWith('/') ? trimmed.slice(0, -1) : trimmed
}

export function getApiBaseUrl(): string {
    // Get Vite env var first (works in both dev and production builds)
    const env = (
        (typeof import.meta !== 'undefined'
            ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
            : undefined) ?? {}
    ) as Record<string, string | undefined>

    // In development, always use explicit env var if provided
    const explicit = env.VITE_API_BASE_URL
    if (explicit) {
        if (shouldUseCurrentOriginForApiUrl(explicit)) {
            return currentOrigin()
        }

        return normalizeApiBaseUrl(explicit)
    }

    // Prefer runtime-injected env from the server (production)
    if (typeof window !== "undefined" && (window as any).__env && (window as any).__env.VITE_API_BASE_URL) {
        const runtimeBase = (window as any).__env.VITE_API_BASE_URL as string
        if (shouldUseCurrentOriginForApiUrl(runtimeBase)) {
            return currentOrigin()
        }
        return normalizeApiBaseUrl(runtimeBase)
    }

    // Prefer same-origin (window) when available - for embedded admin UI
    if (typeof window !== "undefined" && window.location) {
        return currentOrigin()
    }

    // Last resort for SSR/build tools
    return 'http://localhost:8089'
}

/**
 * Alternative API URL getter that waits for client-side hydration
 */
export function getClientApiBaseUrl(): string {
    // Force client-side detection by checking for window again
    if (typeof window === "undefined") {
        return 'http://localhost:8089';
    }

    // Use the same detection logic but ensure we're client-side
    const env = (window as any).__env__ || {};
    if (env.VITE_API_BASE_URL) {
        if (shouldUseCurrentOriginForApiUrl(env.VITE_API_BASE_URL)) {
            return currentOrigin();
        }
        return normalizeApiBaseUrl(env.VITE_API_BASE_URL);
    }

    return currentOrigin();
}
