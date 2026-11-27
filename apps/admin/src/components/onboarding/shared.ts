import { getApiBaseUrl } from '@/lib/api-url'

// Brand sans (Geist, loaded via the Google Fonts link in index.html), applied
// scoped to onboarding. No monospace anywhere, per design direction.
export const BRAND_SANS =
  '"Geist", -apple-system, BlinkMacSystemFont, "Segoe UI", "Roboto", sans-serif'

// This instance's own gateway origin: the same base the quickstart snippets
// print and the in-console test request calls.
export function gatewayBase(): string {
  try {
    const b = getApiBaseUrl()
    if (b) return b.replace(/\/$/, '')
  } catch {
    // fall through to origin
  }
  return typeof window !== 'undefined'
    ? window.location.origin
    : 'https://your-instance.everstack.ai'
}
