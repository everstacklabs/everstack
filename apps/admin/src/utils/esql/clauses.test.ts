import { describe, expect, it } from 'vitest'
import { compileToClauses, type EsqlClause } from './clauses'
import { parseEsql } from './parse'

function clausesOf(input: string): { clauses: EsqlClause[]; unsupportedFields: string[] } {
    const parsed = parseEsql(input)
    if (!parsed.ok) throw new Error(parsed.errors.join(', '))
    const { clauses, unsupported } = compileToClauses(parsed.query)
    return {
        clauses,
        unsupportedFields: unsupported.map((n) => ('field' in n ? n.field : n.kind)),
    }
}

describe('compileToClauses', () => {
    it('emits clauses for span-scoped and non-flat fields', () => {
        const { clauses } = clausesOf('root.status:error tool.name:db.fetch tokens.total > 15000')
        expect(clauses).toEqual([
            { scope: 'root', field: 'status', op: '=', value: 'ERROR' },
            { scope: 'any', field: 'tool.name', op: '=', value: 'db.fetch' },
            { scope: 'any', field: 'tokens.total', op: '>', value: '15000' },
        ])
    })

    it('does not clause-ify flat Tier-1 fields (no double filtering)', () => {
        // bare model / status map to flat params, so they must NOT appear as clauses
        const { clauses } = clausesOf('model:gpt-4o status:ok cost > 0.1')
        expect(clauses).toEqual([])
    })

    it('emits exists + numeric clauses for tool.error / cache.hit / ttft', () => {
        const { clauses } = clausesOf('tool.error exists cache.hit exists ttft > 5s')
        expect(clauses).toEqual([
            { scope: 'any', field: 'tool.error', op: 'exists', value: '' },
            { scope: 'any', field: 'cache.hit', op: 'exists', value: '' },
            { scope: 'any', field: 'ttft', op: '>', value: '5000' },
        ])
    })

    it('leaves genuinely unsupported fields (output full-text) in unsupported', () => {
        const { clauses, unsupportedFields } = clausesOf('output contains "refund"')
        // output contains folds into flat query, so nothing here is a clause…
        expect(clauses).toEqual([])
        expect(unsupportedFields).toEqual([])
    })
})
