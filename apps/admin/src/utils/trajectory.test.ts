import { describe, it, expect } from 'vitest'
import { analyzeTrajectory, scoreTrajectory, type StepInput } from './trajectory'

const step = (startNs: number, kind: StepInput['kind'], name: string, extra: Partial<StepInput> = {}): StepInput => ({
  startNs,
  kind,
  name,
  ...extra,
})

describe('analyzeTrajectory', () => {
  it('orders steps by start time and counts tool vs generation', () => {
    const a = analyzeTrajectory([
      step(30, 'tool', 'fetch'),
      step(10, 'generation', 'gpt'),
      step(20, 'tool', 'search'),
    ])
    expect(a.steps.map((s) => s.name)).toEqual(['gpt', 'search', 'fetch'])
    expect(a.signals.generationCount).toBe(1)
    expect(a.signals.toolCallCount).toBe(2)
    expect(a.signals.distinctTools).toBe(2)
    expect(a.signals.toolSequence).toEqual(['search', 'fetch'])
  })

  it('flags back-to-back identical tool calls as redundant', () => {
    const a = analyzeTrajectory([
      step(1, 'tool', 'search', { args: '{"q":"x"}' }),
      step(2, 'tool', 'search', { args: '{"q":"x"}' }),
      step(3, 'tool', 'search', { args: '{"q":"y"}' }),
    ])
    expect(a.steps.map((s) => s.redundant)).toEqual([false, true, false])
    expect(a.signals.redundantSteps).toBe(1)
  })

  it('flags looping when a tool name repeats more than twice', () => {
    const a = analyzeTrajectory([
      step(1, 'tool', 'poll'),
      step(2, 'tool', 'poll'),
      step(3, 'tool', 'poll'),
      step(4, 'tool', 'done'),
    ])
    expect(a.signals.loopingSteps).toBe(3)
    expect(a.steps[3].looping).toBe(false)
  })

  it('computes tool diversity and error counts', () => {
    const a = analyzeTrajectory([
      step(1, 'tool', 'a'),
      step(2, 'tool', 'a'),
      step(3, 'tool', 'b', { isError: true }),
    ])
    expect(a.signals.toolDiversity).toBeCloseTo(2 / 3)
    expect(a.signals.errorSteps).toBe(1)
  })

  it('returns diversity 1 when there are no tool calls', () => {
    const a = analyzeTrajectory([step(1, 'generation', 'gpt')])
    expect(a.signals.toolDiversity).toBe(1)
    expect(a.signals.toolCallCount).toBe(0)
  })
})

describe('scoreTrajectory', () => {
  it('scores a perfect ordered match as 1.0', () => {
    const r = scoreTrajectory(['search', 'fetch', 'summarize'], ['search', 'fetch', 'summarize'])
    expect(r.score).toBe(1)
    expect(r.verdict).toBe('match')
    expect(r.missing).toEqual([])
    expect(r.extra).toEqual([])
  })

  it('tolerates extra steps between expected ones (LCS, not exact)', () => {
    const r = scoreTrajectory(['search', 'noise', 'fetch'], ['search', 'fetch'])
    expect(r.score).toBe(1)
    expect(r.matched).toBe(2)
    expect(r.extra).toEqual(['noise'])
    expect(r.verdict).toBe('match')
  })

  it('penalizes a missing expected step', () => {
    const r = scoreTrajectory(['search'], ['search', 'fetch'])
    expect(r.score).toBe(0.5)
    expect(r.missing).toEqual(['fetch'])
    expect(r.verdict).toBe('partial')
  })

  it('respects order: out-of-order matches do not all count', () => {
    const r = scoreTrajectory(['fetch', 'search'], ['search', 'fetch'])
    expect(r.matched).toBe(1)
    expect(r.score).toBe(0.5)
  })

  it('reports mismatch when nothing lines up', () => {
    const r = scoreTrajectory(['x', 'y'], ['a', 'b'])
    expect(r.score).toBe(0)
    expect(r.verdict).toBe('mismatch')
    expect(r.missing).toEqual(['a', 'b'])
  })

  it('treats an empty expectation as a trivial match', () => {
    const r = scoreTrajectory(['a'], [])
    expect(r.score).toBe(1)
    expect(r.verdict).toBe('match')
    expect(r.extra).toEqual(['a'])
  })
})
