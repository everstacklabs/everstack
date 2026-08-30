export interface MentionToken {
    /** Index of the '@' character in the text */
    start: number
    /** Index after the filter text */
    end: number
    /** Text typed after '@' (the filter query) */
    filter: string
}

const TRAILING_PUNCTUATION_RE = /[.,!?;:)\]}>"'`]+$/

function isBoundary(ch: string | undefined): boolean {
    if (!ch) return true
    return /\s|[([{"'`]/.test(ch)
}

function isTerminator(ch: string): boolean {
    return /\s|[)\]}>,;:!?'"`]/.test(ch)
}

function trimTrailingPunctuation(value: string): string {
    return value.replace(TRAILING_PUNCTUATION_RE, '')
}

export function isPathLikeMentionFilter(filter: string): boolean {
    const value = filter.trim()
    if (!value) return false
    return (
        value.startsWith('/') ||
        value.startsWith('./') ||
        value.startsWith('../') ||
        value.includes('/')
    )
}

export function extractMentionTokens(text: string): MentionToken[] {
    const tokens: MentionToken[] = []
    const rawMentionRe = /@(\S+)/g

    for (const match of text.matchAll(rawMentionRe)) {
        const start = match.index ?? 0
        const prevChar = start > 0 ? text[start - 1] : undefined
        if (!isBoundary(prevChar)) continue

        const rawMention = match[1]
        const trimmed = trimTrailingPunctuation(rawMention)
        if (!trimmed) continue

        const mentionEnd = start + 1 + trimmed.length
        tokens.push({
            start,
            end: mentionEnd,
            filter: trimmed,
        })
    }

    return tokens
}

/**
 * Parses a mention token from text at the given cursor position.
 *
 * Rules:
 * - Scans backwards from cursor to find the nearest '@'
 * - Triggers when '@' is at position 0 or preceded by a mention boundary
 *   (whitespace or common opening punctuation like `(`, `[`, `"`, `'`)
 * - Does not trigger for emails (e.g. `email@test.com` — '@' preceded by letters)
 * - Returns null if filter contains a space (user finished selecting)
 * - Supports multiple mentions: only the active mention near cursor matters
 */
export function parseMention(text: string, cursor: number): MentionToken | null {
    // Scan backwards from cursor to find '@'
    for (let i = Math.min(cursor, text.length) - 1; i >= 0; i--) {
        const ch = text[i]

        // If we hit a hard token boundary before finding '@', mention is inactive.
        if (isTerminator(ch)) return null

        if (ch === '@') {
            // '@' must be at start or preceded by a mention boundary.
            if (!isBoundary(text[i - 1])) return null

            const filter = text.slice(i + 1, cursor)

            // Filter must not contain spaces (user finished selecting)
            if (filter.includes(' ')) return null

            return {
                start: i,
                end: cursor,
                filter,
            }
        }
    }

    return null
}
