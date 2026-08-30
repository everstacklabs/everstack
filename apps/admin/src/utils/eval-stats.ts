/**
 * eval-stats: statistical-significance engine for eval-run comparison.
 *
 * Every competitor (Braintrust, LangSmith, Opik, HoneyHive) reports a "regression"
 * as a raw mean-score decrease with NO significance testing — so teams ship on
 * noise. This computes a **paired bootstrap** over per-item score differences:
 * a 95% confidence interval on the mean delta and a verdict that refuses to call
 * an improvement/regression unless the CI excludes zero. Numeric and boolean
 * (0/1) scores are handled the same way (a boolean mean is a proportion).
 *
 * Deterministic: a seeded PRNG so the same comparison always renders the same CI.
 *
 * NON-AUTHORITATIVE: the live comparison engine is the Go port behind
 * CompareEvalRuns (typed scorer_results/overall on the response); the run
 * comparison page reads that, not this module. Keep this only for
 * server-less scratch surfaces (e.g. playground live scoring) and as the
 * reference fixture for Go-parity tests.
 */

export type Verdict =
  | 'improvement' // CI entirely > 0
  | 'regression' // CI entirely < 0
  | 'inconclusive' // CI spans 0 — change not distinguishable from noise
  | 'insufficient' // too few paired samples to test

export interface PairedComparison {
  n: number // number of paired items
  meanA: number // baseline mean
  meanB: number // candidate mean
  meanDiff: number // meanB - meanA
  ciLow: number // 2.5th pct of bootstrap mean-diff
  ciHigh: number // 97.5th pct
  pValue: number // two-sided bootstrap p-value for meanDiff != 0
  verdict: Verdict
}

/** mulberry32 — tiny deterministic PRNG so CIs are stable across renders. */
function makeRng(seed: number) {
  let s = seed >>> 0
  return () => {
    s |= 0
    s = (s + 0x6d2b79f5) | 0
    let t = Math.imul(s ^ (s >>> 15), 1 | s)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

function mean(xs: number[]): number {
  if (xs.length === 0) return 0
  let sum = 0
  for (const x of xs) sum += x
  return sum / xs.length
}

function percentile(sorted: number[], p: number): number {
  if (sorted.length === 0) return 0
  const idx = (sorted.length - 1) * p
  const lo = Math.floor(idx)
  const hi = Math.ceil(idx)
  if (lo === hi) return sorted[lo]
  return sorted[lo] + (sorted[hi] - sorted[lo]) * (idx - lo)
}

const MIN_N = 5 // below this a bootstrap is meaningless
const BOOTSTRAP = 2000
const ALPHA = 0.05

/**
 * Paired comparison of candidate (b) vs baseline (a). Arrays must be aligned by
 * dataset item (a[i] and b[i] are the same item under two runs). Returns CI +
 * verdict; positive meanDiff means the candidate scored higher.
 */
export function comparePaired(a: number[], b: number[]): PairedComparison {
  const n = Math.min(a.length, b.length)
  const meanA = mean(a.slice(0, n))
  const meanB = mean(b.slice(0, n))
  const base: PairedComparison = {
    n,
    meanA,
    meanB,
    meanDiff: meanB - meanA,
    ciLow: 0,
    ciHigh: 0,
    pValue: 1,
    verdict: 'insufficient',
  }
  if (n < MIN_N) return base

  const diffs: number[] = new Array(n)
  for (let i = 0; i < n; i++) diffs[i] = b[i] - a[i]

  // Seed from n + a checksum so identical inputs reproduce identical CIs.
  let checksum = n
  for (let i = 0; i < n; i++) checksum = (checksum * 31 + Math.round(diffs[i] * 1e6)) | 0
  const rng = makeRng(checksum)

  const bootMeans = new Array(BOOTSTRAP)
  let ge0 = 0
  let le0 = 0
  for (let k = 0; k < BOOTSTRAP; k++) {
    let sum = 0
    for (let i = 0; i < n; i++) sum += diffs[(rng() * n) | 0]
    const m = sum / n
    bootMeans[k] = m
    if (m >= 0) ge0++
    if (m <= 0) le0++
  }
  bootMeans.sort((x: number, y: number) => x - y)

  const ciLow = percentile(bootMeans, ALPHA / 2)
  const ciHigh = percentile(bootMeans, 1 - ALPHA / 2)
  // Two-sided bootstrap p-value: how often the resampled mean crosses 0.
  const pValue = Math.min(1, (2 * Math.min(ge0, le0)) / BOOTSTRAP)

  let verdict: Verdict = 'inconclusive'
  if (ciLow > 0) verdict = 'improvement'
  else if (ciHigh < 0) verdict = 'regression'

  return { n, meanA, meanB, meanDiff: meanB - meanA, ciLow, ciHigh, pValue, verdict }
}

/**
 * Pull a `{ scorerName: numericValue }` map out of an EvalRunItem's `scores`
 * Struct, which may be either a map `{name: value|{value}}` or an array
 * `[{name, value|numericValue|score}]`. Non-numeric scores are skipped.
 */
export function itemScoreMap(scores: unknown): Record<string, number> {
  const out: Record<string, number> = {}
  if (!scores) return out
  const pickNum = (v: unknown): number | undefined => {
    if (typeof v === 'number' && Number.isFinite(v)) return v
    if (typeof v === 'boolean') return v ? 1 : 0
    if (v && typeof v === 'object') {
      const o = v as Record<string, unknown>
      for (const k of ['value', 'numericValue', 'score', 'numeric_value']) {
        if (typeof o[k] === 'number') return o[k] as number
        if (typeof o[k] === 'boolean') return (o[k] as boolean) ? 1 : 0
      }
    }
    return undefined
  }
  if (Array.isArray(scores)) {
    for (const s of scores as Array<Record<string, unknown>>) {
      const name = typeof s?.name === 'string' ? s.name : undefined
      const val = pickNum(s?.value ?? s?.numericValue ?? s?.score ?? s)
      if (name && val !== undefined) out[name] = val
    }
  } else if (typeof scores === 'object') {
    for (const [name, v] of Object.entries(scores as Record<string, unknown>)) {
      const val = pickNum(v)
      if (val !== undefined) out[name] = val
    }
  }
  return out
}

/**
 * Pair a single numeric metric (e.g. cost, latency) across two runs' items,
 * aligned by dataset_item_id. Used for cost-aware eval: bootstrap a CI on the
 * per-item cost/latency delta, not just compare run totals.
 */
export function pairItemsByMetric(
  baseline: Array<{ datasetItemId?: string }>,
  candidate: Array<{ datasetItemId?: string }>,
  pick: (item: any) => number | undefined, // eslint-disable-line @typescript-eslint/no-explicit-any
): { a: number[]; b: number[] } {
  const baseByItem = new Map<string, number>()
  for (const it of baseline) {
    if (!it.datasetItemId) continue
    const v = pick(it)
    if (v !== undefined && Number.isFinite(v)) baseByItem.set(it.datasetItemId, v)
  }
  const a: number[] = []
  const b: number[] = []
  for (const it of candidate) {
    if (!it.datasetItemId) continue
    const bv = pick(it)
    if (bv === undefined || !Number.isFinite(bv)) continue
    const av = baseByItem.get(it.datasetItemId)
    if (av === undefined) continue
    a.push(av)
    b.push(bv)
  }
  return { a, b }
}

/**
 * Build paired numeric arrays per scorer from two runs' items, aligned by
 * dataset_item_id. Returns `{ scorerName: { a: number[], b: number[] } }`.
 */
export function pairItemsByScorer(
  baselineItems: Array<{ datasetItemId?: string; scores?: unknown }>,
  candidateItems: Array<{ datasetItemId?: string; scores?: unknown }>,
): Record<string, { a: number[]; b: number[] }> {
  const baseByItem = new Map<string, Record<string, number>>()
  for (const it of baselineItems) {
    if (it.datasetItemId) baseByItem.set(it.datasetItemId, itemScoreMap(it.scores))
  }
  const out: Record<string, { a: number[]; b: number[] }> = {}
  for (const it of candidateItems) {
    if (!it.datasetItemId) continue
    const baseScores = baseByItem.get(it.datasetItemId)
    if (!baseScores) continue
    const candScores = itemScoreMap(it.scores)
    for (const name of Object.keys(candScores)) {
      if (!(name in baseScores)) continue
      ;(out[name] ??= { a: [], b: [] }).a.push(baseScores[name])
      out[name].b.push(candScores[name])
    }
  }
  return out
}
