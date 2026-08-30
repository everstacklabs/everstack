import { describe, it, expect } from 'vitest'
import { cohenKappa, interpretKappa, judgeIsTrustworthy } from './agreement'

describe('cohenKappa', () => {
  it('returns 1 for perfect agreement', () => {
    const r = cohenKappa(['pass', 'fail', 'pass', 'fail'], ['pass', 'fail', 'pass', 'fail'])
    expect(r.kappa).toBe(1)
    expect(r.observedAgreement).toBe(1)
  })

  it('returns ~0 for chance-level agreement', () => {
    // Two raters each 50/50 but uncorrelated: observed agreement ~ expected.
    const a = ['p', 'p', 'f', 'f']
    const b = ['p', 'f', 'p', 'f']
    const r = cohenKappa(a, b)
    // po = 0.5 (agree on item 0 and 3), pe = 0.5 -> kappa = 0.
    expect(r.observedAgreement).toBeCloseTo(0.5)
    expect(r.expectedAgreement).toBeCloseTo(0.5)
    expect(r.kappa).toBeCloseTo(0)
  })

  it('is negative when agreement is worse than chance', () => {
    const r = cohenKappa(['p', 'p', 'f', 'f'], ['f', 'f', 'p', 'p'])
    expect(r.kappa).toBeLessThan(0)
  })

  it('handles a single category used by both raters', () => {
    const r = cohenKappa(['pass', 'pass'], ['pass', 'pass'])
    expect(r.kappa).toBe(1)
  })

  it('handles empty input', () => {
    expect(cohenKappa([], []).kappa).toBe(0)
  })

  it('corrects for chance: high raw agreement on a skewed set is not high kappa', () => {
    // 9/10 "pass" for both, judge disagrees on the one "fail".
    const human = ['p', 'p', 'p', 'p', 'p', 'p', 'p', 'p', 'p', 'f']
    const judge = ['p', 'p', 'p', 'p', 'p', 'p', 'p', 'p', 'p', 'p']
    const r = cohenKappa(human, judge)
    expect(r.observedAgreement).toBeCloseTo(0.9)
    // High raw agreement but kappa should be low/zero (judge never says fail).
    expect(r.kappa).toBeLessThan(0.2)
  })
})

describe('interpretKappa', () => {
  it('maps to Landis-Koch strength labels', () => {
    expect(interpretKappa(-0.1)).toBe('poor')
    expect(interpretKappa(0.1)).toBe('slight')
    expect(interpretKappa(0.3)).toBe('fair')
    expect(interpretKappa(0.5)).toBe('moderate')
    expect(interpretKappa(0.7)).toBe('substantial')
    expect(interpretKappa(0.9)).toBe('almost perfect')
  })
})

describe('judgeIsTrustworthy', () => {
  it('requires moderate agreement over a sufficient sample', () => {
    expect(judgeIsTrustworthy({ n: 30, observedAgreement: 0.9, expectedAgreement: 0.5, kappa: 0.6 })).toBe(true)
    expect(judgeIsTrustworthy({ n: 5, observedAgreement: 0.9, expectedAgreement: 0.5, kappa: 0.9 })).toBe(false)
    expect(judgeIsTrustworthy({ n: 30, observedAgreement: 0.6, expectedAgreement: 0.5, kappa: 0.2 })).toBe(false)
  })
})
