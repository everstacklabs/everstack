// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const mutateAsync = vi.fn()
let isError = false
let isPending = false

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a href="/login">{children}</a>,
}))
vi.mock('@/hooks/auth', () => ({
  useVerifyMagicLink: () => ({ mutateAsync, isError, isPending }),
}))
vi.mock('@/components/layout/auth-layout', () => ({
  AuthLayout: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}))
vi.mock('@/components/brand/everstack-logo', () => ({
  EverstackLogo: () => null,
}))

const { MagicLinkVerification: Page } = await import('./magic-link-verification')

describe('MagicLinkVerification (/verify-magic-link)', () => {
  const replace = vi.fn()
  const replaceState = vi.fn()

  function openAt(hash: string) {
    vi.stubGlobal('location', { pathname: '/verify-magic-link', hash, replace })
    vi.stubGlobal('history', { state: null, replaceState })
  }

  beforeEach(() => {
    isError = false
    isPending = false
    mutateAsync.mockReset()
    replace.mockReset()
    replaceState.mockReset()
  })
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
  })

  // Opening the link must not sign anyone in: a planted link would swap the
  // visitor into the attacker's session, and a scanner that renders pages
  // would spend the token. The token is only spent on an explicit click.
  it('does not spend the token on load, only on "Continue signing in"', async () => {
    openAt('#token=abc%2B%2F%3D')
    mutateAsync.mockResolvedValue({ success: true })

    render(<Page />)

    expect(replaceState).toHaveBeenCalledWith(null, '', '/verify-magic-link')
    expect(mutateAsync).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: 'Continue signing in' }))

    await waitFor(() => expect(replace).toHaveBeenCalledWith('/'))
    expect(mutateAsync).toHaveBeenCalledTimes(1)
    expect(mutateAsync).toHaveBeenCalledWith({ token: 'abc+/=' })
  })

  it('explains a missing token instead of calling the server', () => {
    openAt('')

    render(<Page />)

    expect(mutateAsync).not.toHaveBeenCalled()
    expect(screen.getByText('Link not valid')).toBeTruthy()
  })

  it('shows the failure when the server rejects the token', async () => {
    openAt('#token=used')
    mutateAsync.mockRejectedValue(new Error('invalid token'))

    const { rerender } = render(<Page />)
    fireEvent.click(screen.getByRole('button', { name: 'Continue signing in' }))
    await waitFor(() => expect(mutateAsync).toHaveBeenCalledTimes(1))

    isError = true
    rerender(<Page />)
    expect(screen.getByText('Link not valid')).toBeTruthy()
    expect(replace).not.toHaveBeenCalled()
  })
})
