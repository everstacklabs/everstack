import type { EsqlNode, EsqlQuery } from './ast'
import { PRESETS } from './presets'

/**
 * Render a parsed ESQL query as a read-only plain-English "Matches:" line.
 * This is an explanation aid shown under the editor, never an input.
 */

function formatMs(raw: string | number): string {
    const ms = Number(raw)
    if (!Number.isFinite(ms)) return `${raw}ms`
    if (ms >= 60_000) return `${+(ms / 60_000).toFixed(2)}m`
    if (ms >= 1_000) return `${+(ms / 1_000).toFixed(2)}s`
    return `${ms}ms`
}

function describePredicate(node: Extract<EsqlNode, { kind: 'predicate' }>): string {
    const v = String(node.value)
    const gt = node.op === '>' || node.op === '>='
    switch (node.field) {
        case 'status':
            return v.toUpperCase() === 'ERROR' ? 'failed' : v.toUpperCase() === 'OK' ? 'succeeded' : `status ${v}`
        case 'model':
            return `used ${v}`
        case 'provider':
            return `on ${v}`
        case 'cost':
            return `cost ${gt ? 'over' : 'under'} $${v}`
        case 'duration':
            return `${gt ? 'slower' : 'faster'} than ${formatMs(v)}`
        case 'ttft':
            return `time-to-first-token ${gt ? 'over' : 'under'} ${formatMs(v)}`
        case 'tokens.total':
            return `${gt ? 'over' : 'under'} ${v} tokens`
        case 'tool.name':
            return `called tool ${v}`
        case 'user':
            return `user ${v}`
        case 'session':
            return `session ${v}`
        case 'thread':
            return `thread ${v}`
        case 'environment':
            return `in ${v}`
        case 'correlation':
            return `correlation ${v}`
        case 'tag':
            return `tagged ${v}`
        case 'query':
        case 'output':
            return `mentioning “${v}”`
        default:
            if (node.field.startsWith('metadata.')) return `${node.field.slice('metadata.'.length)} = ${v}`
            return `${node.field} ${node.op} ${v}`
    }
}

function describeExists(node: Extract<EsqlNode, { kind: 'exists' }>): string {
    switch (node.field) {
        case 'tool.error':
            return 'hit a tool error'
        case 'cache.hit':
            return 'hit cache'
        default:
            return `has ${node.field}`
    }
}

function describeNode(node: EsqlNode): string {
    switch (node.kind) {
        case 'text':
            return `mentioning “${node.value}”`
        case 'preset':
            return (PRESETS[node.id]?.label ?? node.id).toLowerCase()
        case 'predicate': {
            const base = describePredicate(node)
            return node.scope === 'root' ? `${base} (root span)` : base
        }
        case 'exists':
            return describeExists(node)
        case 'sequence':
            return `ran the sequence ${node.steps.map((s) => s.value).join(' → ')}`
        default:
            return ''
    }
}

function joinClauses(parts: string[]): string {
    const clean = parts.filter(Boolean)
    if (clean.length === 0) return ''
    if (clean.length === 1) return clean[0]
    if (clean.length === 2) return `${clean[0]} and ${clean[1]}`
    return `${clean.slice(0, -1).join(', ')}, and ${clean[clean.length - 1]}`
}

export function describeEsql(query: EsqlQuery): string {
    const clauses = query.nodes.map(describeNode)
    const body = joinClauses(clauses)
    return body ? `Traces that ${body}.` : 'All traces in range.'
}
