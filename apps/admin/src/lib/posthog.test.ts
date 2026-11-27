// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

vi.mock('posthog-js', () => ({
  default: {
    init: vi.fn(),
    register: vi.fn(),
    identify: vi.fn(),
    group: vi.fn(),
    reset: vi.fn(),
    capture: vi.fn(),
    get_property: vi.fn(),
  },
}))

type Env = Record<string, string | undefined>

function setRuntimeEnv(env: Env | undefined) {
  ;(window as { __env?: Env }).__env = env
}

// Fresh module per test: the init guard is module state.
async function load() {
  vi.resetModules()
  const mod = await import('./posthog')
  const { default: posthog } = await import('posthog-js')
  return { ...mod, ph: vi.mocked(posthog) }
}

describe('resolvePostHogConfig', () => {
  it('records when the gateway provides both key and host', async () => {
    const { resolvePostHogConfig } = await load()
    expect(
      resolvePostHogConfig({ POSTHOG_KEY: 'phc_x', POSTHOG_HOST: 'https://ph.everstack.ai' }, {}, false),
    ).toEqual({ key: 'phc_x', host: 'https://ph.everstack.ai' })
  })

  // The console ships inside self-hosted and CE gateway binaries. A key
  // baked in at build time must never turn analytics on there.
  it('ignores build-time keys outside the dev server', async () => {
    const { resolvePostHogConfig } = await load()
    const build = { key: 'phc_baked', host: 'https://ph.everstack.ai' }
    expect(resolvePostHogConfig({}, build, false)).toBeNull()
    expect(resolvePostHogConfig({ POSTHOG_HOST: 'https://ph.everstack.ai' }, build, false)).toBeNull()
    expect(resolvePostHogConfig({}, build, true)).toEqual({ key: 'phc_baked', host: 'https://ph.everstack.ai' })
  })

  // No default host: an operator who sets only a key must not route
  // events through Everstack's proxy.
  it('stays off without a host', async () => {
    const { resolvePostHogConfig } = await load()
    expect(resolvePostHogConfig({ POSTHOG_KEY: 'phc_x' }, {}, false)).toBeNull()
  })
})

describe('resolveAnalyticsGroups', () => {
  it('groups a managed instance by its owning organization and the instance', async () => {
    const { resolveAnalyticsGroups } = await load()
    const groups = resolveAnalyticsGroups(
      { ORGANIZATION_ID: 'org-owner', ORGANIZATION_SLUG: 'owner', INSTANCE_ID: 'inst-1' },
      { id: 'org-other', slug: 'other', name: 'Other Org' },
    )
    // The user's first membership is a different org: the host's org wins
    // and the other org's name is not attached to it.
    expect(groups).toEqual({
      organization: { id: 'org-owner', slug: 'owner', name: undefined },
      instance: { id: 'inst-1', organizationId: 'org-owner' },
    })
  })

  it('falls back to the session organization when self-hosted', async () => {
    const { resolveAnalyticsGroups } = await load()
    expect(resolveAnalyticsGroups({}, { id: 'org-1', slug: 'acme', name: 'Acme' })).toEqual({
      organization: { id: 'org-1', slug: 'acme', name: 'Acme' },
    })
  })
})

describe('initPostHog / identifyUser', () => {
  beforeEach(() => setRuntimeEnv(undefined))
  afterEach(() => {
    setRuntimeEnv(undefined)
    vi.clearAllMocks()
  })

  it('does nothing when the gateway did not provide a key', async () => {
    setRuntimeEnv({ POSTHOG_HOST: 'https://ph.everstack.ai' })
    const { initPostHog, identifyUser, ph } = await load()
    initPostHog()
    identifyUser({ userId: 'u1', organizationId: 'org-1' })
    expect(ph.init).not.toHaveBeenCalled()
    expect(ph.identify).not.toHaveBeenCalled()
    expect(ph.group).not.toHaveBeenCalled()
  })

  it('records replay with inputs and sensitive text masked, and no payloads or exceptions', async () => {
    setRuntimeEnv({ POSTHOG_KEY: 'phc_x', POSTHOG_HOST: 'https://ph.everstack.ai' })
    const { initPostHog, ph } = await load()
    const privacy = await import('./replay-privacy')
    const sanitize = await import('./analytics-sanitize')
    initPostHog()
    expect(ph.init).toHaveBeenCalledTimes(1)
    const [key, options] = ph.init.mock.calls[0]
    expect(key).toBe('phc_x')
    expect(options).toMatchObject({
      api_host: 'https://ph.everstack.ai',
      mask_all_text: true,
      mask_all_element_attributes: true,
      disable_session_recording: false,
      capture_exceptions: false,
      enable_recording_console_log: false,
      persistence: 'localStorage',
      cross_subdomain_cookie: false,
      save_referrer: false,
      save_campaign_params: false,
      before_send: sanitize.sanitizeCaptureResult,
      session_recording: {
        maskAllInputs: true,
        maskTextSelector: privacy.REPLAY_MASK_TEXT_SELECTOR,
        blockSelector: privacy.REPLAY_BLOCK_SELECTOR,
        captureCanvas: { recordCanvas: false },
        recordCrossOriginIframes: false,
        recordHeaders: false,
        recordBody: false,
        maskCapturedNetworkRequestFn: privacy.stripNetworkPayload,
      },
    })
  })

  it('identifies the user and groups by organization and instance', async () => {
    setRuntimeEnv({
      POSTHOG_KEY: 'phc_x',
      POSTHOG_HOST: 'https://ph.everstack.ai',
      ORGANIZATION_ID: 'org-owner',
      ORGANIZATION_SLUG: 'owner',
      INSTANCE_ID: 'inst-1',
    })
    const { initPostHog, identifyUser, ph } = await load()
    initPostHog()
    identifyUser({ userId: 'u1', email: 'a@example.com', name: 'A', organizationId: 'org-other' })

    expect(ph.identify).toHaveBeenCalledWith('u1', { email: 'a@example.com', name: 'A' })
    expect(ph.group).toHaveBeenCalledWith('organization', 'org-owner', { slug: 'owner', name: undefined })
    expect(ph.group).toHaveBeenCalledWith('instance', 'inst-1', { organization_id: 'org-owner' })
  })
})

// Shared browsers: a session that expires or turns out invalid bounces to
// sign-in without an explicit sign-out. The next visitor must not be
// tagged as the previous user, but anonymous visitors keep their id so a
// signed-out visit still links up on sign-in.
describe('resetIfIdentified', () => {
  afterEach(() => vi.clearAllMocks())

  it('resets an identified browser that is no longer signed in', async () => {
    setRuntimeEnv({ POSTHOG_KEY: 'phc_x', POSTHOG_HOST: 'https://ph.everstack.ai' })
    const { initPostHog, resetIfIdentified, ph } = await load()
    initPostHog()
    ph.get_property.mockReturnValue('identified')
    resetIfIdentified()
    expect(ph.reset).toHaveBeenCalledTimes(1)
  })

  it('leaves an anonymous visitor alone', async () => {
    setRuntimeEnv({ POSTHOG_KEY: 'phc_x', POSTHOG_HOST: 'https://ph.everstack.ai' })
    const { initPostHog, resetIfIdentified, ph } = await load()
    initPostHog()
    ph.get_property.mockReturnValue('anonymous')
    resetIfIdentified()
    expect(ph.reset).not.toHaveBeenCalled()
  })
})
