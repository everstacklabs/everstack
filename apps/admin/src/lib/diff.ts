/**
 * Tiny word-level diff for trace comparison. LLM responses are typically
 * a few hundred chars to a few KB, so the O(n*m) LCS table is fine — no
 * need to pull in a 30 KB diff package for this scale.
 *
 * Returns an array of operations: equal/insert/delete. Tokens are
 * preserved exactly so whitespace renders correctly in the UI.
 */

export type DiffOp =
    | { type: 'equal'; value: string }
    | { type: 'insert'; value: string }
    | { type: 'delete'; value: string }

/** Tokenise into words AND whitespace runs as separate tokens so the
 * rendering can keep the original layout. */
function tokenise(s: string): string[] {
    if (!s) return []
    return s.match(/\s+|\S+/g) ?? []
}

/**
 * Word-level LCS diff. O(n*m) time + memory; cap inputs at 20k tokens to
 * keep us out of trouble — beyond that we render a "too large for inline
 * diff" placeholder.
 */
const MAX_TOKENS = 20_000

export function diffWords(a: string, b: string): DiffOp[] {
    if (!a && !b) return []
    if (!a) return [{ type: 'insert', value: b }]
    if (!b) return [{ type: 'delete', value: a }]

    const A = tokenise(a)
    const B = tokenise(b)
    if (A.length > MAX_TOKENS || B.length > MAX_TOKENS) {
        return [
            { type: 'delete', value: a },
            { type: 'insert', value: b },
        ]
    }

    const n = A.length
    const m = B.length
    // dp[i][j] = length of LCS of A[0..i) and B[0..j)
    const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
    for (let i = 1; i <= n; i++) {
        for (let j = 1; j <= m; j++) {
            if (A[i - 1] === B[j - 1]) {
                dp[i][j] = dp[i - 1][j - 1] + 1
            } else {
                dp[i][j] = Math.max(dp[i - 1][j], dp[i][j - 1])
            }
        }
    }

    const ops: DiffOp[] = []
    let i = n
    let j = m
    while (i > 0 && j > 0) {
        if (A[i - 1] === B[j - 1]) {
            ops.push({ type: 'equal', value: A[i - 1] })
            i--
            j--
        } else if (dp[i - 1][j] >= dp[i][j - 1]) {
            ops.push({ type: 'delete', value: A[i - 1] })
            i--
        } else {
            ops.push({ type: 'insert', value: B[j - 1] })
            j--
        }
    }
    while (i > 0) {
        ops.push({ type: 'delete', value: A[i - 1] })
        i--
    }
    while (j > 0) {
        ops.push({ type: 'insert', value: B[j - 1] })
        j--
    }
    ops.reverse()

    // Merge adjacent same-type ops for cleaner rendering.
    const merged: DiffOp[] = []
    for (const op of ops) {
        const prev = merged[merged.length - 1]
        if (prev && prev.type === op.type) {
            prev.value += op.value
        } else {
            merged.push({ ...op })
        }
    }
    return merged
}

/** Summary of a diff: how many adds/deletes, useful for headers. */
export function diffStats(ops: DiffOp[]): { adds: number; deletes: number; equals: number } {
    let adds = 0
    let deletes = 0
    let equals = 0
    for (const op of ops) {
        const len = op.value.length
        if (op.type === 'insert') adds += len
        else if (op.type === 'delete') deletes += len
        else equals += len
    }
    return { adds, deletes, equals }
}
