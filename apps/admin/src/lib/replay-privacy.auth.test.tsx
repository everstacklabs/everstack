// @vitest-environment jsdom
//
// Signed-out visitors of managed instances are recorded, so the sign-in
// pages are in replay. Their layout and copy stay readable (to see the
// handoff and any bounce), but anything a visitor or the server puts on the
// page (errors that can echo an email, the magic-link address) is masked,
// and the invite and device pages, which carry tokens and codes, are masked
// entirely.
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LoginForm } from '@/components/auth/login-form'
import { RegisterForm } from '@/components/auth/register-form'
import { AuthReplayBoundary } from '@/components/layout/auth-replay-boundary'
import { isReplayReadableRoute, REPLAY_MASK_TEXT_SELECTOR } from './replay-privacy'

const SECRET = 'victim@example.com'

vi.mock('@tanstack/react-router', () => ({
  Link: ({ children }: { children: ReactNode }) => <a href="/x">{children}</a>,
}))
vi.mock('@/hooks/auth', () => ({
  useLogin: () => ({ error: new Error(`No account for ${SECRET}`), isPending: false, mutateAsync: vi.fn() }),
  useRequestMagicLink: () => ({ error: null, isPending: false, mutateAsync: vi.fn(() => Promise.resolve()) }),
}))
vi.mock('../hooks/auth/use-auth', () => ({
  useRegister: () => ({ error: new Error(`${SECRET} is already registered`), isPending: false, mutateAsync: vi.fn() }),
}))

function textNodes(container: HTMLElement, needle: string): Node[] {
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
  const out: Node[] = []
  for (let n = walker.nextNode(); n; n = walker.nextNode()) if (n.textContent?.includes(needle)) out.push(n)
  return out
}

const masked = (n: Node) => n.parentElement?.closest(REPLAY_MASK_TEXT_SELECTOR) != null

describe('sign-in pages in replay', () => {
  afterEach(cleanup)

  it('keeps login and register readable, masks invite and device pages', () => {
    expect(isReplayReadableRoute('/login')).toBe(true)
    expect(isReplayReadableRoute('/register')).toBe(true)
    expect(isReplayReadableRoute('/invite/inv_abc')).toBe(false)
    expect(isReplayReadableRoute('/device')).toBe(false)
  })

  it('masks every auth page that is not readable', () => {
    for (const [path, readable] of [
      ['/invite/inv_abc', false],
      ['/device', false],
      ['/login', true],
    ] as const) {
      const { container, unmount } = render(
        <AuthReplayBoundary pathname={path}>
          <p>Approve code WXYZ-1234</p>
        </AuthReplayBoundary>,
      )
      const [node] = textNodes(container, 'WXYZ-1234')
      expect(masked(node), path).toBe(!readable)
      unmount()
    }
  })

  it('masks login errors but keeps the page copy readable', () => {
    const { container } = render(
      <AuthReplayBoundary pathname="/login">
        <LoginForm />
      </AuthReplayBoundary>,
    )
    const secret = textNodes(container, SECRET)
    expect(secret.length).toBeGreaterThan(0)
    for (const n of secret) expect(masked(n)).toBe(true)
    const [heading] = textNodes(container, 'Sign in')
    expect(heading && masked(heading)).toBe(false)
  })

  it('masks register errors', () => {
    const { container } = render(
      <AuthReplayBoundary pathname="/register">
        <RegisterForm />
      </AuthReplayBoundary>,
    )
    const secret = textNodes(container, SECRET)
    expect(secret.length).toBeGreaterThan(0)
    for (const n of secret) expect(masked(n)).toBe(true)
  })

  it('masks the address on the magic-link "Check your email" view', async () => {
    const { container } = render(
      <AuthReplayBoundary pathname="/login">
        <LoginForm />
      </AuthReplayBoundary>,
    )
    const tab = screen.getByRole('tab', { name: /magic link/i })
    fireEvent.mouseDown(tab, { button: 0 })
    fireEvent.click(tab)
    const input = container.querySelector<HTMLInputElement>('#magic-email')!
    fireEvent.change(input, { target: { value: 'magic.user@example.com' } })
    await act(async () => {
      fireEvent.submit(input.closest('form')!)
    })
    expect(await screen.findByText('Check your email')).toBeTruthy()
    const address = textNodes(container, 'magic.user@example.com')
    expect(address.length).toBeGreaterThan(0)
    for (const n of address) expect(masked(n)).toBe(true)
    const [copy] = textNodes(container, 'Check your email')
    expect(masked(copy)).toBe(false)
  })
})
