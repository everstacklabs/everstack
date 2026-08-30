export const POST_AUTH_RETURN_URL_KEY = 'evs_post_auth_return_url'

export function safeLocalReturnURL(value: unknown, fallback = '/'): string {
  if (
    typeof value !== 'string' ||
    !value.startsWith('/') ||
    value.startsWith('//')
  ) {
    return fallback
  }

  try {
    const base = new URL('https://everstack.local')
    const parsed = new URL(value, base)
    if (parsed.origin !== base.origin) return fallback
    return `${parsed.pathname}${parsed.search}${parsed.hash}`
  } catch {
    return fallback
  }
}
