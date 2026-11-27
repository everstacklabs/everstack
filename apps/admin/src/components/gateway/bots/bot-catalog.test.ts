import { describe, expect, it } from 'vitest'
import {
    BACKEND_SURFACES,
    BOT_CATALOG,
    KEY_PLACEHOLDER,
    defaultSurfaces,
    offeredSurfaces,
    renderSetup,
    type BotKind,
    type RenderedSetup,
    type Surface,
} from './bot-catalog'

const GATEWAY = 'https://gw.example.com'
const KEY = 'sk-test-0123456789abcdef'

function subsets<T>(items: T[]): T[][] {
    return items.reduce<T[][]>((acc, item) => acc.concat(acc.map((s) => [...s, item])), [[]])
}

function allText(setup: RenderedSetup): string[] {
    const out = [setup.kind.configLocation, setup.kind.apply, setup.kind.description, setup.kind.name]
    if (setup.merged) out.push(setup.merged.title, setup.merged.code, setup.merged.note ?? '')
    for (const section of setup.sections) {
        out.push(section.label, ...section.notes)
        for (const block of section.blocks) {
            out.push(block.title, ...block.caveats)
            if (block.type === 'code') out.push(block.code)
            if (block.type === 'values') out.push(block.intro, ...block.values.flatMap((v) => [v.label, v.value]))
            if (block.type === 'steps') out.push(...block.steps)
        }
    }
    return out
}

function codeBlocks(setup: RenderedSetup) {
    const blocks = setup.sections.flatMap((s) => s.blocks).filter((b) => b.type === 'code')
    return setup.merged ? [setup.merged, ...blocks] : blocks
}

// Every kind and every combination of the surfaces it offers.
const cases = BOT_CATALOG.flatMap((entry) =>
    subsets(offeredSurfaces(entry.id)).map((surfaces) => ({ kind: entry.id, surfaces })),
)

describe('bot catalog', () => {
    it('has exactly one entry per kind the gateway accepts', () => {
        const ids = BOT_CATALOG.map((e) => e.id).sort()
        expect(ids).toEqual((Object.keys(BACKEND_SURFACES) as BotKind[]).sort())
        expect(new Set(ids).size).toBe(ids.length)
    })

    it('never offers a surface the gateway refuses for the kind', () => {
        for (const entry of BOT_CATALOG) {
            const offered = offeredSurfaces(entry.id)
            expect(offered.length).toBeGreaterThan(0)
            for (const s of offered) expect(BACKEND_SURFACES[entry.id]).toContain(s)
            for (const s of defaultSurfaces(entry.id)) expect(offered).toContain(s)
        }
    })

    it('uses the agreed defaults', () => {
        expect(defaultSurfaces('openclaw')).toEqual(['inference', 'mcp', 'telemetry'])
        expect(defaultSurfaces('nanobot')).toEqual(['inference', 'mcp'])
        expect(defaultSurfaces('grok-bot')).toEqual(['mcp'])
        expect(defaultSurfaces('meta-muse')).toEqual(['mcp'])
        expect(defaultSurfaces('grok-build')).toEqual(['inference', 'mcp'])
        expect(offeredSurfaces('grok-build')).toEqual(['inference', 'mcp'])
        expect(offeredSurfaces('meta-muse')).toEqual(['mcp'])
        expect(offeredSurfaces('grok-bot')).toEqual(['mcp'])
        for (const entry of BOT_CATALOG) expect(defaultSurfaces(entry.id)).not.toContain('a2a')
    })

    it('leaves no template placeholder unreplaced, with or without a key', () => {
        for (const { kind, surfaces } of cases) {
            for (const apiKey of [KEY, undefined]) {
                const setup = renderSetup(kind, surfaces, { gatewayUrl: `${GATEWAY}/`, apiKey, model: 'gpt-x' })
                expect(setup).not.toBeNull()
                for (const text of allText(setup!)) {
                    expect(text, `${kind} ${surfaces.join(',')}`).not.toMatch(/\{\{\s*[A-Za-z_]+\s*\}\}/)
                    expect(text).not.toContain('\u2014')
                    expect(text).not.toContain(`${GATEWAY}//`)
                }
            }
        }
    })

    it('fills the key when given and the placeholder when not', () => {
        for (const { kind, surfaces } of cases) {
            if (surfaces.length === 0) continue
            const withKey = allText(renderSetup(kind, surfaces, { gatewayUrl: GATEWAY, apiKey: KEY })!).join('\n')
            const withoutKey = allText(renderSetup(kind, surfaces, { gatewayUrl: GATEWAY })!).join('\n')
            expect(withoutKey).not.toContain(KEY)
            if (withKey.includes(KEY)) expect(withoutKey).toContain(KEY_PLACEHOLDER)
            expect(withKey).toContain(GATEWAY)
        }
    })

    it('shows something for every enabled surface', () => {
        for (const { kind, surfaces } of cases) {
            const setup = renderSetup(kind, surfaces, { gatewayUrl: GATEWAY })!
            for (const section of setup.sections) {
                expect(section.blocks.length + section.notes.length + (setup.merged ? 1 : 0)).toBeGreaterThan(0)
            }
            expect(setup.sections.map((s) => s.surface)).toEqual(surfaces)
        }
    })

    it('renders JSON that parses and TOML without repeated tables', () => {
        for (const { kind, surfaces } of cases) {
            const setup = renderSetup(kind, surfaces, { gatewayUrl: GATEWAY, apiKey: KEY })!
            for (const block of codeBlocks(setup)) {
                if (block.language === 'json') expect(() => JSON.parse(block.code)).not.toThrow()
                if (block.language === 'toml') {
                    const headers = block.code.split('\n').filter((l) => /^\[[^[]/.test(l.trim()))
                    expect(new Set(headers).size, `${kind} ${surfaces.join(',')}`).toBe(headers.length)
                }
            }
        }
    })

    it('builds the merged OpenClaw file from the enabled surfaces only', () => {
        const setup = renderSetup('openclaw', ['inference', 'mcp'], { gatewayUrl: GATEWAY, apiKey: KEY, model: 'm1' })!
        expect(setup.merged?.code).toContain('baseUrl: "https://gw.example.com/v1"')
        expect(setup.merged?.code).toContain('transport: "streamable-http"')
        expect(setup.merged?.code).not.toContain('diagnostics')
        expect(setup.merged?.code).toContain('primary: "everstack/m1"')
        // The merged parts are not repeated below; their caveats still are.
        const mcp = setup.sections.find((s) => s.surface === 'mcp')!
        expect(mcp.notes.join(' ')).toMatch(/streamable-http/)
    })

    // Each of these came from running the real bot with the rendered config.
    it('tells OpenClaw users to install the telemetry plugin, beside the one-file config and under Traces', () => {
        const install = 'openclaw plugins install @openclaw/diagnostics-otel'
        const withTraces = renderSetup('openclaw', ['inference', 'mcp', 'telemetry'], { gatewayUrl: GATEWAY })!
        expect(withTraces.merged?.note).toContain(install)
        const traces = withTraces.sections.find((s) => s.surface === 'telemetry')!
        expect(traces.blocks.some((b) => b.type === 'code' && b.code === install)).toBe(true)
        const withoutTraces = renderSetup('openclaw', ['inference', 'mcp'], { gatewayUrl: GATEWAY })!
        expect(withoutTraces.merged?.note).not.toContain('plugins install')
        const tracesOnly = renderSetup('openclaw', ['telemetry'], { gatewayUrl: GATEWAY })!
        expect(allText(tracesOnly).join('\n')).toContain(install)
    })

    it('makes the OpenClaw Anthropic block usable as pasted', () => {
        const setup = renderSetup('openclaw', ['inference'], { gatewayUrl: GATEWAY, model: 'some-model' })!
        const alt = codeBlocks(setup).find((b) => b.title.startsWith('Anthropic-compatible'))!
        expect(alt.code).toContain('primary: "everstack-anthropic/some-model"')
        expect(alt.code).toContain(`baseUrl: "${GATEWAY}"`)
    })

    it('installs the Hermes trace plugin under its catalog name', () => {
        const text = allText(renderSetup('hermes', ['telemetry'], { gatewayUrl: GATEWAY })!).join('\n')
        expect(text).toContain('hermes plugins install hermes_otel')
        expect(text).not.toContain('hermes-otel')
        expect(text).toContain('hermes gateway restart')
    })

    it('matches the surfaces the gateway accepts per kind', () => {
        // Hand copy of internal/bots/http.go kindSurfaces.
        expect(BACKEND_SURFACES['grok-build']).toEqual(['inference', 'mcp'])
        expect(BACKEND_SURFACES['grok-bot']).toEqual(['mcp'])
        expect(BACKEND_SURFACES['meta-muse']).toEqual(['mcp'])
    })

    it('gives Grok Build a custom model provider with exact keys', () => {
        const setup = renderSetup('grok-build', ['inference', 'mcp'], { gatewayUrl: GATEWAY, apiKey: KEY, model: 'm1' })!
        expect(setup.sections.map((s) => s.surface)).toEqual(['inference', 'mcp'])
        const code = (setup.sections[0].blocks[0] as { code: string }).code
        expect(code).toContain('[model.everstack]')
        expect(code).toContain('model = "m1"')
        expect(code).toContain('base_url = "https://gw.example.com/v1"')
        expect(code).toContain(`api_key = "${KEY}"`)
        expect(code).toContain('api_backend = "chat_completions"')
        expect(code).toContain('[models]\ndefault = "everstack"')
    })

    it('ignores surfaces the kind does not offer', () => {
        const setup = renderSetup('grok-bot', ['inference', 'mcp', 'a2a'] as Surface[], { gatewayUrl: GATEWAY })!
        expect(setup.sections.map((s) => s.surface)).toEqual(['mcp'])
        expect(renderSetup('unknown', ['mcp'], { gatewayUrl: GATEWAY })).toBeNull()
    })
})
