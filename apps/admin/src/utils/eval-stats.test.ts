import { describe, it, expect } from 'vitest'
import { comparePaired, itemScoreMap, pairItemsByScorer } from './eval-stats'

describe('comparePaired', () => {
  it('calls a clear, consistent gain an improvement (CI > 0)', () => {
    const a = Array(20).fill(0.4)
    const b = Array(20).fill(0.8)
    const r = comparePaired(a, b)
    expect(r.verdict).toBe('improvement')
    expect(r.ciLow).toBeGreaterThan(0)
    expect(r.meanDiff).toBeCloseTo(0.4, 5)
  })

  it('calls a clear, consistent drop a regression (CI < 0)', () => {
    const a = Array(20).fill(0.9)
    const b = Array(20).fill(0.5)
    const r = comparePaired(a, b)
    expect(r.verdict).toBe('regression')
    expect(r.ciHigh).toBeLessThan(0)
  })

  it('refuses to call noise (mean delta ~0) anything — inconclusive', () => {
    const a = Array(20).fill(0.5)
    // alternating +0.3 / -0.3 → mean diff 0, wide spread → CI spans 0
    const b = a.map((v, i) => v + (i % 2 === 0 ? 0.3 : -0.3))
    const r = comparePaired(a, b)
    expect(r.verdict).toBe('inconclusive')
    expect(r.ciLow).toBeLessThan(0)
    expect(r.ciHigh).toBeGreaterThan(0)
  })

  it('flags insufficient samples below the minimum', () => {
    expect(comparePaired([1, 0, 1], [1, 1, 1]).verdict).toBe('insufficient')
  })

  it('is deterministic across calls', () => {
    const a = Array(15).fill(0).map((_, i) => (i % 3) / 3)
    const b = a.map((v) => v + 0.1)
    expect(comparePaired(a, b)).toEqual(comparePaired(a, b))
  })
})

describe('itemScoreMap', () => {
  it('reads a map of {name: value | {value} | boolean}', () => {
    expect(itemScoreMap({ accuracy: 0.8, helpful: { value: 1 }, passed: true })).toEqual({
      accuracy: 0.8,
      helpful: 1,
      passed: 1,
    })
  })
  it('reads an array of {name, value}', () => {
    expect(itemScoreMap([{ name: 'accuracy', value: 0.8 }, { name: 'tone', numericValue: 0.5 }])).toEqual({
      accuracy: 0.8,
      tone: 0.5,
    })
  })
})

describe('pairItemsByScorer', () => {
  it('pairs items by datasetItemId per scorer', () => {
    const base = [
      { datasetItemId: 'x', scores: { acc: 0.4 } },
      { datasetItemId: 'y', scores: { acc: 0.6 } },
    ]
    const cand = [
      { datasetItemId: 'x', scores: { acc: 0.9 } },
      { datasetItemId: 'y', scores: { acc: 0.8 } },
      { datasetItemId: 'z', scores: { acc: 1.0 } }, // unpaired → ignored
    ]
    const paired = pairItemsByScorer(base, cand)
    expect(paired.acc.a).toEqual([0.4, 0.6])
    expect(paired.acc.b).toEqual([0.9, 0.8])
  })
})
