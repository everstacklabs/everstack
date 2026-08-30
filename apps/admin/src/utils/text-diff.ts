/**
 * text-diff: dependency-free LCS diff for the eval comparison grid.
 *
 * Braintrust caps diffable output length; we explicitly do not, so the
 * algorithm degrades gracefully instead of hanging: common prefix/suffix are
 * trimmed first (usually most of the text), the middle is diffed at word
 * granularity, and small changed spans are refined to char granularity. When
 * inputs are too large for an exact word LCS the diff falls back to line
 * granularity, and past that to a single replace block - always correct,
 * progressively coarser.
 */

export type DiffSegment = {
    type: 'equal' | 'add' | 'del'
    text: string
}

/** Max DP cells for an exact LCS pass (~a few ms of work). */
const MAX_LCS_CELLS = 1_000_000
/** Max chars per side for char-level refinement of a changed span. */
const MAX_CHAR_REFINE = 600

/** Generic LCS diff over token arrays via a classic DP table + backtrack. */
function lcsDiff(a: string[], b: string[]): DiffSegment[] {
    const n = a.length
    const m = b.length
    if (n === 0 && m === 0) return []
    if (n === 0) return [{ type: 'add', text: b.join('') }]
    if (m === 0) return [{ type: 'del', text: a.join('') }]

    // dp[(i)*(m+1)+j] = LCS length of a[i..] vs b[j..]
    const dp = new Int32Array((n + 1) * (m + 1))
    for (let i = n - 1; i >= 0; i--) {
        for (let j = m - 1; j >= 0; j--) {
            dp[i * (m + 1) + j] =
                a[i] === b[j]
                    ? dp[(i + 1) * (m + 1) + j + 1] + 1
                    : Math.max(dp[(i + 1) * (m + 1) + j], dp[i * (m + 1) + j + 1])
        }
    }

    const out: DiffSegment[] = []
    const push = (type: DiffSegment['type'], text: string) => {
        if (!text) return
        const last = out[out.length - 1]
        if (last && last.type === type) last.text += text
        else out.push({ type, text })
    }

    let i = 0
    let j = 0
    while (i < n && j < m) {
        if (a[i] === b[j]) {
            push('equal', a[i])
            i++
            j++
        } else if (dp[(i + 1) * (m + 1) + j] >= dp[i * (m + 1) + j + 1]) {
            push('del', a[i])
            i++
        } else {
            push('add', b[j])
            j++
        }
    }
    while (i < n) push('del', a[i++])
    while (j < m) push('add', b[j++])
    return out
}

/** Split into word tokens, whitespace attached to the preceding word. */
function wordTokens(s: string): string[] {
    return s.match(/\S+\s*|\s+/g) ?? []
}

/** Split into lines, newline attached. */
function lineTokens(s: string): string[] {
    return s.match(/[^\n]*\n|[^\n]+/g) ?? []
}

/**
 * Refine adjacent del/add pairs to char granularity when both sides are small
 * enough for an exact char LCS. This is what makes single-word or
 * single-character edits read as such instead of whole-word replacements.
 */
function refineCharLevel(segments: DiffSegment[]): DiffSegment[] {
    const out: DiffSegment[] = []
    for (let k = 0; k < segments.length; k++) {
        const cur = segments[k]
        const next = segments[k + 1]
        if (
            cur.type === 'del' &&
            next?.type === 'add' &&
            cur.text.length <= MAX_CHAR_REFINE &&
            next.text.length <= MAX_CHAR_REFINE
        ) {
            out.push(...lcsDiff(cur.text.split(''), next.text.split('')))
            k++
            continue
        }
        out.push(cur)
    }
    // Merge any adjacent same-type segments introduced by refinement.
    const merged: DiffSegment[] = []
    for (const seg of out) {
        const last = merged[merged.length - 1]
        if (last && last.type === seg.type) last.text += seg.text
        else merged.push({ ...seg })
    }
    return merged
}

/**
 * Diff two strings into equal/add/del segments. `del` segments belong to the
 * baseline (a), `add` segments to the candidate (b). No length cap: quality
 * degrades from char -> word -> line -> block as inputs grow, never hangs.
 */
export function diffText(a: string, b: string): DiffSegment[] {
    if (a === b) return a ? [{ type: 'equal', text: a }] : []

    // Trim common prefix.
    let start = 0
    const minLen = Math.min(a.length, b.length)
    while (start < minLen && a[start] === b[start]) start++
    // Trim common suffix (without overlapping the prefix).
    let endA = a.length
    let endB = b.length
    while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
        endA--
        endB--
    }

    const prefix = a.slice(0, start)
    const suffix = a.slice(endA)
    const midA = a.slice(start, endA)
    const midB = b.slice(start, endB)

    let middle: DiffSegment[]
    const wa = wordTokens(midA)
    const wb = wordTokens(midB)
    if (wa.length * wb.length <= MAX_LCS_CELLS) {
        middle = refineCharLevel(lcsDiff(wa, wb))
    } else {
        const la = lineTokens(midA)
        const lb = lineTokens(midB)
        if (la.length * lb.length <= MAX_LCS_CELLS) {
            middle = refineCharLevel(lcsDiff(la, lb))
        } else {
            // Too large for an exact diff: one replace block, still lossless.
            middle = []
            if (midA) middle.push({ type: 'del', text: midA })
            if (midB) middle.push({ type: 'add', text: midB })
        }
    }

    const out: DiffSegment[] = []
    if (prefix) out.push({ type: 'equal', text: prefix })
    for (const seg of middle) {
        const last = out[out.length - 1]
        if (last && last.type === seg.type) last.text += seg.text
        else out.push({ ...seg })
    }
    if (suffix) {
        const last = out[out.length - 1]
        if (last && last.type === 'equal') last.text += suffix
        else out.push({ type: 'equal', text: suffix })
    }
    return out
}
