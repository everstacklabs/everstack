import { describe, expect, it } from 'vitest'
import type { EsqlNode, EsqlQuery } from './ast'
import { compileToListTracesParams } from './compile'
import { findField } from './fields'
import { parseEsql } from './parse'
import { PRESETS } from './presets'
import { esqlFromLegacyParams, serializeEsql } from './serialize'

function parsedQuery(input: string): EsqlQuery {
  const parsed = parseEsql(input)
  expect(parsed).toMatchObject({ ok: true })
  if (!parsed.ok) throw new Error(parsed.errors.join(', '))
  return parsed.query
}

describe('parseEsql', () => {
  it('parses equality predicates with canonical fields and aliases', () => {
    expect(parsedQuery('status:ERROR model=gpt-4o user_id:u_123')).toEqual({
      nodes: [
        { kind: 'predicate', scope: 'any', field: 'status', op: ':', value: 'ERROR' },
        { kind: 'predicate', scope: 'any', field: 'model', op: ':', value: 'gpt-4o' },
        { kind: 'predicate', scope: 'any', field: 'user', op: ':', value: 'u_123' },
      ],
    })
  })

  it('parses contains clauses and quoted free text', () => {
    expect(parsedQuery('"checkout failed" output contains "refund issued"')).toEqual({
      nodes: [
        { kind: 'text', value: 'checkout failed' },
        {
          kind: 'predicate',
          scope: 'any',
          field: 'output',
          op: 'contains',
          value: 'refund issued',
        },
      ],
    })
  })

  it('parses numeric and duration comparisons with units', () => {
    expect(parsedQuery('cost > 0.05 duration > 30s tokens.total >= 15000 ttft < 5s')).toEqual({
      nodes: [
        { kind: 'predicate', scope: 'any', field: 'cost', op: '>', value: 0.05 },
        { kind: 'predicate', scope: 'any', field: 'duration', op: '>', value: 30_000 },
        {
          kind: 'predicate',
          scope: 'any',
          field: 'tokens.total',
          op: '>=',
          value: 15_000,
        },
        { kind: 'predicate', scope: 'any', field: 'ttft', op: '<', value: 5_000 },
      ],
    })
  })

  it('parses exists clauses', () => {
    expect(parsedQuery('tool.error exists cache.hit exists')).toEqual({
      nodes: [
        { kind: 'exists', scope: 'any', field: 'tool.error' },
        { kind: 'exists', scope: 'any', field: 'cache.hit' },
      ],
    })
  })

  it('parses scoped predicates', () => {
    expect(parsedQuery('root.model:gpt-4o any.provider:openai')).toEqual({
      nodes: [
        { kind: 'predicate', scope: 'root', field: 'model', op: ':', value: 'gpt-4o' },
        { kind: 'predicate', scope: 'any', field: 'provider', op: ':', value: 'openai' },
      ],
    })
  })

  it('parses preset keywords', () => {
    expect(parsedQuery('failed slow expensive no_output tool_error retry')).toEqual({
      nodes: [
        { kind: 'preset', id: 'failed' },
        { kind: 'preset', id: 'slow' },
        { kind: 'preset', id: 'expensive' },
        { kind: 'preset', id: 'no_output' },
        { kind: 'preset', id: 'tool_error' },
        { kind: 'preset', id: 'retry' },
      ],
    })
  })

  it('resolves parity aliases from the legacy structured-filter bar', () => {
    expect(parsedQuery('latency > 3s price > 1 status_code:error name contains checkout')).toEqual({
      nodes: [
        { kind: 'predicate', scope: 'any', field: 'duration', op: '>', value: 3_000 },
        { kind: 'predicate', scope: 'any', field: 'cost', op: '>', value: 1 },
        { kind: 'predicate', scope: 'any', field: 'status', op: ':', value: 'ERROR' },
        { kind: 'predicate', scope: 'any', field: 'query', op: 'contains', value: 'checkout' },
      ],
    })
  })

  it('parses metadata shorthand', () => {
    expect(parsedQuery('@campaign:summer')).toEqual({
      nodes: [
        {
          kind: 'predicate',
          scope: 'any',
          field: 'metadata.campaign',
          op: ':',
          value: 'summer',
        },
      ],
    })
  })

  it('accepts AND as a no-op', () => {
    expect(parsedQuery('status:ERROR AND provider:openai')).toEqual({
      nodes: [
        { kind: 'predicate', scope: 'any', field: 'status', op: ':', value: 'ERROR' },
        { kind: 'predicate', scope: 'any', field: 'provider', op: ':', value: 'openai' },
      ],
    })
  })

  it('returns validation errors for unknown fields, OR/NOT, and topology scopes', () => {
    expect(parseEsql('service:api')).toEqual({
      ok: false,
      errors: ['Unknown field: service'],
    })
    expect(parseEsql('status:ERROR OR provider:openai NOT model:gpt-4o')).toEqual({
      ok: false,
      errors: ['OR/NOT not supported in v1', 'OR/NOT not supported in v1'],
    })
    expect(parseEsql('parent.model:gpt-4o child.provider:openai')).toEqual({
      ok: false,
      errors: [
        'topology scope not supported yet',
        'topology scope not supported yet',
      ],
    })
  })

  it('returns validation errors for disallowed operators', () => {
    expect(parseEsql('model > gpt-4o tool.error:yes')).toEqual({
      ok: false,
      errors: [
        'Unsupported operator for model: >',
        'Unsupported operator for tool.error: :',
      ],
    })
  })
})

describe('serializeEsql', () => {
  it('round-trips representative queries', () => {
    const queries: EsqlQuery[] = [
      parsedQuery('checkout status:ERROR provider:openai cost > 0.10 duration <= 2s'),
      parsedQuery('output contains "refund issued" @campaign:summer tag:vip'),
      parsedQuery('tool.error exists failed'),
      {
        nodes: [
          { kind: 'predicate', scope: 'root', field: 'model', op: '=', value: 'gpt-4o' },
        ],
      },
    ]

    for (const query of queries) {
      const reparsed = parseEsql(serializeEsql(query))
      expect(reparsed).toEqual({ ok: true, query })
    }
  })
})

describe('esqlFromLegacyParams', () => {
  it('produces a string that parses and compiles to the equivalent flat params', () => {
    const esql = esqlFromLegacyParams({
      query: 'missing api key',
      statusCode: 'ERROR',
      model: 'gpt-4o',
      provider: 'openai',
      userId: 'u_123',
      sessionId: 's_123',
      threadId: 't_123',
      environment: 'prod',
      correlationId: 'c_123',
      tags: 'prod,vip',
      metadata: 'plan=pro,customer_tier=enterprise',
      minCost: 0.1,
      maxCost: 1.5,
      minDuration: 500,
      maxDuration: 60_000,
    })

    const parsed = parseEsql(esql)
    expect(parsed).toMatchObject({ ok: true })
    if (!parsed.ok) throw new Error(parsed.errors.join(', '))

    expect(compileToListTracesParams(parsed.query)).toEqual({
      params: {
        query: 'missing api key',
        statusCode: 'ERROR',
        model: 'gpt-4o',
        provider: 'openai',
        userId: 'u_123',
        sessionId: 's_123',
        threadId: 't_123',
        environment: 'prod',
        correlationId: 'c_123',
        tags: ['prod', 'vip'],
        metadata: ['plan=pro', 'customer_tier=enterprise'],
        minCost: 0.1,
        maxCost: 1.5,
        minDurationMs: 500,
        maxDurationMs: 60_000,
      },
      unsupported: [],
    })
  })
})

describe('compileToListTracesParams', () => {
  it('maps Tier 1 nodes to flat list-traces params', () => {
    const query = parsedQuery(
      'checkout failed slow expensive status:ok model:gpt-4o provider:openai user:u session:s thread:t environment:prod correlation:c tag:vip @plan:pro output contains refund duration < 1m cost <= 2',
    )

    expect(compileToListTracesParams(query)).toEqual({
      params: {
        query: 'checkout refund',
        statusCode: 'OK',
        minDurationMs: 30_000,
        minCost: 0.1,
        model: 'gpt-4o',
        provider: 'openai',
        userId: 'u',
        sessionId: 's',
        threadId: 't',
        environment: 'prod',
        correlationId: 'c',
        tags: ['vip'],
        metadata: ['plan=pro'],
        maxDurationMs: 60_000,
        maxCost: 2,
      },
      unsupported: [],
    })
  })

  it('places Phase 2 nodes in unsupported', () => {
    const unsupportedNodes: EsqlNode[] = [
      { kind: 'predicate', scope: 'any', field: 'tool.name', op: ':', value: 'db.fetch' },
      { kind: 'exists', scope: 'any', field: 'tool.error' },
      { kind: 'exists', scope: 'any', field: 'cache.hit' },
      { kind: 'predicate', scope: 'any', field: 'ttft', op: '>', value: 5_000 },
      { kind: 'predicate', scope: 'any', field: 'tokens.total', op: '>', value: 15_000 },
      { kind: 'predicate', scope: 'root', field: 'model', op: ':', value: 'gpt-4o' },
    ]

    expect(compileToListTracesParams({ nodes: unsupportedNodes })).toEqual({
      params: {},
      unsupported: unsupportedNodes,
    })
  })
})

describe('presets', () => {
  it('expands Tier 1 presets into compilable params', () => {
    expect(compileToListTracesParams(parsedQuery('failed slow expensive'))).toEqual({
      params: {
        statusCode: 'ERROR',
        minDurationMs: 30_000,
        minCost: 0.1,
      },
      unsupported: [],
    })
  })

  it('leaves tool_error in unsupported', () => {
    expect(compileToListTracesParams(parsedQuery('tool_error'))).toEqual({
      params: {},
      unsupported: [{ kind: 'preset', id: 'tool_error' }],
    })
  })

  it('exposes field lookup and preset expansions', () => {
    expect(findField('statusCode')?.id).toBe('status')
    expect(PRESETS.failed.expand()).toEqual([
      { kind: 'predicate', scope: 'any', field: 'status', op: ':', value: 'ERROR' },
    ])
  })
})
