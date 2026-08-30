import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

describe('admin sidebar branding', () => {
    it('uses the compact logo mark', () => {
        const source = readFileSync(
            new URL('./sidebar-nav.tsx', import.meta.url),
            'utf8',
        )

        expect(source).toContain('<EverstackLogo size="sm" />')
    })
})
