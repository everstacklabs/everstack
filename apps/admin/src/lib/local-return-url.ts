import { safeReturnPath } from '@everstack/utils/functions/return-url'

export const POST_AUTH_RETURN_URL_KEY = 'evs_post_auth_return_url'

// Any origin works as the base: only the same-origin path is ever returned.
const LOCAL_BASE = 'https://everstack.local'

/**
 * A same-origin route for a post-auth return value, or the fallback. Shares
 * the console's return URL policy so the two apps cannot drift apart.
 */
export function safeLocalReturnURL(value: unknown, fallback = '/'): string {
  return safeReturnPath(value, LOCAL_BASE, fallback)
}
