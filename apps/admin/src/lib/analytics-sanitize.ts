import type { CaptureResult } from 'posthog-js'

/**
 * Last line of defence before an event leaves the browser (posthog-js
 * `before_send`, which also runs for replay `$snapshot` batches).
 *
 * - URLs keep only origin and path, with path tokens redacted. The traces, logs, issues and outcomes
 *   pages put free-text ESQL and search terms in `q` and `query`, and a hash
 *   can hold anything, so no query string or hash is ever sent.
 * - Autocapture never sends link hrefs (`attr__href` / `href`), which carry
 *   the same query strings.
 */

const ABSOLUTE_URL = /^https?:\/\//i
const URL_KEY = /url|href|referrer|pathname/i
const MAX_DEPTH = 4

/**
 * Routes that carry a secret in the path itself. `/invite/$token` is the only
 * one in the admin; device codes and handoff codes travel in the query
 * string (stripped) or through server routes the SPA never renders.
 */
const SECRET_PATH_SEGMENTS: Array<[RegExp, string]> = [[/^\/invite\/[^/]+/, '/invite/:token']]

function redactPath(path: string): string {
  for (const [pattern, replacement] of SECRET_PATH_SEGMENTS) {
    if (pattern.test(path)) return path.replace(pattern, replacement)
  }
  return path
}

export function stripUrl(value: string): string {
  if (ABSOLUTE_URL.test(value)) {
    try {
      const url = new URL(value)
      return url.origin + redactPath(url.pathname)
    } catch {
      return value.split(/[?#]/, 1)[0]
    }
  }
  if (value.startsWith('/')) return redactPath(value.split(/[?#]/, 1)[0])
  return value
}

function sanitizeValue(key: string, value: unknown, depth: number): unknown {
  if (typeof value === 'string') {
    return URL_KEY.test(key) || ABSOLUTE_URL.test(value) ? stripUrl(value) : value
  }
  if (depth >= MAX_DEPTH || value === null || typeof value !== 'object') return value
  if (Array.isArray(value)) return value.map((item) => sanitizeValue(key, item, depth + 1))
  return sanitizeObject(value as Record<string, unknown>, depth + 1)
}

function sanitizeObject(obj: Record<string, unknown>, depth = 0): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(obj)) {
    out[key] = sanitizeValue(key, value, depth)
  }
  return out
}

/** `attr__href="..."` and `href="..."` in an autocapture elements chain. */
const CHAIN_HREF = /(?<=[:"])(?:attr__href|href)="(?:[^"\\]|\\.)*"/g

function sanitizeElements(elements: unknown): unknown {
  if (!Array.isArray(elements)) return elements
  return elements.map((el) => {
    if (!el || typeof el !== 'object') return el
    const { attr__href: _attrHref, href: _href, ...rest } = el as Record<string, unknown>
    return rest
  })
}

function sanitizeHeatmap(data: unknown): unknown {
  if (!data || typeof data !== 'object' || Array.isArray(data)) return data
  const out: Record<string, unknown> = {}
  for (const [url, points] of Object.entries(data as Record<string, unknown>)) {
    const key = stripUrl(url)
    const existing = out[key]
    out[key] = Array.isArray(existing) && Array.isArray(points) ? [...existing, ...points] : points
  }
  return out
}

/** rrweb Meta events (type 4) carry `window.location.href`. */
function sanitizeSnapshotData(data: unknown): unknown {
  if (!Array.isArray(data)) return data
  return data.map((evt) => {
    if (evt && typeof evt === 'object' && (evt as { type?: unknown }).type === 4) {
      const meta = evt as { data?: { href?: unknown } }
      if (meta.data && typeof meta.data.href === 'string') {
        return { ...meta, data: { ...meta.data, href: stripUrl(meta.data.href) } }
      }
    }
    return evt
  })
}

export function sanitizeCaptureResult(cr: CaptureResult | null): CaptureResult | null {
  if (!cr) return cr
  const props = (cr.properties ?? {}) as Record<string, unknown>

  if (cr.event === '$snapshot') {
    return withPersonProps(cr, {
      ...cr,
      properties: {
        ...props,
        $snapshot_data: sanitizeSnapshotData(props.$snapshot_data),
        ...('$current_url' in props && typeof props.$current_url === 'string'
          ? { $current_url: stripUrl(props.$current_url) }
          : {}),
      },
    })
  }

  const { $elements, $elements_chain, $heatmap_data, ...rest } = props
  const properties: Record<string, unknown> = sanitizeObject(rest)
  if ($elements !== undefined) properties.$elements = sanitizeElements($elements)
  if (typeof $elements_chain === 'string') properties.$elements_chain = $elements_chain.replace(CHAIN_HREF, '')
  if ($heatmap_data !== undefined) properties.$heatmap_data = sanitizeHeatmap($heatmap_data)

  return withPersonProps(cr, { ...cr, properties })
}

/** Person properties (`$set`, `$set_once`) on any event, replay batches included. */
function withPersonProps(cr: CaptureResult, result: CaptureResult): CaptureResult {
  if (cr.$set) result.$set = sanitizeObject(cr.$set)
  if (cr.$set_once) result.$set_once = sanitizeObject(cr.$set_once)
  return result
}

/**
 * posthog-js persists the landing URL in the browser (`$client_session_props`
 * holds `{ r: referrer, u: location.href }`, and `$initial_person_info` the
 * same) and replays it into events as `$session_entry_*`. Returns a copy of
 * the persisted props with every URL reduced to origin and redacted path,
 * or null when nothing needed changing.
 */
export function scrubPersistedProps(props: Record<string, unknown>): Record<string, unknown> | null {
  let changed = false
  const scrub = (key: string, value: unknown, depth: number): unknown => {
    if (typeof value === 'string') {
      const isUrlKey = key === 'u' || key === 'r' || URL_KEY.test(key)
      if (isUrlKey || ABSOLUTE_URL.test(value)) {
        const next = stripUrl(value)
        if (next !== value) changed = true
        return next
      }
      return value
    }
    if (depth >= MAX_DEPTH || value === null || typeof value !== 'object' || Array.isArray(value)) return value
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) out[k] = scrub(k, v, depth + 1)
    return out
  }
  const next = scrub('', props, 0) as Record<string, unknown>
  return changed ? next : null
}
