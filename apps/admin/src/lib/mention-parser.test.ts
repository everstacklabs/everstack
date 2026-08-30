import { describe, it, expect } from 'vitest'
import { parseMention, extractMentionTokens, isPathLikeMentionFilter } from './mention-parser'

describe('parseMention', () => {
    it('detects @src at cursor 4', () => {
        const result = parseMention('@src', 4)
        expect(result).toEqual({ start: 0, end: 4, filter: 'src' })
    })

    it('detects @pa after text with space', () => {
        const result = parseMention('hello @pa', 9)
        expect(result).toEqual({ start: 6, end: 9, filter: 'pa' })
    })

    it('returns null for email@test.com', () => {
        const result = parseMention('email@test.com', 14)
        expect(result).toBeNull()
    })

    it('returns null for word@mid', () => {
        const result = parseMention('word@mid', 8)
        expect(result).toBeNull()
    })

    it('returns null when filter contains space (@ followed by space)', () => {
        const result = parseMention('@ ', 2)
        expect(result).toBeNull()
    })

    it('detects @ at start with empty filter', () => {
        const result = parseMention('@', 1)
        expect(result).toEqual({ start: 0, end: 1, filter: '' })
    })

    it('handles @ preceded by newline', () => {
        const result = parseMention('line\n@fi', 8)
        expect(result).toEqual({ start: 5, end: 8, filter: 'fi' })
    })

    it('handles multiple mentions — returns second mention near cursor', () => {
        const result = parseMention('@file1 text @fi', 15)
        expect(result).toEqual({ start: 12, end: 15, filter: 'fi' })
    })

    it('returns null when cursor is after completed mention', () => {
        const result = parseMention('@file1 ', 7)
        expect(result).toBeNull()
    })

    it('returns null when no @ in text', () => {
        const result = parseMention('hello world', 11)
        expect(result).toBeNull()
    })

    it('handles @ preceded by tab', () => {
        const result = parseMention('hello\t@src', 10)
        expect(result).toEqual({ start: 6, end: 10, filter: 'src' })
    })

    it('detects mention with path-like filter', () => {
        const result = parseMention('@/repo/src', 10)
        expect(result).toEqual({ start: 0, end: 10, filter: '/repo/src' })
    })

    it('detects mention preceded by opening quote', () => {
        const result = parseMention('see "@/repo/src', 15)
        expect(result).toEqual({ start: 5, end: 15, filter: '/repo/src' })
    })

    it('returns null when cursor is after trailing punctuation', () => {
        const result = parseMention('see @/repo/src,', 15)
        expect(result).toBeNull()
    })
})

describe('extractMentionTokens', () => {
    it('extracts normalized mentions and strips trailing punctuation', () => {
        const tokens = extractMentionTokens('use @alpha, then @/repo/src.')
        expect(tokens).toEqual([
            { start: 4, end: 10, filter: 'alpha' },
            { start: 17, end: 27, filter: '/repo/src' },
        ])
    })

    it('ignores email-like tokens', () => {
        const tokens = extractMentionTokens('mail me at email@test.com')
        expect(tokens).toEqual([])
    })
})

describe('isPathLikeMentionFilter', () => {
    it('detects slash-based file references', () => {
        expect(isPathLikeMentionFilter('/repo/src')).toBe(true)
        expect(isPathLikeMentionFilter('./src')).toBe(true)
        expect(isPathLikeMentionFilter('../src')).toBe(true)
        expect(isPathLikeMentionFilter('src/utils')).toBe(true)
    })

    it('does not treat simple aliases as path-like', () => {
        expect(isPathLikeMentionFilter('researcher')).toBe(false)
        expect(isPathLikeMentionFilter('agent-qa')).toBe(false)
        expect(isPathLikeMentionFilter('')).toBe(false)
    })
})
