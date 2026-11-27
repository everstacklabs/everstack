// @vitest-environment jsdom
//
// Real posthog-js (not mocked): whatever it persists in the browser must not
// hold a path token or a query string. posthog-js stores the landing URL
// (session entry and initial person info); with the default cookie
// persistence that landed in a year-long `ph_<key>_posthog` cookie scoped to
// everstack.ai and sent to every *.everstack.ai host.
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest'

const TOKEN = 'inv_SECRET9f8e'
const CODE = 'DEV-CODE-4321'

// The test runner's Node exposes a non-functional global localStorage that
// shadows jsdom's, so install a working in-memory Storage for both.
class MemoryStorage implements Storage {
  private data = new Map<string, string>()
  get length() {
    return this.data.size
  }
  clear() {
    this.data.clear()
  }
  getItem(key: string) {
    return this.data.has(key) ? this.data.get(key)! : null
  }
  key(index: number) {
    return Array.from(this.data.keys())[index] ?? null
  }
  removeItem(key: string) {
    this.data.delete(key)
  }
  setItem(key: string, value: string) {
    this.data.set(key, String(value))
  }
  keys() {
    return Array.from(this.data.keys())
  }
}
const local = new MemoryStorage()
const session = new MemoryStorage()

function persistedBlobs(): string {
  const dump = (s: MemoryStorage) => s.keys().map((k) => `${k}=${s.getItem(k)}`)
  return [document.cookie, ...dump(local), ...dump(session)].join('\n')
}

describe('PostHog browser persistence', () => {
  beforeAll(() => {
    Object.defineProperty(window, 'localStorage', { value: local, configurable: true })
    Object.defineProperty(window, 'sessionStorage', { value: session, configurable: true })
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{}', { status: 200 }))))
    window.history.replaceState({}, '', `/invite/${TOKEN}?code=${CODE}#frag`)
    ;(window as { __env?: Record<string, string> }).__env = {
      POSTHOG_KEY: 'phc_test_persistence',
      POSTHOG_HOST: 'https://ph.example.test',
    }
  })
  afterAll(() => vi.unstubAllGlobals())

  it('never stores a path token or query string after landing on an invite URL', async () => {
    const { initPostHog, capturePageview, posthog } = await import('./posthog')
    initPostHog()
    capturePageview(`/invite/${TOKEN}?code=${CODE}`)
    posthog.capture('custom_event')

    const blobs = persistedBlobs()
    expect(blobs.length).toBeGreaterThan(0)
    expect(blobs).not.toContain(TOKEN)
    expect(blobs).not.toContain(CODE)
    expect(blobs).not.toContain('frag')
    // Stored per origin, and nothing in a cookie shared with other
    // *.everstack.ai hosts.
    expect(local.keys().some((k) => k.startsWith('ph_'))).toBe(true)
    expect(document.cookie).not.toMatch(/ph_/)
  })
})
