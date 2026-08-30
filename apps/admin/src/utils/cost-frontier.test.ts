import { describe, it, expect } from 'vitest'
import { paretoFrontier, qualityPerDollar, isDominated, type VariantPoint } from './cost-frontier'

const ids = (ps: VariantPoint[]) => ps.map((p) => p.id).sort()

describe('paretoFrontier', () => {
  it('keeps only non-dominated variants', () => {
    const points: VariantPoint[] = [
      { id: 'cheap-ok', score: 0.7, cost: 1 }, // frontier (cheapest)
      { id: 'mid-good', score: 0.85, cost: 3 }, // frontier (more quality, more cost)
      { id: 'expensive-best', score: 0.9, cost: 10 }, // frontier (most quality)
      { id: 'dominated', score: 0.75, cost: 5 }, // dominated by mid-good (higher score, lower cost)
    ]
    expect(ids(paretoFrontier(points))).toEqual(['cheap-ok', 'expensive-best', 'mid-good'])
  })

  it('sorts the frontier by cost ascending', () => {
    const out = paretoFrontier([
      { id: 'b', score: 0.9, cost: 10 },
      { id: 'a', score: 0.7, cost: 1 },
      { id: 'c', score: 0.85, cost: 3 },
    ])
    expect(out.map((p) => p.id)).toEqual(['a', 'c', 'b'])
  })

  it('keeps identical points (neither dominates)', () => {
    const out = paretoFrontier([
      { id: 'x', score: 0.8, cost: 2 },
      { id: 'y', score: 0.8, cost: 2 },
    ])
    expect(out).toHaveLength(2)
  })

  it('a strictly cheaper, equal-score variant dominates', () => {
    const out = paretoFrontier([
      { id: 'cheaper', score: 0.8, cost: 1 },
      { id: 'pricier', score: 0.8, cost: 2 },
    ])
    expect(ids(out)).toEqual(['cheaper'])
  })
})

describe('qualityPerDollar', () => {
  it('is score / cost', () => {
    expect(qualityPerDollar({ id: 'a', score: 0.8, cost: 4 })).toBeCloseTo(0.2)
  })
  it('handles zero cost', () => {
    expect(qualityPerDollar({ id: 'free', score: 0.5, cost: 0 })).toBe(Infinity)
    expect(qualityPerDollar({ id: 'free0', score: 0, cost: 0 })).toBe(0)
  })
})

describe('isDominated', () => {
  it('flags inefficient variants', () => {
    const all: VariantPoint[] = [
      { id: 'good', score: 0.9, cost: 2 },
      { id: 'bad', score: 0.6, cost: 5 },
    ]
    expect(isDominated(all[1], all)).toBe(true)
    expect(isDominated(all[0], all)).toBe(false)
  })
})
