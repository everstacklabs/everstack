import posthog from 'posthog-js'
import { env } from '@/env'

let initialized = false

export function initPostHog(): void {
  if (initialized) return
  if (typeof window === 'undefined') return

  const runtime = (window as { __env?: Record<string, string> }).__env ?? {}
  const key = runtime.POSTHOG_KEY || env.VITE_POSTHOG_KEY
  if (!key) return

  const host = runtime.POSTHOG_HOST || env.VITE_POSTHOG_HOST || 'https://ph.everstack.ai'

  posthog.init(key, {
    api_host: host,
    ui_host: 'https://eu.posthog.com',
    defaults: '2026-01-30',
    person_profiles: 'identified_only',
    capture_pageview: false,
    capture_pageleave: true,
    autocapture: true,
    session_recording: {
      maskAllInputs: true,
      maskTextSelector: '.ph-mask',
      recordCrossOriginIframes: false,
    },
    disable_session_recording: false,
    loaded: (ph) => {
      if (import.meta.env.DEV) {
        ph.debug(false)
      }
    },
  })

  posthog.register({ surface: 'admin', app_env: detectAppEnv() })

  initialized = true
}

function detectAppEnv(): 'production' | 'development' | 'preview' {
  if (import.meta.env.DEV) return 'development'
  const host = window.location.hostname
  if (host.endsWith('.everstack.ai')) return 'production'
  return 'preview'
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
  if (args.organizationId) {
    posthog.group('organization', args.organizationId, {
      slug: args.organizationSlug,
      name: args.organizationName,
    })
  }
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
