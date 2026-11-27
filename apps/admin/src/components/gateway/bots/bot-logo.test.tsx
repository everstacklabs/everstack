// @vitest-environment jsdom
// Bot logos: every mark the catalog points at ships with the app, is safe to
// serve, and unknown kinds fall back to the generic icon.

import { existsSync, readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'
import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { BOT_CATALOG } from './bot-catalog'
import { BOT_LOGOS, BotLogo } from './bot-logo'

const PUBLIC_DIR = join(process.cwd(), 'public')

// Kinds that show the generic icon on purpose. A new kind must either ship a
// mark or be added here.
const GENERIC_KINDS = ['nightly-openbot', 'custom']

afterEach(cleanup)

describe('bot logos', () => {
    it('gives every catalog kind a mark or an explicit generic icon', () => {
        for (const entry of BOT_CATALOG) {
            const hasLogo = Boolean(BOT_LOGOS[entry.id])
            expect(hasLogo, entry.id).toBe(!GENERIC_KINDS.includes(entry.id))
        }
    })

    it('ships every referenced file', () => {
        for (const [kind, asset] of Object.entries(BOT_LOGOS)) {
            expect(asset.src, kind).toMatch(/^\/bots\/[a-z0-9-]+\.(svg|png)$/)
            expect(existsSync(join(PUBLIC_DIR, asset.src)), asset.src).toBe(true)
        }
    })

    it('ships no file the catalog does not use', () => {
        const used = new Set(Object.values(BOT_LOGOS).map((asset) => asset.src))
        for (const file of readdirSync(join(PUBLIC_DIR, 'bots'))) {
            expect(used.has(`/bots/${file}`), file).toBe(true)
        }
    })

    it('ships SVGs with no scripts or outside references', () => {
        for (const file of readdirSync(join(PUBLIC_DIR, 'bots')).filter((name) => name.endsWith('.svg'))) {
            const svg = readFileSync(join(PUBLIC_DIR, 'bots', file), 'utf8')
            expect(svg, file).not.toMatch(/<script|<foreignObject|<image|\son[a-z]+=|javascript:|href=["']https?:/i)
        }
    })

    it('renders the mark for a known kind', () => {
        const { container } = render(<BotLogo kind="openclaw" />)
        expect(container.querySelector('[data-bot-logo="openclaw"] img')?.getAttribute('src')).toBe('/bots/openclaw.svg')
    })

    it('renders the generic icon for a kind without a mark', () => {
        for (const kind of ['custom', 'nightly-openbot', 'something-newer']) {
            const { container } = render(<BotLogo kind={kind} />)
            expect(container.querySelector('[data-bot-logo="generic"]'), kind).not.toBeNull()
            expect(container.querySelector('img'), kind).toBeNull()
            cleanup()
        }
    })
})
