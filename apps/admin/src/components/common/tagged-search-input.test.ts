import { describe, expect, it } from 'vitest'
import { derivePills, buildSuggestions } from './tagged-search-input'
import type { EvsQueryField } from '@/utils/evs-query'

const FIELDS: EvsQueryField[] = [
    { id: 'query', label: 'All Text', token: 'text:', searchKey: 'query' },
    {
        id: 'status',
        label: 'Status',
        token: 'status:',
        searchKey: 'statusCode',
        aliases: ['statusCode'],
    },
    { id: 'model', label: 'Model', token: 'model:', searchKey: 'model' },
    { id: 'provider', label: 'Provider', token: 'provider:', searchKey: 'provider' },
    {
        id: 'traceId',
        label: 'Trace ID',
        token: 'traceId:',
        searchKey: 'trace',
        clearKeys: ['span'],
    },
    { id: 'tags', label: 'Tag', token: 'tag:', searchKey: 'tags' },
    { id: 'metadata', label: 'Metadata', token: 'metadata:', searchKey: 'metadata' },
    { id: 'minCost', label: 'Min Cost', token: 'costMin:', searchKey: 'minCost' },
    {
        id: 'minDuration',
        label: 'Min Duration',
        token: 'durationMin:',
        searchKey: 'minDuration',
    },
]

describe('derivePills', () => {
    it('excludes free-text query and renders scalar facets', () => {
        const pills = derivePills(
            { query: 'hello world', model: 'gpt-4o', provider: 'openai' },
            FIELDS,
        )
        expect(pills.map((p) => `${p.prefix}${p.value}`)).toEqual([
            'model:gpt-4o',
            'provider:openai',
        ])
    })

    it('tones status pills by value', () => {
        expect(derivePills({ statusCode: 'ERROR' }, FIELDS)[0].tone).toBe('error')
        expect(derivePills({ statusCode: 'OK' }, FIELDS)[0].tone).toBe('ok')
    })

    it('emits one pill per tag and removes just that tag', () => {
        const pills = derivePills({ tags: 'prod,customer,beta' }, FIELDS)
        expect(pills).toHaveLength(3)
        const next = pills[1].remove({ tags: 'prod,customer,beta' })
        expect(next.tags).toBe('prod,beta')
    })

    it('drops the param entirely when the last tag is removed', () => {
        const pills = derivePills({ tags: 'only' }, FIELDS)
        expect(pills[0].remove({ tags: 'only' }).tags).toBeUndefined()
    })

    it('renders metadata as @key:value and removes by exact entry', () => {
        const pills = derivePills({ metadata: 'plan=pro,region=us-east' }, FIELDS)
        expect(pills.map((p) => `${p.prefix}${p.value}`)).toEqual([
            '@plan:pro',
            '@region:us-east',
        ])
        expect(
            pills[0].remove({ metadata: 'plan=pro,region=us-east' }).metadata,
        ).toBe('region=us-east')
    })

    it('formats cost and duration range pills', () => {
        const pills = derivePills(
            { minCost: '0.01', minDuration: '1500' },
            FIELDS,
        )
        const rendered = pills.map((p) => `${p.prefix} ${p.value}`)
        expect(rendered).toContain('cost ≥ $0.01')
        expect(rendered).toContain('duration ≥ 1.5s')
    })

    it('carries clearKeys so removing a trace pill also clears span', () => {
        const pill = derivePills({ trace: 'tr_123' }, FIELDS)[0]
        expect(pill.clearKeys).toEqual(['span'])
    })

    it('produces a re-editable canonical token', () => {
        const pill = derivePills({ model: 'gpt 4o' }, FIELDS)[0]
        expect(pill.editToken).toBe('model:"gpt 4o"')
    })
})

describe('buildSuggestions', () => {
    it('suggests matching facet keys during the key stage', () => {
        const labels = buildSuggestions('mod', FIELDS).map((s) => s.label)
        expect(labels).toContain('model:')
        expect(labels.every((l) => l.endsWith(':'))).toBe(true)
    })

    it('suggests known enum values after a key and commits on select', () => {
        const suggestions = buildSuggestions('status:e', FIELDS)
        expect(suggestions).toHaveLength(1)
        expect(suggestions[0].label).toBe('ERROR')
        expect(suggestions[0].commitOnSelect).toBe(true)
        expect(suggestions[0].insert).toBe('status:ERROR ')
    })

    it('returns no value suggestions for free-value fields', () => {
        expect(buildSuggestions('model:gpt', FIELDS)).toEqual([])
    })

    it('only rewrites the trailing fragment, preserving prior text', () => {
        const [first] = buildSuggestions('status:ok mod', FIELDS)
        expect(first.insert).toBe('status:ok model:')
    })
})
