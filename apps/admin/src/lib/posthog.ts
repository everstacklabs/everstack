import posthog from 'posthog-js'
import { env } from '@/env'
import { sanitizeCaptureResult, scrubPersistedProps } from '@/lib/analytics-sanitize'
import {
  REPLAY_BLOCK_SELECTOR,
  REPLAY_MASK_TEXT_SELECTOR,
  stripNetworkPayload,
} from '@/lib/replay-privacy'

let initialized = false

type RuntimeEnv = Record<string, string | undefined>

function runtimeEnv(): RuntimeEnv {
  if (typeof window === 'undefined') return {}
  return (window as { __env?: RuntimeEnv }).__env ?? {}
}

export type PostHogConfig = { key: string; host: string }

/**
 * Decides whether this console records analytics, and where to.
 *
 * The decision is the server's. The gateway injects POSTHOG_KEY and
 * POSTHOG_HOST into `window.__env` only for a signed-in user, and only
 * when its operator configured both (our helm chart does so for managed
 * cloud instances). The console is embedded in every gateway binary,
 * self-hosted and CE included, so a key baked in at build time would make
 * every install phone home: build-time VITE_ values are honoured in the
 * Vite dev server only. There is no default host for the same reason.
 */
export function resolvePostHogConfig(
  runtime: RuntimeEnv,
  build: { key?: string; host?: string },
  isDev: boolean,
): PostHogConfig | null {
  const key = runtime.POSTHOG_KEY || (isDev ? build.key : undefined)
  const host = runtime.POSTHOG_HOST || (isDev ? build.host : undefined)
  if (!key || !host) return null
  return { key, host }
}

export function initPostHog(): void {
  if (initialized) return
  if (typeof window === 'undefined') return

  const config = resolvePostHogConfig(
    runtimeEnv(),
    { key: env.VITE_POSTHOG_KEY, host: env.VITE_POSTHOG_HOST },
    import.meta.env.DEV,
  )
  if (!config) return

  posthog.init(config.key, {
    api_host: config.host,
    ui_host: 'https://eu.posthog.com',
    defaults: '2026-01-30',
    person_profiles: 'identified_only',
    capture_pageview: false,
    capture_pageleave: true,
    // The instance console renders prompts, completions, trace payloads and
    // API keys as page text. Autocapture keeps the click events but never
    // their text or attribute values. Session replay records the page with
    // every input masked, the text of secret and customer-content renderers
    // masked, canvases and iframes blocked, and no network headers or
    // bodies. See replay-privacy.ts. Exceptions are not captured: their
    // messages can echo prompts and there is no reliable scrubber.
    autocapture: true,
    mask_all_text: true,
    mask_all_element_attributes: true,
    disable_session_recording: false,
    session_recording: {
      maskAllInputs: true,
      maskTextSelector: REPLAY_MASK_TEXT_SELECTOR,
      blockSelector: REPLAY_BLOCK_SELECTOR,
      captureCanvas: { recordCanvas: false },
      recordCrossOriginIframes: false,
      recordHeaders: false,
      recordBody: false,
      maskCapturedNetworkRequestFn: stripNetworkPayload,
    },
    capture_exceptions: false,
    // Console output includes workflow stream events and API errors; the
    // shared project's remote setting must not turn it on here.
    enable_recording_console_log: false,
    // Strips query strings, hashes and link hrefs from every event,
    // including replay batches. See analytics-sanitize.ts.
    before_send: sanitizeCaptureResult,
    // Browser storage stays on this host and holds no URL beyond its path.
    // The default cookie persistence wrote the raw landing URL (invite token,
    // handoff codes) into a year-long cookie on everstack.ai, sent to every
    // other instance and readable by script. localStorage is per origin and
    // never sent to a server; nothing is shared across subdomains; the
    // referrer and campaign capture that persist the initial URL are off,
    // and what posthog-js still persists is scrubbed (scrubPersistence).
    persistence: 'localStorage',
    cross_subdomain_cookie: false,
    save_referrer: false,
    save_campaign_params: false,
    loaded: (ph) => {
      if (import.meta.env.DEV) {
        ph.debug(false)
      }
      ph.onSessionId(() => scrubPersistence(posthog))
      ph.on('eventCaptured', () => scrubPersistence(posthog))
    },
  })

  posthog.register({ surface: 'admin', app_env: detectAppEnv() })

  initialized = true
}

/** Rewrites persisted posthog-js state so it holds no query string or path token. */
function scrubPersistence(ph: typeof posthog): void {
  for (const store of [ph.persistence, ph.sessionPersistence]) {
    if (!store?.props) continue
    const next = scrubPersistedProps(store.props)
    if (next) {
      store.props = next
      store.save()
    }
  }
}

function detectAppEnv(): 'production' | 'development' | 'preview' {
  if (import.meta.env.DEV) return 'development'
  const host = window.location.hostname
  if (host.endsWith('.everstack.ai')) return 'production'
  return 'preview'
}

export type AnalyticsGroups = {
  organization?: { id: string; slug?: string; name?: string }
  instance?: { id: string; organizationId?: string }
}

/**
 * The PostHog groups events are attributed to. On a managed instance the
 * gateway provides the instance and its owning organization from the
 * verified host scope; the organization id is the one Cloud groups by, so
 * both consoles land on the same organization. The session's first
 * organization is only a fallback (self-hosted): a user in several
 * organizations may be using an instance owned by any of them.
 */
export function resolveAnalyticsGroups(
  runtime: RuntimeEnv,
  sessionOrg?: { id?: string; slug?: string; name?: string },
): AnalyticsGroups {
  const groups: AnalyticsGroups = {}
  const orgId = runtime.ORGANIZATION_ID || sessionOrg?.id
  if (orgId) {
    const fromSession = sessionOrg?.id === orgId
    groups.organization = {
      id: orgId,
      slug: runtime.ORGANIZATION_SLUG || (fromSession ? sessionOrg?.slug : undefined),
      name: fromSession ? sessionOrg?.name : undefined,
    }
  }
  if (runtime.INSTANCE_ID) {
    groups.instance = { id: runtime.INSTANCE_ID, organizationId: orgId }
  }
  return groups
}

export function identifyUser(args: {
  userId: string
  email?: string
  name?: string
  organizationId?: string
  organizationSlug?: string
  organizationName?: string
}): void {
  if (!initialized) return
  posthog.identify(args.userId, {
    email: args.email,
    name: args.name,
  })
  const groups = resolveAnalyticsGroups(runtimeEnv(), {
    id: args.organizationId,
    slug: args.organizationSlug,
    name: args.organizationName,
  })
  if (groups.organization) {
    posthog.group('organization', groups.organization.id, {
      slug: groups.organization.slug,
      name: groups.organization.name,
    })
  }
  if (groups.instance) {
    posthog.group('instance', groups.instance.id, {
      organization_id: groups.instance.organizationId,
    })
  }
}

/**
 * For a browser found signed out without an explicit sign-out (expired or
 * revoked session, the bounce to sign-in): forget the previous user so the
 * next visitor on a shared browser is not tagged as them. Anonymous
 * visitors keep their id, so a signed-out visit still links up on sign-in.
 */
export function resetIfIdentified(): void {
  if (!initialized) return
  if (posthog.get_property('$user_state') === 'identified') posthog.reset()
}

export function resetPostHog(): void {
  if (!initialized) return
  posthog.reset()
}

export function capturePageview(path: string): void {
  if (!initialized) return
  posthog.capture('$pageview', { $current_url: window.location.origin + path })
}

export function captureEvent(event: string, props?: Record<string, unknown>): void {
  if (!initialized) return
  posthog.capture(event, props)
}

export { posthog }
