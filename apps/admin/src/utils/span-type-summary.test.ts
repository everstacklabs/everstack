import { describe, it, expect } from 'vitest'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { spanTypeSummary } from './span-type-summary'

function span(attrs: Record<string, string>): Span {
  return { spanAttributes: attrs } as unknown as Span
}

describe('spanTypeSummary', () => {
  it('summarizes a sandbox exec span', () => {
    const f = spanTypeSummary(
      span({ 'sandbox.command': 'git status', 'sandbox.exit_code': '0', 'sandbox.duration_ms': '12' }),
      'sandbox',
    )
    expect(f).toEqual([
      { label: 'Command', value: 'git status' },
      { label: 'Exit code', value: '0' },
      { label: 'Duration (ms)', value: '12' },
    ])
  })

  it('summarizes a browser span and drops empty fields', () => {
    const f = spanTypeSummary(span({ 'browser.action': 'navigate', 'browser.url': 'https://x.io' }), 'browser')
    expect(f).toEqual([
      { label: 'Action', value: 'navigate' },
      { label: 'URL', value: 'https://x.io' },
    ])
  })

  it('summarizes a retriever span, falling back across memory/vector keys', () => {
    const f = spanTypeSummary(span({ 'memory.operation': 'retrieve', 'memory.result_count': '5' }), 'retriever')
    expect(f).toContainEqual({ label: 'Operation', value: 'retrieve' })
    expect(f).toContainEqual({ label: 'Results', value: '5' })
  })

  it('summarizes a scorer span', () => {
    const f = spanTypeSummary(
      span({ 'scorer.name': 'task_completion', 'scorer.score_count': '2', 'scoring.state': '2 done' }),
      'scorer',
    )
    expect(f).toEqual([
      { label: 'Scorer', value: 'task_completion' },
      { label: 'Scores', value: '2' },
      { label: 'State', value: '2 done' },
    ])
  })

  it('returns empty for categories without a dedicated summary', () => {
    expect(spanTypeSummary(span({}), 'internal')).toEqual([])
    expect(spanTypeSummary(span({ 'browser.action': 'click' }), 'gateway')).toEqual([])
  })
})
