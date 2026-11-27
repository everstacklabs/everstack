import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  CLOUD_LOGIN_BOUNCE_AT_KEY,
  CLOUD_LOGIN_BOUNCE_WINDOW_MS,
  cloudLoginURL,
  cloudLoginURLForPage,
  isAuthError,
  resetCloudLoginURLForPage,
} from './auth-redirect'

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

const cloud = 'https://app.everstack.ai'
const instanceOrigin = 'https://xdxd-abcdef.eu-gra-1.everstack.ai'
const instancePage = `${instanceOrigin}/deployments/agents`
const withReturn = `${cloud}/login?returnUrl=${encodeURIComponent(instancePage)}`
const bare = `${cloud}/login`

function memoryStorage(initial: Record<string, string> = {}) {
  const store = new Map(Object.entries(initial))
  return {
    store,
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => {
      store.set(key, value)
    },
  }
}

describe('cloudLoginURL', () => {
  it('names the instance page as the return target on a first bounce', () => {
    expect(cloudLoginURL(cloud, instancePage, memoryStorage(), 1_000)).toBe(withReturn)
  })

  it('records when the bounce happened', () => {
    const storage = memoryStorage()
    cloudLoginURL(cloud, instancePage, storage, 1_000)
    expect(storage.store.get(CLOUD_LOGIN_BOUNCE_AT_KEY)).toBe('1000')
  })

  it('drops the return target when the previous bounce was moments ago', () => {
    const storage = memoryStorage({ [CLOUD_LOGIN_BOUNCE_AT_KEY]: '1000' })
    expect(
      cloudLoginURL(cloud, instancePage, storage, 1_000 + CLOUD_LOGIN_BOUNCE_WINDOW_MS - 1),
    ).toBe(bare)
  })

  it('carries the return target again once the window has passed', () => {
    const storage = memoryStorage({ [CLOUD_LOGIN_BOUNCE_AT_KEY]: '1000' })
    expect(
      cloudLoginURL(cloud, instancePage, storage, 1_000 + CLOUD_LOGIN_BOUNCE_WINDOW_MS),
    ).toBe(withReturn)
  })

  it('ignores an unreadable bounce record', () => {
    const storage = memoryStorage({ [CLOUD_LOGIN_BOUNCE_AT_KEY]: 'not a time' })
    expect(cloudLoginURL(cloud, instancePage, storage, 5_000)).toBe(withReturn)
  })

  it('drops the return target when the bounce cannot be recorded', () => {
    expect(cloudLoginURL(cloud, instancePage, null, 1_000)).toBe(bare)
    const throwing = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {
        throw new Error('blocked')
      },
    }
    expect(cloudLoginURL(cloud, instancePage, throwing, 1_000)).toBe(bare)
  })

  it('returns the bare sign-in URL without a return target', () => {
    expect(cloudLoginURL(cloud, '', memoryStorage(), 1_000)).toBe(bare)
  })

  it('tolerates a trailing slash on the cloud URL', () => {
    expect(cloudLoginURL(`${cloud}/`, instancePage, memoryStorage(), 1_000)).toBe(withReturn)
  })
})

describe('cloudLoginURLForPage', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
    resetCloudLoginURLForPage()
  })

  it('computes the target once per page load, so parallel bounces agree', () => {
    const storage = memoryStorage()
    vi.stubGlobal('window', {
      __env: { CLOUD_URL: cloud },
      location: { origin: instanceOrigin, pathname: '/deployments/agents' },
      sessionStorage: storage,
    })
    // A failed data request and the session poll both bounce in the same
    // tick (for example refocusing a tab after expiry); the second must not
    // read the first one's stamp as a loop.
    expect(cloudLoginURLForPage()).toBe(withReturn)
    expect(cloudLoginURLForPage()).toBe(withReturn)
    expect(storage.store.get(CLOUD_LOGIN_BOUNCE_AT_KEY)).toBeDefined()
  })

  it('falls back only on a real second bounce, after a completed round trip', () => {
    const storage = memoryStorage()
    vi.stubGlobal('window', {
      __env: { CLOUD_URL: cloud },
      location: { origin: instanceOrigin, pathname: '/' },
      sessionStorage: storage,
    })
    expect(cloudLoginURLForPage()).toBe(`${bare}?returnUrl=${encodeURIComponent(`${instanceOrigin}/`)}`)
    // The cloud relays the user straight back; this is a new page load.
    resetCloudLoginURLForPage()
    expect(cloudLoginURLForPage()).toBe(bare)
  })

  it('returns to this origin and path, leaving query and hash behind', () => {
    vi.stubGlobal('window', {
      __env: { CLOUD_URL: cloud },
      location: {
        origin: instanceOrigin,
        pathname: '/deployments/agents',
        search: '?token=secret',
        hash: '#step',
      },
      sessionStorage: memoryStorage(),
    })
    expect(cloudLoginURLForPage()).toBe(withReturn)
  })

  it('falls back to the bare URL when session storage is blocked', () => {
    vi.stubGlobal('window', {
      __env: { CLOUD_URL: cloud },
      location: { origin: instanceOrigin, pathname: '/' },
      get sessionStorage(): Storage {
        throw new Error('blocked')
      },
    })
    expect(cloudLoginURLForPage()).toBe(bare)
  })

  it('is the bare URL outside a browser', () => {
    expect(cloudLoginURLForPage()).toBe(bare)
  })
})
