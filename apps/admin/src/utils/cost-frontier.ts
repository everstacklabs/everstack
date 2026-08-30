/**
 * cost-frontier: quality-per-dollar Pareto analysis for eval-run / prompt-model
 * variants (D5, cost-aware eval).
 *
 * The roadmap makes quality-per-dollar a first-class axis: instead of picking the
 * highest-scoring variant regardless of cost, surface the Pareto frontier of
 * score x cost so a team can see which variants are genuinely non-dominated (you
 * cannot get more quality without paying more, or pay less without losing
 * quality). Pairs with the per-item cost CI in eval-stats (pairItemsByMetric).
 *
 * Convention: score is "higher is better" (any scale); cost is "lower is better"
 * (USD, or latency, etc.).
 */

export interface VariantPoint {
  id: string
  score: number
  cost: number
}

/**
 * dominates returns true when a is at least as good as b on both axes and
 * strictly better on at least one (>= score, <= cost, and not equal on both).
 */
function dominates(a: VariantPoint, b: VariantPoint): boolean {
  const noWorse = a.score >= b.score && a.cost <= b.cost
  const strictlyBetter = a.score > b.score || a.cost < b.cost
  return noWorse && strictlyBetter
}

/**
 * paretoFrontier returns the non-dominated variants (the efficient frontier),
 * sorted by cost ascending then score descending. Identical points are both
 * kept (neither strictly dominates the other).
 */
export function paretoFrontier(points: VariantPoint[]): VariantPoint[] {
  const frontier = points.filter((p) => !points.some((q) => q !== p && dominates(q, p)))
  return frontier.sort((x, y) => x.cost - y.cost || y.score - x.score)
}

/**
 * qualityPerDollar is score divided by cost, for ranking variants by efficiency.
 * A zero-cost variant returns Infinity when it scored, else 0.
 */
export function qualityPerDollar(p: VariantPoint): number {
  if (p.cost <= 0) return p.score > 0 ? Infinity : 0
  return p.score / p.cost
}

/**
 * isDominated reports whether a point is dominated by any other in the set — i.e.
 * it is NOT on the Pareto frontier. Useful to grey out inefficient variants.
 */
export function isDominated(point: VariantPoint, all: VariantPoint[]): boolean {
  return all.some((q) => q !== point && dominates(q, point))
}
