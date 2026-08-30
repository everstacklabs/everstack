import type { EsqlNode } from './ast'
import { compileToListTracesParams } from './compile'
import { parseEsql } from './parse'

/**
 * Bridge ESQL <-> the traces route's flat search params. Both the filter bar
 * and the saved-queries page compile a query the same way through here.
 */

// Route search-param keys ESQL owns; cleared and rewritten on every apply.
export const ESQL_MANAGED_KEYS = [
    'query',
    'statusCode',
    'model',
    'provider',
    'userId',
    'sessionId',
    'threadId',
    'environment',
    'correlationId',
    'tags',
    'metadata',
    'minCost',
    'maxCost',
    'minDuration',
    'maxDuration',
    'queries', // legacy JSON-chip param from the old structured filter bar
] as const

function toSearchPatch(params: Record<string, unknown>): Record<string, string | undefined> {
    const patch: Record<string, string | undefined> = {}
    const str = (v: unknown) => (v == null ? undefined : String(v))
    const csv = (v: unknown) => (Array.isArray(v) && v.length ? v.join(',') : undefined)

    patch.query = str(params.query)
    patch.statusCode = str(params.statusCode)
    patch.model = str(params.model)
    patch.provider = str(params.provider)
    patch.userId = str(params.userId)
    patch.sessionId = str(params.sessionId)
    patch.threadId = str(params.threadId)
    patch.environment = str(params.environment)
    patch.correlationId = str(params.correlationId)
    patch.tags = csv(params.tags)
    patch.metadata = csv(params.metadata)
    patch.minCost = str(params.minCost)
    patch.maxCost = str(params.maxCost)
    patch.minDuration = str(params.minDurationMs)
    patch.maxDuration = str(params.maxDurationMs)
    return patch
}

export type EsqlSearchResult = {
    ok: boolean
    params: Record<string, string | undefined>
    unsupported: EsqlNode[]
    error: string | null
}

/** Parse + compile an ESQL string into the route's flat search-param shape. */
export function esqlToSearchParams(esql: string): EsqlSearchResult {
    const value = esql.trim()
    if (!value) return { ok: true, params: {}, unsupported: [], error: null }

    const parsed = parseEsql(value)
    if (!parsed.ok) {
        return { ok: false, params: {}, unsupported: [], error: parsed.errors[0] ?? 'Invalid filter' }
    }
    const { params, unsupported } = compileToListTracesParams(parsed.query)
    return { ok: true, params: toSearchPatch(params), unsupported, error: null }
}

/** A search patch that clears every ESQL-managed key (for a clean re-apply). */
export function clearedEsqlParams(): Record<string, undefined> {
    return Object.fromEntries(ESQL_MANAGED_KEYS.map((k) => [k, undefined]))
}
