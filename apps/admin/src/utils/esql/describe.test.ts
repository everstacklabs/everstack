import { describe, expect, it } from 'vitest'
import { describeEsql } from './describe'
import { parseEsql } from './parse'

function say(input: string): string {
    const parsed = parseEsql(input)
    if (!parsed.ok) throw new Error(parsed.errors.join(', '))
    return describeEsql(parsed.query)
}

describe('describeEsql', () => {
    it('renders an empty query as the full range', () => {
        expect(say('')).toBe('All traces in range.')
    })

    it('renders a single clause', () => {
        expect(say('status:error')).toBe('Traces that failed.')
    })

    it('joins clauses with commas and a trailing "and"', () => {
        expect(say('failed any.model:gpt-5.2 tool.error exists cost > 0.05')).toBe(
            'Traces that failed, used gpt-5.2, hit a tool error, and cost over $0.05.',
        )
    })

    it('humanises durations and free text', () => {
        expect(say('duration > 30s "refund"')).toBe(
            'Traces that slower than 30s and mentioning “refund”.',
        )
    })

    it('notes root scope', () => {
        expect(say('root.model:gpt-4o')).toBe('Traces that used gpt-4o (root span).')
    })
})
