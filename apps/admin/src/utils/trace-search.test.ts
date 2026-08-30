import { describe, it, expect } from 'vitest'
import type { Span, SpanTreeNode } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { spanMatchesQuery, collectSearchMatches } from './trace-search'

const span = (
  spanId: string,
  spanName: string,
  spanAttributes: Record<string, string> = {},
  statusCode = 'OK',
): Span => ({ spanId, spanName, statusCode, spanAttributes }) as unknown as Span

const node = (s: Span, children: SpanTreeNode[] = []): SpanTreeNode =>
  ({ span: s, children }) as unknown as SpanTreeNode

// root
// ├── turn
// │   ├── llm  (model gpt-4o-mini, output "the answer is 42")
// │   └── tool (tool.name web_search_xyz)
// └── mem  (query vector-lookup-xyz, status ERROR)
const tree: SpanTreeNode = node(
  span('root', 'agent.session', { 'session.id': 'sess-unique-xyz' }),
  [
    node(span('turn', 'agent.turn.1'), [
      node(
        span('llm', 'provider.openai.chat', {
          'gen_ai.request.model': 'gpt-4o-mini',
          'trace.output': 'the answer is 42',
        }),
      ),
      node(span('tool', 'agent.tool.search', { 'tool.name': 'web_search_xyz' })),
    ]),
    node(span('mem', 'memory.retrieve', { query: 'vector-lookup-xyz' }, 'ERROR')),
  ],
)

describe('spanMatchesQuery', () => {
  it('an empty or whitespace query matches everything', () => {
    expect(spanMatchesQuery(span('x', 'anything'), '')).toBe(true)
    expect(spanMatchesQuery(span('x', 'anything'), '   ')).toBe(true)
  })

  it('matches the raw span name, case-insensitively', () => {
    expect(spanMatchesQuery(span('x', 'memory.retrieve'), 'MEMORY')).toBe(true)
  })

  it('matches an attribute value', () => {
    expect(
      spanMatchesQuery(span('x', 'provider.openai.chat', { 'gen_ai.request.model': 'gpt-4o-mini' }), 'gpt-4o'),
    ).toBe(true)
  })

  it('matches an attribute key', () => {
    expect(spanMatchesQuery(span('x', 'x', { 'tool.name': 'web_search' }), 'tool.name')).toBe(true)
  })

  it('matches the status code', () => {
    expect(spanMatchesQuery(span('x', 'x', {}, 'ERROR'), 'error')).toBe(true)
  })

  it('does not match when the token is absent everywhere', () => {
    expect(spanMatchesQuery(span('x', 'provider.openai.chat', { model: 'gpt-4o' }), 'no-such-zzz')).toBe(false)
  })
})

describe('collectSearchMatches', () => {
  it('returns empty sets for an empty query or missing tree', () => {
    expect(collectSearchMatches(tree, '').matchIds.size).toBe(0)
    expect(collectSearchMatches(tree, '   ').visibleIds.size).toBe(0)
    expect(collectSearchMatches(null, 'x').matchIds.size).toBe(0)
    expect(collectSearchMatches(undefined, 'x').visibleIds.size).toBe(0)
  })

  it('matches a deep span and includes its ancestors as visible', () => {
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'gpt-4o-mini')
    expect([...matchIds]).toEqual(['llm'])
    expect([...visibleIds].sort()).toEqual(['llm', 'root', 'turn'])
  })

  it('matches by attribute key and keeps the path to the hit', () => {
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'tool.name')
    expect([...matchIds]).toEqual(['tool'])
    expect([...visibleIds].sort()).toEqual(['root', 'tool', 'turn'])
  })

  it('unions the visible paths of matches across branches', () => {
    // "xyz" is present on root, tool and mem (but not llm or turn).
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'xyz')
    expect([...matchIds].sort()).toEqual(['mem', 'root', 'tool'])
    // turn is pulled in as an ancestor of tool even though it is not a match.
    expect([...visibleIds].sort()).toEqual(['mem', 'root', 'tool', 'turn'])
  })

  it('matches a root span alone with no descendants', () => {
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'sess-unique-xyz')
    expect([...matchIds]).toEqual(['root'])
    expect([...visibleIds]).toEqual(['root'])
  })

  it('matches on status and includes the ancestor', () => {
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'error')
    expect([...matchIds]).toEqual(['mem'])
    expect([...visibleIds].sort()).toEqual(['mem', 'root'])
  })

  it('returns empty when nothing matches', () => {
    const { matchIds, visibleIds } = collectSearchMatches(tree, 'no-such-token-zzz')
    expect(matchIds.size).toBe(0)
    expect(visibleIds.size).toBe(0)
  })
})
