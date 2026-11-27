const TOKEN_PARAMS = ['activation_token', 'activation_fallback'] as const

/**
 * The current same-origin URL with any activation token removed, or null when
 * there is none. Billing reads the token from router state first; this keeps
 * it out of browser history and out of Referer headers sent afterwards.
 */
export function urlWithoutActivationToken(href: string): string | null {
  let url: URL
  try {
    url = new URL(href)
  } catch {
    return null
  }
  if (!url.searchParams.has('activation_token')) return null
  for (const name of TOKEN_PARAMS) url.searchParams.delete(name)
  const search = url.searchParams.toString()
  return `${url.pathname}${search ? `?${search}` : ''}${url.hash}`
}
