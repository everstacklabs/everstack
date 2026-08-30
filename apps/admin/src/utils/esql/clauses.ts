import type { EsqlNode, EsqlQuery } from './ast'
import { compileToListTracesParams } from './compile'

/**
 * Tier-2 transport: convert the AST nodes that can't map to flat list params
 * (span-scoped / EXISTS / tool.name / tokens.total) into structured clauses the
 * Go clause compiler executes as TraceId-membership subqueries. Fields the
 * backend does not yet handle stay in `unsupported` (they silently no-op today).
 */

export type EsqlClause = {
    scope: 'any' | 'root'
    field: string
    op: string // "=", "!=", ">", ">=", "<", "<=", "exists"
    value: string
}

// Fields the Go clause compiler resolves today (traceClauseFieldExpr + the
// bespoke tool.error / cache.hit exists conditions).
const CLAUSE_FIELDS = new Set([
    'trace',
    'has',
    'agent',
    'model',
    'provider',
    'user',
    'session',
    'thread',
    'correlation',
    'status',
    'tool.name',
    'tokens.total',
    'tool.error',
    'cache.hit',
    'ttft',
])

function isSupported(field: string): boolean {
    return field.startsWith('metadata.') || CLAUSE_FIELDS.has(field)
}

function nodeToClause(node: EsqlNode): EsqlClause | null {
    const scope = (n: EsqlNode & { scope: string }) => (n.scope === 'root' ? 'root' : 'any')

    if (node.kind === 'exists') {
        if (!isSupported(node.field)) return null
        return { scope: scope(node), field: node.field, op: 'exists', value: '' }
    }
    if (node.kind === 'predicate') {
        if (!isSupported(node.field)) return null
        const op = node.op === ':' ? '=' : node.op
        if (op === 'contains') return null // full-text, not a clause
        return { scope: scope(node), field: node.field, op, value: String(node.value) }
    }
    return null
}

export function compileToClauses(query: EsqlQuery): {
    clauses: EsqlClause[]
    unsupported: EsqlNode[]
} {
    // Reuse compile's unsupported set: exactly the nodes not expressible as flat
    // params (root scope, tool.name, tokens.total, exists, etc.).
    const { unsupported } = compileToListTracesParams(query)
    const clauses: EsqlClause[] = []
    const stillUnsupported: EsqlNode[] = []
    for (const node of unsupported) {
        const clause = nodeToClause(node)
        if (clause) clauses.push(clause)
        else stillUnsupported.push(node)
    }
    return { clauses, unsupported: stillUnsupported }
}
