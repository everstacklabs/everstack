/**
 * agreement: inter-rater agreement for judge calibration (D6, eval-of-evals).
 *
 * To trust an LLM-as-judge you have to know it agrees with humans. Cohen's kappa
 * measures agreement between two raters on categorical labels, correcting for the
 * agreement you'd expect by chance — so a judge that always says "pass" on a
 * mostly-pass set does not look good just because raw agreement is high. We use
 * it as judge-vs-human calibration (rater A = LLM judge, rater B = human) and,
 * computed over rolling windows, as judge-drift detection.
 */

export interface AgreementResult {
  n: number
  /** Raw fraction of items the two raters labelled the same. */
  observedAgreement: number
  /** Agreement expected by chance, from the category marginals. */
  expectedAgreement: number
  /** Cohen's kappa in [-1, 1]: 1 perfect, 0 chance, <0 worse than chance. */
  kappa: number
}

function marginals(labels: string[]): Map<string, number> {
  const m = new Map<string, number>()
  for (const l of labels) m.set(l, (m.get(l) ?? 0) + 1)
  return m
}

/**
 * cohenKappa computes inter-rater agreement between two raters' categorical
 * labels, aligned by item (a[i] and b[i] are the same item). Extra labels beyond
 * the shorter array are ignored.
 */
export function cohenKappa(a: string[], b: string[]): AgreementResult {
  const n = Math.min(a.length, b.length)
  if (n === 0) {
    return { n: 0, observedAgreement: 0, expectedAgreement: 0, kappa: 0 }
  }

  let agree = 0
  for (let i = 0; i < n; i++) if (a[i] === b[i]) agree++
  const po = agree / n

  const ma = marginals(a.slice(0, n))
  const mb = marginals(b.slice(0, n))
  let pe = 0
  for (const [cat, ca] of ma) {
    const cb = mb.get(cat) ?? 0
    pe += (ca / n) * (cb / n)
  }

  let kappa: number
  if (pe >= 1) {
    // Both raters used a single category throughout: perfect iff they agree.
    kappa = po >= 1 ? 1 : 0
  } else {
    kappa = (po - pe) / (1 - pe)
  }

  return { n, observedAgreement: po, expectedAgreement: pe, kappa }
}

export type KappaStrength =
  | 'poor'
  | 'slight'
  | 'fair'
  | 'moderate'
  | 'substantial'
  | 'almost perfect'

/** interpretKappa maps a kappa value to the Landis-Koch strength label. */
export function interpretKappa(kappa: number): KappaStrength {
  if (kappa < 0) return 'poor'
  if (kappa < 0.2) return 'slight'
  if (kappa < 0.4) return 'fair'
  if (kappa < 0.6) return 'moderate'
  if (kappa < 0.8) return 'substantial'
  return 'almost perfect'
}

/**
 * judgeIsTrustworthy is a convenience gate for D6: a judge is considered
 * calibrated when agreement with humans is at least "moderate" (kappa >= 0.4)
 * over a sufficient sample.
 */
export function judgeIsTrustworthy(result: AgreementResult, minN = 20): boolean {
  return result.n >= minN && result.kappa >= 0.4
}
