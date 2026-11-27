import { describe, expect, it } from 'vitest'
import { isPublicAuthRoute } from './auth-routes'

describe('isPublicAuthRoute', () => {
    // The magic-link email opens /verify-magic-link while signed out. If the
    // guard treated it as protected it would bounce to /login before the
    // token could be verified, and the link would never sign anyone in.
    it('lets the magic-link landing page through', () => {
        expect(isPublicAuthRoute('/verify-magic-link')).toBe(true)
    })

    it('keeps the existing public routes', () => {
        for (const path of ['/login', '/register', '/invite/abc', '/device']) {
            expect(isPublicAuthRoute(path)).toBe(true)
        }
    })

    it('guards dashboard routes', () => {
        for (const path of ['/', '/settings', '/deployments/agents', '/vault/api-keys']) {
            expect(isPublicAuthRoute(path)).toBe(false)
        }
    })
})
