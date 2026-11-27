// @vitest-environment jsdom

import { describe, it, expect, afterEach } from 'vitest'
import { cleanup, render } from '@testing-library/react'
import { MentionText } from './mention-text'

// The full mention path lives in the Tooltip (masked in session replay), not
// in a title or aria-label, which replay would record verbatim.
function getBadge(text: string): HTMLElement {
    const badge = Array.from(document.querySelectorAll<HTMLElement>('[data-mention]')).find((el) =>
        el.textContent?.includes(text),
    )
    if (!badge) throw new Error(`no mention badge containing ${text}`)
    expect(badge.hasAttribute('title')).toBe(false)
    expect(badge.hasAttribute('aria-label')).toBe(false)
    return badge
}

describe('MentionText', () => {
    afterEach(cleanup)

    it('renders a file mention badge in the middle of text', () => {
        const { container } = render(<MentionText>testing the @/changelog.yaml file</MentionText>)

        const badge = getBadge('changelog.yaml')
        expect(badge).toBeDefined()
        expect(badge.textContent).toContain('changelog.yaml')
        expect(container.textContent).toContain('testing the changelog.yaml file')
    })

    it('keeps trailing punctuation outside the badge', () => {
        const { container } = render(<MentionText>see @/changelog.yaml, please</MentionText>)

        const badge = getBadge('changelog.yaml')
        expect(badge.textContent).toContain('changelog.yaml')
        expect(container.textContent).toContain('changelog.yaml, please')
    })

    it('supports mentions preceded by quotes', () => {
        render(<MentionText>use &quot;@/changelog.yaml&quot; now</MentionText>)

        const badge = getBadge('changelog.yaml')
        expect(badge).toBeDefined()
    })

    it('renders folder references as badges', () => {
        render(<MentionText>open @/docs and continue</MentionText>)

        const badge = getBadge('docs')
        expect(badge).toBeDefined()
        expect(badge.textContent).toContain('docs')
    })

    it('composer variant keeps full mention text for caret alignment', () => {
        render(<MentionText variant="composer">open @/docs now</MentionText>)

        const badge = getBadge('docs')
        expect(badge).toBeDefined()
        expect(badge.textContent).toContain('docs')
        expect(badge.textContent).not.toContain('/docs')
    })

    it('renders @folder-style references as badges', () => {
        render(<MentionText>open @folder now</MentionText>)

        const badge = getBadge('folder')
        expect(badge).toBeDefined()
        expect(badge.textContent).toContain('folder')
    })
})
