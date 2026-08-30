/**
 * trace-search: in-trace span search (find a span within one large trace).
 *
 * The trace detail renders a Search box; this is the matching logic behind it.
 * A query matches a span anywhere in its content (title, name, status, category,
 * and every attribute key/value), and the tree is filtered to matches plus their
 * ancestors so the path to each hit stays visible. Pure and dependency-light so
 * it is trivially testable, mirroring trace-tree-flatten.
 */

import type { Span, SpanTreeNode } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getSpanDisplayConfig } from './span-title-name-map'
import { categoryLabels } from './span-display-helpers'

/**
 * True when a span matches the in-trace search query. Case-insensitive substring
 * over the span's display title/subtitle, raw name, status, category label, and
 * every attribute key/value, so a match can be found anywhere in the span rather
 * than only in its title. An empty query matches everything.
 */
export function spanMatchesQuery(span: Span, query: string): boolean {
  const needle = query.trim().toLowerCase()
  if (!needle) return true
  const cfg = getSpanDisplayConfig(span)
  const fields: Array<string | undefined> = [
    cfg.title,
    cfg.subtitle,
    span.spanName,
    span.statusCode,
    categoryLabels[cfg.category],
  ]
  for (const f of fields) {
    if (f && f.toLowerCase().includes(needle)) return true
  }
  const attrs = span.spanAttributes
  if (attrs) {
    for (const key in attrs) {
      if (key.toLowerCase().includes(needle)) return true
      const val = attrs[key]
      if (val && val.toLowerCase().includes(needle)) return true
    }
  }
  return false
}

/**
 * Walk the span tree for an in-trace search: collect the spans that match the
 * query (`matchIds`) plus every ancestor of a match (`visibleIds`), so the path
 * to each hit stays visible. Returns empty sets for an empty query or missing
 * tree.
 */
export function collectSearchMatches(
  root: SpanTreeNode | null | undefined,
  query: string,
): { matchIds: Set<string>; visibleIds: Set<string> } {
  const matchIds = new Set<string>()
  const visibleIds = new Set<string>()
  const q = query.trim()
  if (!q || !root) return { matchIds, visibleIds }
  const walk = (node: SpanTreeNode): boolean => {
    let childVisible = false
    for (const child of node.children ?? []) {
      if (walk(child)) childVisible = true
    }
    const id = node.span?.spanId
    const selfMatch = !!(node.span && spanMatchesQuery(node.span, q))
    if (id && selfMatch) matchIds.add(id)
    if (selfMatch || childVisible) {
      if (id) visibleIds.add(id)
      return true
    }
    return false
  }
  walk(root)
  return { matchIds, visibleIds }
}
