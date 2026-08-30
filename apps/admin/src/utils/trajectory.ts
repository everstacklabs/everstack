/**
 * trajectory: derive and score an agent's path through a trace (D2).
 *
 * Most eval tools grade only the final output. Agents fail in the middle: they
 * call the wrong tool, loop on the same call, or take a needlessly long path to
 * the answer. This turns a trace's spans into an ordered trajectory of steps,
 * computes intrinsic quality signals (redundancy, loops, errors), and scores an
 * actual tool sequence against an expected one (ordered LCS coverage) so a
 * dataset can assert "the agent should search, then fetch, then summarize" and
 * get a real number back, not just a thumbs-up on the last message.
 *
 * Pure and deterministic: takes already-extracted step descriptors so it has no
 * dependency on the proto Span type and is trivially testable. The trace view
 * adapts Span[] -> StepInput[] via getSpanDisplayConfig.
 */

export type StepKind = 'generation' | 'tool' | 'function' | 'other'

export interface StepInput {
  /** Stable ordering key (nanoseconds since epoch, or any monotonic number). */
  startNs: number
  kind: StepKind
  /** Tool/function/model name used for sequence comparison and loop detection. */
  name: string
  /** Optional serialized args; identical (name,args) back-to-back = redundant. */
  args?: string
  isError?: boolean
}

export interface TrajectoryStep extends StepInput {
  index: number
  /** True when this step repeats the immediately preceding (name,args). */
  redundant: boolean
  /** True when this step's name appears in a run of >2 across the trajectory. */
  looping: boolean
}

export interface TrajectorySignals {
  stepCount: number
  toolCallCount: number
  generationCount: number
  distinctTools: number
  redundantSteps: number
  loopingSteps: number
  errorSteps: number
  /** distinctTools / toolCallCount; 1.0 = never repeated a tool, lower = churn. */
  toolDiversity: number
  toolSequence: string[]
}

export interface TrajectoryAnalysis {
  steps: TrajectoryStep[]
  signals: TrajectorySignals
}

const TOOL_KINDS: ReadonlySet<StepKind> = new Set(['tool', 'function'])

/** Build the ordered trajectory and intrinsic quality signals from raw steps. */
export function analyzeTrajectory(input: StepInput[]): TrajectoryAnalysis {
  const ordered = [...input].sort((a, b) => a.startNs - b.startNs)

  // Count runs of the same name to flag looping (a name appearing >2 times).
  const nameCounts = new Map<string, number>()
  for (const s of ordered) nameCounts.set(s.name, (nameCounts.get(s.name) ?? 0) + 1)

  const steps: TrajectoryStep[] = ordered.map((s, index) => {
    const prev = index > 0 ? ordered[index - 1] : undefined
    const redundant =
      !!prev && prev.name === s.name && (prev.args ?? '') === (s.args ?? '') && TOOL_KINDS.has(s.kind)
    const looping = TOOL_KINDS.has(s.kind) && (nameCounts.get(s.name) ?? 0) > 2
    return { ...s, index, redundant, looping }
  })

  const toolSteps = steps.filter((s) => TOOL_KINDS.has(s.kind))
  const distinctTools = new Set(toolSteps.map((s) => s.name)).size
  const toolCallCount = toolSteps.length

  const signals: TrajectorySignals = {
    stepCount: steps.length,
    toolCallCount,
    generationCount: steps.filter((s) => s.kind === 'generation').length,
    distinctTools,
    redundantSteps: steps.filter((s) => s.redundant).length,
    loopingSteps: steps.filter((s) => s.looping).length,
    errorSteps: steps.filter((s) => s.isError).length,
    toolDiversity: toolCallCount > 0 ? distinctTools / toolCallCount : 1,
    toolSequence: toolSteps.map((s) => s.name),
  }

  return { steps, signals }
}

export interface TrajectoryScore {
  /** Fraction of expected steps matched in order (0..1). */
  score: number
  /** Length of the longest common subsequence of names, in order. */
  matched: number
  expectedLength: number
  /** Expected steps that never appeared (in order). */
  missing: string[]
  /** Actual tool calls beyond what the LCS consumed (extra/unexpected work). */
  extra: string[]
  /** 'match' >= 0.999, 'partial' > 0, else 'mismatch'. */
  verdict: 'match' | 'partial' | 'mismatch'
}

/**
 * Score an actual tool sequence against an expected ordered sequence using a
 * longest-common-subsequence: order matters, but extra steps between expected
 * ones do not disqualify a match. Returns coverage of the expected path plus
 * the missing and extra steps for explainability.
 */
export function scoreTrajectory(actual: string[], expected: string[]): TrajectoryScore {
  const n = actual.length
  const m = expected.length
  if (m === 0) {
    return {
      score: 1,
      matched: 0,
      expectedLength: 0,
      missing: [],
      extra: [...actual],
      verdict: 'match',
    }
  }

  // LCS table over (actual, expected).
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
  for (let i = 1; i <= n; i++) {
    for (let j = 1; j <= m; j++) {
      dp[i][j] =
        actual[i - 1] === expected[j - 1]
          ? dp[i - 1][j - 1] + 1
          : Math.max(dp[i - 1][j], dp[i][j - 1])
    }
  }
  const matched = dp[n][m]

  // Backtrack to recover which expected/actual indices participated.
  const matchedExpected = new Set<number>()
  const matchedActual = new Set<number>()
  let i = n
  let j = m
  while (i > 0 && j > 0) {
    if (actual[i - 1] === expected[j - 1]) {
      matchedExpected.add(j - 1)
      matchedActual.add(i - 1)
      i--
      j--
    } else if (dp[i - 1][j] >= dp[i][j - 1]) {
      i--
    } else {
      j--
    }
  }

  const missing = expected.filter((_, idx) => !matchedExpected.has(idx))
  const extra = actual.filter((_, idx) => !matchedActual.has(idx))
  const score = matched / m
  const verdict: TrajectoryScore['verdict'] =
    score >= 0.999 ? 'match' : score > 0 ? 'partial' : 'mismatch'

  return { score, matched, expectedLength: m, missing, extra, verdict }
}
