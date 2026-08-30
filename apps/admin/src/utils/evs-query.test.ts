import { describe, expect, it } from 'vitest'
import {
  evsManagedSearchKeys,
  evsQueryFromSearch,
  parseEvsQuery,
  type EvsQueryField,
} from './evs-query'

const fields: EvsQueryField[] = [
  { id: 'query', label: 'Text', token: 'text:', searchKey: 'query' },
  {
    id: 'traceId',
    label: 'Trace',
    token: 'traceId:',
    searchKey: 'trace',
    aliases: ['trace'],
    clearKeys: ['span'],
  },
  {
    id: 'status',
    label: 'Status',
    token: 'status:',
    searchKey: 'statusCode',
    aliases: ['statusCode'],
  },
  {
    id: 'provider',
    label: 'Provider',
    token: 'provider:',
    searchKey: 'provider',
  },
  { id: 'tags', label: 'Tag', token: 'tag:', searchKey: 'tags' },
  {
    id: 'metadata',
    label: 'Metadata',
    token: 'metadata:',
    searchKey: 'metadata',
    aliases: ['meta'],
  },
]

describe('parseEvsQuery', () => {
  it('parses a multi-clause Datadog-style query', () => {
    const parsed = parseEvsQuery(
      'checkout status:error provider:anthropic tag:prod tag:vip @customer_tier:enterprise duration>500ms cost<=0.25',
      fields,
    )

    expect(parsed).toEqual({
      ok: true,
      filters: {
        query: 'checkout',
        statusCode: 'ERROR',
        provider: 'anthropic',
        tags: 'prod,vip',
        metadata: 'customer_tier=enterprise',
        minDuration: '500',
        maxCost: '0.25',
      },
    })
  })

  it('supports quoted text and metadata:key=value syntax', () => {
    const parsed = parseEvsQuery(
      'text:"missing api key" metadata:plan=pro trace:tr_123',
      fields,
    )

    expect(parsed).toEqual({
      ok: true,
      filters: {
        query: 'missing api key',
        metadata: 'plan=pro',
        trace: 'tr_123',
      },
    })
  })

  it('normalizes duration units to milliseconds', () => {
    expect(parseEvsQuery('duration>=2s duration<1m', fields)).toEqual({
      ok: true,
      filters: {
        minDuration: '2000',
        maxDuration: '60000',
      },
    })
  })

  it('returns validation errors for unsupported fields', () => {
    expect(parseEvsQuery('service:api cost>nope', fields)).toEqual({
      ok: false,
      errors: ['Unknown field: service', 'Invalid cost value: nope'],
    })
  })
})

describe('evsQueryFromSearch', () => {
  it('formats existing search params as an EVS Query string', () => {
    expect(
      evsQueryFromSearch(
        {
          query: 'missing api key',
          statusCode: 'ERROR',
          tags: 'prod,vip',
          metadata: 'plan=pro,customer_tier=enterprise',
          minDuration: '500',
        },
        fields,
      ),
    ).toBe(
      'text:"missing api key" status:ERROR tag:prod tag:vip @plan:pro @customer_tier:enterprise duration>=500ms',
    )
  })
})

describe('evsManagedSearchKeys', () => {
  it('includes clear keys for linked route state', () => {
    expect(evsManagedSearchKeys(fields)).toContain('span')
  })
})
