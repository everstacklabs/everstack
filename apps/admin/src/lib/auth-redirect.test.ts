import { describe, expect, it } from 'vitest'
import { isAuthError } from './auth-redirect'

describe('isAuthError', () => {
  // Only a real authentication failure (session gone) should trigger a
  // logout/redirect. Regression guard: PermissionDenied must NOT — it logged
  // users out when they visited a feature-gated / restricted page.
  it.each([
    { code: 16 },
    { code: 'unauthenticated' },
  ])('treats Unauthenticated as an auth error (%o)', (err) => {
    expect(isAuthError(err)).toBe(true)
  })

  it.each([
    { code: 7 },
    { code: 'permission_denied' },
    { code: 9 },
    { code: 'failed_precondition' },
    { code: 5 },
    { code: 'not_found' },
    { code: undefined },
  ])('does NOT treat non-authentication codes as an auth error (%o)', (err) => {
    expect(isAuthError(err)).toBe(false)
  })

  it.each([null, undefined, 'nope', 42])('is safe for non-object input %s', (input) => {
    expect(isAuthError(input)).toBe(false)
  })
})
