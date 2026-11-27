import { describe, expect, it } from 'vitest'
import type { CaptureResult } from 'posthog-js'
import { sanitizeCaptureResult, stripUrl } from './analytics-sanitize'

const event = (e: Partial<CaptureResult>): CaptureResult =>
  ({ uuid: 'u', event: '$pageview', properties: {}, ...e }) as CaptureResult

describe('stripUrl', () => {
  it('keeps origin and path, drops query and hash', () => {
    expect(stripUrl('https://i.everstack.ai/observability/traces?q=input%3A%22my+secret%22#row-3')).toBe(
      'https://i.everstack.ai/observability/traces',
    )
    expect(stripUrl('/observability/logs?query=password')).toBe('/observability/logs')
    expect(stripUrl('/settings')).toBe('/settings')
  })

  // /invite/$token is the only admin route with a secret in the path.
  it('redacts tokens carried in the path', () => {
    expect(stripUrl('https://i.everstack.ai/invite/inv_9f8e7d6c5b?x=1')).toBe('https://i.everstack.ai/invite/:token')
    expect(stripUrl('/invite/inv_9f8e7d6c5b/')).toBe('/invite/:token/')
    expect(stripUrl('/invite')).toBe('/invite')
    expect(stripUrl('/settings/invites')).toBe('/settings/invites')
  })

  it('leaves non-URL text alone', () => {
    expect(stripUrl('hello?')).toBe('hello?')
    expect(stripUrl('')).toBe('')
  })
})

describe('sanitizeCaptureResult', () => {
  it('strips query strings from every URL property, including person properties', () => {
    const out = sanitizeCaptureResult(
      event({
        properties: {
          $current_url: 'https://i.everstack.ai/observability/traces?q=prompt+text',
          $pathname: '/observability/traces',
          $referrer: 'https://app.everstack.ai/acme?search=x',
          $initial_current_url: 'https://i.everstack.ai/?token=abc',
          $prev_pageview_pathname: '/logs?query=y',
          $external_click_url: 'https://docs.example.com/page?key=sk-1',
          $web_vitals_LCP_event: { $current_url: 'https://i.everstack.ai/x?q=z' },
          surface: 'admin',
        },
        $set: { $current_url: 'https://i.everstack.ai/a?q=1' },
        $set_once: { $initial_referrer: 'https://x.example/?q=2', $initial_current_url: 'https://i.everstack.ai/?q=3' },
      }),
    )!
    const json = JSON.stringify(out)
    expect(json).not.toMatch(/\?|prompt|token|sk-1|search=|query=/)
    expect(out.properties.$current_url).toBe('https://i.everstack.ai/observability/traces')
    expect(out.properties.$prev_pageview_pathname).toBe('/logs')
    expect(out.properties.surface).toBe('admin')
  })

  it('removes link hrefs from autocapture, in both element formats', () => {
    const out = sanitizeCaptureResult(
      event({
        event: '$autocapture',
        properties: {
          $elements: [{ tag_name: 'a', attr__href: '/observability/traces?q=secret', href: '/observability/traces?q=secret', nth_child: 1 }],
          $elements_chain:
            'a.link:attr__class="link"attr__href="/observability/traces?q=se\\"cret"href="/observability/traces?q=secret"nth-child="1";div:nth-child="2"',
        },
      }),
    )!
    expect(JSON.stringify(out)).not.toMatch(/secret|href/)
    expect(out.properties.$elements[0]).toEqual({ tag_name: 'a', nth_child: 1 })
    expect(out.properties.$elements_chain).toBe('a.link:attr__class="link"nth-child="1";div:nth-child="2"')
  })

  it('re-keys heatmap data by path-only URL', () => {
    const out = sanitizeCaptureResult(
      event({ event: '$$heatmap', properties: { $heatmap_data: { 'https://i.everstack.ai/logs?q=x': [{ x: 1 }] } } }),
    )!
    expect(out.properties.$heatmap_data).toEqual({ 'https://i.everstack.ai/logs': [{ x: 1 }] })
  })

  it('strips the page URL from replay meta events', () => {
    const out = sanitizeCaptureResult(
      event({
        event: '$snapshot',
        properties: {
          $snapshot_data: [
            { type: 4, data: { href: 'https://i.everstack.ai/observability/logs?query=secret', width: 1, height: 1 } },
            { type: 2, data: 'compressed' },
          ],
        },
      }),
    )!
    expect(out.properties.$snapshot_data[0].data.href).toBe('https://i.everstack.ai/observability/logs')
    expect(out.properties.$snapshot_data[1]).toEqual({ type: 2, data: 'compressed' })
  })

  it('redacts invite tokens from pageview and replay URLs', () => {
    const page = sanitizeCaptureResult(
      event({ properties: { $current_url: 'https://i.everstack.ai/invite/inv_secret', $pathname: '/invite/inv_secret' } }),
    )!
    expect(JSON.stringify(page)).not.toContain('inv_secret')
    const replay = sanitizeCaptureResult(
      event({
        event: '$snapshot',
        properties: { $snapshot_data: [{ type: 4, data: { href: 'https://i.everstack.ai/invite/inv_secret' } }] },
      }),
    )!
    expect(JSON.stringify(replay)).not.toContain('inv_secret')
  })

  it('sanitizes $identify events and their person properties', () => {
    const out = sanitizeCaptureResult(
      event({
        event: '$identify',
        properties: {
          $current_url: 'https://i.everstack.ai/invite/inv_secret?code=DEV-1',
          $session_entry_url: 'https://i.everstack.ai/invite/inv_secret?code=DEV-1',
          $anon_distinct_id: 'anon-1',
        },
        $set: { $current_url: 'https://i.everstack.ai/a?q=1', email: 'a@example.com' },
        $set_once: { $initial_current_url: 'https://i.everstack.ai/invite/inv_secret?code=DEV-1', $initial_pathname: '/invite/inv_secret' },
      }),
    )!
    const json = JSON.stringify(out)
    expect(json).not.toMatch(/inv_secret|DEV-1|\?q=/)
    expect(out.properties.$anon_distinct_id).toBe('anon-1')
    expect(out.$set?.email).toBe('a@example.com')
  })

  it('sanitizes person properties on replay batches too', () => {
    const out = sanitizeCaptureResult(
      event({
        event: '$snapshot',
        properties: { $snapshot_data: [] },
        $set: { $current_url: 'https://i.everstack.ai/a?q=1' },
        $set_once: { $initial_current_url: 'https://i.everstack.ai/invite/inv_secret?code=DEV-1' },
      }),
    )!
    expect(JSON.stringify(out)).not.toMatch(/inv_secret|DEV-1|\?q=/)
  })

  it('passes null through', () => {
    expect(sanitizeCaptureResult(null)).toBeNull()
  })
})
