// @vitest-environment jsdom
// Bots page: list states, the connect flow, key handling, rotate and remove.

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { describeBotError, BotsApiError } from '@/hooks/gateway/use-bots'
import { BotsPanel } from './bots-panel'

vi.mock('@/hooks/auth', () => ({
    useSession: () => ({ data: { user: { organizations: [{ id: 'org1' }] } } }),
}))
vi.mock('@/lib/api-url', () => ({ getApiBaseUrl: () => 'https://gw.test' }))
vi.mock('@/hooks/deployments/use-gateway-models', () => ({ useGatewayModels: () => ({ data: [] }) }))
vi.mock('@/hooks/deployments/use-agents', () => ({ useAgents: () => ({ data: [] }) }))

const KEY = 'sk-live-0123456789abcdef'
const ROTATED = 'sk-live-rotated-fedcba9876'

type Bot = {
    id: string
    name: string
    kind: string
    surfaces: string[]
    api_key_id: string
    key_hint: string
    remote_agent: string | null
    rate_limit_rpm: number | null
    usage: { calls_30d: number; by_surface: Record<string, number> }
    created_at: string
    last_seen_at: string | null
}

let bots: Bot[]
let listStatus: number
let remotes: { id: string; name: string }[]
const writeText = vi.fn(async (_text: string) => {})
let postBots: (body: any) => Response
const calls: { method: string; path: string; body?: any }[] = []

const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

const bot = (over: Partial<Bot>): Bot => ({
    id: 'b1',
    name: 'openclaw-1',
    kind: 'openclaw',
    surfaces: ['inference', 'mcp', 'telemetry'],
    api_key_id: 'key-0001',
    key_hint: 'sk-...cdef',
    remote_agent: null,
    rate_limit_rpm: null,
    usage: { calls_30d: 0, by_surface: { inference: 0, mcp: 0, telemetry: 0, a2a: 0 } },
    created_at: '2026-11-01T00:00:00Z',
    last_seen_at: null,
    ...over,
})

let client: QueryClient
function renderPage() {
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
        <QueryClientProvider client={client}>
            <BotsPanel />
        </QueryClientProvider>,
    )
}

/** Everything the app could have persisted the key into. */
function persistedState(): string {
    const queries = client.getQueryCache().getAll().map((q) => q.state)
    const mutations = client.getMutationCache().getAll().map((m) => m.state)
    return [
        JSON.stringify(queries),
        JSON.stringify(mutations),
        JSON.stringify({ ...window.localStorage }),
        JSON.stringify({ ...window.sessionStorage }),
        window.location.href,
    ].join('\n')
}

beforeEach(() => {
    bots = []
    listStatus = 200
    calls.length = 0
    remotes = []
    writeText.mockClear()
    // jsdom has no clipboard.
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true })
    postBots = (body) => {
        const created = bot({
            id: 'new',
            name: body.name,
            kind: body.kind,
            surfaces: body.surfaces,
            rate_limit_rpm: body.rate_limit_rpm ?? null,
        })
        bots = [...bots, created]
        return json(201, { bot: created, api_key: KEY })
    }
    vi.stubGlobal(
        'ResizeObserver',
        class {
            observe() {}
            unobserve() {}
            disconnect() {}
        },
    )
    vi.stubGlobal('fetch', async (url: string, init?: RequestInit) => {
        const method = init?.method ?? 'GET'
        const path = url.replace('https://gw.test', '')
        const body = init?.body ? JSON.parse(String(init.body)) : undefined
        calls.push({ method, path, body })
        if (path === '/api/bots' && method === 'GET') {
            return listStatus === 200 ? json(200, { bots }) : json(listStatus, { error: 'unauthorized' })
        }
        if (path === '/api/bots' && method === 'POST') return postBots(body)
        const rotate = /^\/api\/bots\/([^/]+)\/rotate-key$/.exec(path)
        if (rotate) {
            const current = bots.find((b) => b.id === rotate[1])!
            return json(200, { bot: { ...current, key_hint: 'sk-...9876', api_key_id: 'key-rotated' }, api_key: ROTATED })
        }
        const item = /^\/api\/bots\/([^/]+)$/.exec(path)
        if (item && method === 'PATCH') {
            const current = bots.find((b) => b.id === item[1])!
            return json(200, { bot: { ...current, ...body } })
        }
        if (item && method === 'DELETE') {
            bots = bots.filter((b) => b.id !== item[1])
            return new Response(null, { status: 204 })
        }
        if (path === '/api/interop/remotes') return json(200, { remotes })
        if (path === '/api/interop/a2a/published') return json(200, { published: {} })
        return json(404, { error: 'not_found' })
    })
})
afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
})

describe('bots list', () => {
    it('explains bot connections when there are none', async () => {
        renderPage()
        expect(await screen.findByText(/A bot connection gives one personal bot/)).toBeDefined()
        expect(screen.getAllByRole('button', { name: 'Connect a bot' })).toHaveLength(1)
    })

    it('shows each bot with its type, surfaces, key hint, last seen and remote', async () => {
        bots = [
            bot({ id: 'a', name: 'home-claw' }),
            bot({
                id: 'b',
                name: 'grok',
                kind: 'grok-bot',
                surfaces: ['mcp'],
                key_hint: 'sk-...1111',
                last_seen_at: new Date(Date.now() - 3 * 3600 * 1000).toISOString(),
            }),
            bot({ id: 'c', name: 'zc', kind: 'zeroclaw', surfaces: ['mcp', 'a2a'], remote_agent: 'zc-remote' }),
        ]
        renderPage()
        const list = await screen.findByRole('list', { name: 'Bots' })
        const rows = within(list).getAllByRole('listitem')
        expect(rows).toHaveLength(3)
        expect(rows[0].textContent).toContain('OpenClaw')
        expect(rows[0].textContent).toContain('Models')
        expect(rows[0].textContent).toContain('Traces')
        expect(rows[0].textContent).toContain('Not connected yet')
        expect(rows[1].textContent).toContain('Grok Bot')
        expect(rows[1].textContent).toContain('sk-...1111')
        expect(rows[1].textContent).toContain('Last seen 3 hours ago')
        expect(rows[1].textContent).not.toContain('Models')
        expect(rows[2].textContent).toContain('Agents')
        expect(rows[2].textContent).toContain('Remote agent: zc-remote')
    })

    it('shows 30-day usage and the rate limit in one quiet line', async () => {
        const seen = new Date(Date.now() - 60 * 1000).toISOString()
        bots = [
            bot({
                id: 'a',
                name: 'busy',
                last_seen_at: seen,
                rate_limit_rpm: 120,
                usage: { calls_30d: 1204, by_surface: { inference: 1000, mcp: 200, telemetry: 4, a2a: 0 } },
            }),
            bot({ id: 'b', name: 'never' }),
            bot({ id: 'c', name: 'quiet', last_seen_at: seen }),
            bot({
                id: 'd',
                name: 'once',
                last_seen_at: seen,
                usage: { calls_30d: 1, by_surface: { inference: 0, mcp: 1, telemetry: 0, a2a: 0 } },
            }),
        ]
        renderPage()
        const rows = within(await screen.findByRole('list', { name: 'Bots' })).getAllByRole('listitem')
        const usage = within(rows[0]).getByText(/1,204 calls in 30 days/)
        expect(usage.getAttribute('title')).toBe('Models 1,000, Tools 200, Traces 4')
        expect(rows[0].textContent).toContain('120 requests/min')
        // Never seen and no calls: nothing to show.
        expect(rows[1].textContent).not.toMatch(/calls? in 30 days/)
        expect(rows[1].textContent).not.toContain('requests/min')
        // Seen, but nothing counted in the window.
        expect(rows[2].textContent).toContain('0 calls in 30 days')
        expect(within(rows[2]).getByText(/0 calls in 30 days/).getAttribute('title')).toBeNull()
        expect(rows[3].textContent).toContain('1 call in 30 days')
        expect(within(rows[3]).getByText(/1 call in 30 days/).getAttribute('title')).toBe('Tools 1')
    })

    it('asks to sign in when the session is gone', async () => {
        listStatus = 401
        renderPage()
        expect(await screen.findByText(/Your session has ended/)).toBeDefined()
        expect(screen.getByRole('link', { name: 'Sign in' }).getAttribute('href')).toBe('/login')
        expect(screen.queryByText(/A bot connection gives/)).toBeNull()
    })
})

describe('connect a bot', () => {
    it('shows the key once with the filled-in OpenClaw config, and forgets it on close', async () => {
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
        const dialog = await screen.findByRole('dialog')
        expect((within(dialog).getByLabelText('Name') as HTMLInputElement).value).toBe('openclaw-1')
        expect(within(dialog).getByRole('radio', { name: /OpenClaw/ }).getAttribute('aria-checked')).toBe('true')
        expect(within(dialog).getByRole('checkbox', { name: /Models/ }).getAttribute('aria-checked')).toBe('true')
        expect(within(dialog).getByRole('checkbox', { name: /Agents/ }).getAttribute('aria-checked')).toBe('false')
        // Step 2 opens at the top, where the key is, however far step 1 was scrolled.
        const body = dialog.querySelector('[data-slot="sheet-body"]') as HTMLElement
        body.scrollTop = 300
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Connect bot' }))
        })
        const post = calls.find((c) => c.method === 'POST' && c.path === '/api/bots')!
        expect(post.body).toEqual({ name: 'openclaw-1', kind: 'openclaw', surfaces: ['inference', 'mcp', 'telemetry'] })
        await screen.findByText(/You will not see this key again/)
        expect((screen.getByRole('dialog').querySelector('[data-slot="sheet-body"]') as HTMLElement).scrollTop).toBe(0)

        expect(await screen.findByText(/You will not see this key again/)).toBeDefined()
        const setup = screen.getByRole('dialog')
        expect(within(setup).getByText(KEY)).toBeDefined()
        const text = setup.textContent ?? ''
        expect(text).toContain('baseUrl: "https://gw.test/v1"')
        expect(text).toContain(`apiKey: "${KEY}"`)
        expect(text).toContain('url: "https://gw.test/mcp"')
        expect(text).toContain(`headers: { Authorization: "Bearer ${KEY}" }`)
        expect(text).toContain('endpoint: "https://gw.test"')
        expect(text).toMatch(/OpenClaw appends `?\/v1\/traces/)
        expect(text).not.toMatch(/\{\{[A-Z_]+\}\}/)
        expect(persistedState()).not.toContain(KEY)

        // Closing without copying asks first.
        fireEvent.click(within(setup).getByRole('button', { name: 'Done' }))
        const confirm = await screen.findByRole('alertdialog')
        expect(confirm.textContent).toMatch(/You will not see this key again/)
        await act(async () => {
            fireEvent.click(within(confirm).getByRole('button', { name: 'Close' }))
        })
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        expect(document.body.textContent).not.toContain(KEY)
        expect(persistedState()).not.toContain(KEY)

        // Setup later shows the placeholder, never the key.
        fireEvent.click(await screen.findByRole('button', { name: 'Setup openclaw-1' }))
        const later = await screen.findByRole('dialog')
        expect(later.textContent).toContain('apiKey: "<your bot key>"')
        expect(later.textContent).not.toContain(KEY)
    })

    it('closes without asking once the key was copied, but not after copying only the URL', async () => {
        const openStep2 = async () => {
            fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
            const dialog = await screen.findByRole('dialog')
            await act(async () => {
                fireEvent.click(within(dialog).getByRole('button', { name: 'Connect bot' }))
            })
            await screen.findByText(/You will not see this key again/)
            return screen.getByRole('dialog')
        }
        renderPage()
        let setup = await openStep2()
        await act(async () => {
            fireEvent.click(within(setup).getByRole('button', { name: 'Copy gateway URL' }))
        })
        expect(writeText).toHaveBeenCalledWith('https://gw.test')
        fireEvent.click(within(setup).getByRole('button', { name: 'Done' }))
        expect(await screen.findByRole('alertdialog')).toBeDefined()
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Keep open' }))
        })
        await act(async () => {
            fireEvent.click(within(setup).getByRole('button', { name: 'Copy bot key' }))
        })
        expect(writeText).toHaveBeenLastCalledWith(KEY)
        fireEvent.click(within(setup).getByRole('button', { name: 'Done' }))
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        expect(screen.queryByRole('alertdialog')).toBeNull()
        expect(document.body.textContent).not.toContain(KEY)
    })

    it('offers only Tools for Grok Bot', async () => {
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
        const dialog = await screen.findByRole('dialog')
        fireEvent.click(within(dialog).getByRole('radio', { name: /Grok Bot/ }))
        expect(within(dialog).getByText('Hosted by xAI')).toBeDefined()
        expect(within(dialog).getAllByRole('checkbox')).toHaveLength(1)
        expect(within(dialog).getByRole('checkbox', { name: /Tools/ }).getAttribute('aria-checked')).toBe('true')
        expect(within(dialog).queryByRole('checkbox', { name: /Models/ })).toBeNull()
        expect((within(dialog).getByLabelText('Name') as HTMLInputElement).value).toBe('grok-bot-1')
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Connect bot' }))
        })
        const post = calls.find((c) => c.method === 'POST' && c.path === '/api/bots')!
        expect(post.body.surfaces).toEqual(['mcp'])
        const setup = await screen.findByRole('dialog')
        await within(setup).findByText(/You will not see this key again/)
        expect(setup.textContent).toMatch(/We have not confirmed the exact setting names for this version of Grok Bot/)
        expect(setup.textContent).toMatch(/do not document a static auth header/)
    })

    it('offers Models and Tools for Grok Build and sends a rate limit', async () => {
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
        const dialog = await screen.findByRole('dialog')
        fireEvent.click(within(dialog).getByRole('radio', { name: /Grok Build/ }))
        expect(within(dialog).getAllByRole('checkbox')).toHaveLength(2)
        expect(within(dialog).getByRole('checkbox', { name: /Models/ }).getAttribute('aria-checked')).toBe('true')
        expect(within(dialog).getByRole('checkbox', { name: /Tools/ }).getAttribute('aria-checked')).toBe('true')
        const limit = within(dialog).getByLabelText('Rate limit (optional)')
        const connect = within(dialog).getByRole('button', { name: 'Connect bot' }) as HTMLButtonElement
        for (const bad of ['0', '100001', '12.5', 'abc', '-3']) {
            fireEvent.change(limit, { target: { value: bad } })
            expect(connect.disabled, bad).toBe(true)
            expect(within(dialog).getByText(/whole number from 1 to 100,000/)).toBeDefined()
        }
        fireEvent.change(limit, { target: { value: ' 120 ' } })
        expect(connect.disabled).toBe(false)
        await act(async () => {
            fireEvent.click(connect)
        })
        const post = calls.find((c) => c.method === 'POST' && c.path === '/api/bots')!
        expect(post.body).toEqual({ name: 'grok-build-1', kind: 'grok-build', surfaces: ['inference', 'mcp'], rate_limit_rpm: 120 })
        const setup = await screen.findByRole('dialog')
        await within(setup).findByText(/You will not see this key again/)
        expect(setup.textContent).toContain('[model.everstack]')
        expect(setup.textContent).toContain(`api_key = "${KEY}"`)
    })

    it('explains a rejected rate limit', async () => {
        postBots = () => json(400, { error: 'invalid_rate_limit' })
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
        const dialog = await screen.findByRole('dialog')
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Connect bot' }))
        })
        expect(await within(dialog).findByText(/Set the rate limit to a whole number/)).toBeDefined()
    })

    it('explains a taken name', async () => {
        postBots = () => json(409, { error: 'name_taken' })
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Connect a bot' }))
        const dialog = await screen.findByRole('dialog')
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Connect bot' }))
        })
        expect(await within(dialog).findByText(/A bot with this name already exists\./)).toBeDefined()
    })
})

describe('row actions', () => {
    it('makes a new key after confirming and shows it once', async () => {
        bots = [bot({ id: 'b1', name: 'home-claw' })]
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'New key for home-claw' }))
        const confirm = await screen.findByRole('alertdialog')
        expect(confirm.textContent).toMatch(/current key stops working within 30 seconds/)
        await act(async () => {
            fireEvent.click(within(confirm).getByRole('button', { name: 'Make new key' }))
        })
        expect(calls.some((c) => c.method === 'POST' && c.path === '/api/bots/b1/rotate-key')).toBe(true)
        const dialog = await screen.findByRole('dialog')
        expect(within(dialog).getByText(ROTATED)).toBeDefined()
        expect(dialog.textContent).toContain(`apiKey: "${ROTATED}"`)
        expect(persistedState()).not.toContain(ROTATED)

        // The trace attribute uses the new key's id, and copying it is not copying the key.
        expect(dialog.textContent).toContain('everstack.api_key_id')
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Copy everstack.api_key_id value' }))
        })
        expect(writeText).toHaveBeenLastCalledWith('key-rotated')
        fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }))
        expect(await screen.findByRole('alertdialog')).toBeDefined()
    })

    it('shows the trace attribute in setup only when Traces is on', async () => {
        bots = [
            bot({ id: 'b1', name: 'with-traces', api_key_id: 'key-abc' }),
            bot({ id: 'b2', name: 'no-traces', surfaces: ['mcp'], api_key_id: 'key-def' }),
        ]
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Setup with-traces' }))
        let dialog = await screen.findByRole('dialog')
        expect(within(dialog).getByText(/resource attribute/).textContent).toContain('everstack.api_key_id')
        expect(within(dialog).getByText('key-abc')).toBeDefined()
        fireEvent.click(within(dialog).getByRole('button', { name: 'Done' }))
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        fireEvent.click(await screen.findByRole('button', { name: 'Setup no-traces' }))
        dialog = await screen.findByRole('dialog')
        expect(dialog.textContent).not.toContain('everstack.api_key_id')
    })

    it('removes a bot after confirming that its key is revoked', async () => {
        bots = [bot({ id: 'b1', name: 'home-claw' })]
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Remove home-claw' }))
        const confirm = await screen.findByRole('alertdialog')
        expect(confirm.textContent).toMatch(/key is revoked/)
        await act(async () => {
            fireEvent.click(within(confirm).getByRole('button', { name: 'Remove' }))
        })
        expect(calls.some((c) => c.method === 'DELETE' && c.path === '/api/bots/b1')).toBe(true)
        expect(await screen.findByText(/A bot connection gives one personal bot/)).toBeDefined()
        expect(screen.queryByRole('alertdialog')).toBeNull()
    })
})

describe('edit', () => {
    it('sends only what changed, and null to unlink the remote', async () => {
        const errors = vi.spyOn(console, 'error')
        remotes = [{ id: 'r1', name: 'claw-a2a' }]
        bots = [bot({ id: 'b1', name: 'home-claw', remote_agent: 'claw-a2a' })]
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Edit home-claw' }))
        let dialog = await screen.findByRole('dialog')
        await within(dialog).findByRole('combobox', { name: /Remote agent/ })
        fireEvent.change(within(dialog).getByLabelText('Name'), { target: { value: 'house-claw' } })
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
        })
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        expect(calls.filter((c) => c.method === 'PATCH').map((c) => c.body)).toEqual([{ name: 'house-claw' }])

        fireEvent.click(await screen.findByRole('button', { name: 'Edit home-claw' }))
        dialog = await screen.findByRole('dialog')
        expect((within(dialog).getByLabelText('Name') as HTMLInputElement).value).toBe('home-claw')
        const native = dialog.querySelector('select') as HTMLSelectElement
        fireEvent.change(native, { target: { value: '__none__' } })
        await act(async () => {
            fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
        })
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        expect(calls.filter((c) => c.method === 'PATCH').map((c) => c.body)[1]).toEqual({ remote_agent: null })
        const reactWarnings = errors.mock.calls.filter((args) => /Warning|Cannot update/.test(String(args[0])))
        expect(reactWarnings).toEqual([])
        errors.mockRestore()
    })

    it('sends the rate limit only when it changed, and null to clear it', async () => {
        bots = [bot({ id: 'b1', name: 'home-claw', rate_limit_rpm: 60 })]
        renderPage()
        const editAndSave = async (value?: string) => {
            fireEvent.click(await screen.findByRole('button', { name: 'Edit home-claw' }))
            const dialog = await screen.findByRole('dialog')
            const field = within(dialog).getByLabelText('Rate limit (optional)') as HTMLInputElement
            expect(field.value).toBe(String(bots[0].rate_limit_rpm ?? ''))
            if (value !== undefined) fireEvent.change(field, { target: { value } })
            await act(async () => {
                fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
            })
            await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull())
        }
        const patches = () => calls.filter((c) => c.method === 'PATCH').map((c) => c.body)

        await editAndSave('60')
        expect(patches()).toEqual([])
        await editAndSave('120')
        expect(patches()).toEqual([{ rate_limit_rpm: 120 }])
        bots = [{ ...bots[0], rate_limit_rpm: 120 }]
        await act(async () => {
            await client.invalidateQueries()
        })
        await editAndSave('')
        expect(patches()[1]).toEqual({ rate_limit_rpm: null })

        fireEvent.click(await screen.findByRole('button', { name: 'Edit home-claw' }))
        const dialog = await screen.findByRole('dialog')
        fireEvent.change(within(dialog).getByLabelText('Rate limit (optional)'), { target: { value: '0' } })
        expect((within(dialog).getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true)
    })
})

describe('error copy', () => {
    it('maps codes to plain sentences', () => {
        expect(describeBotError(new BotsApiError('name_taken', 409))).toBe('A bot with this name already exists.')
        expect(describeBotError(new BotsApiError('invalid_name', 400))).toMatch(/1 to 64 characters/)
        expect(describeBotError(new BotsApiError('invalid_rate_limit', 400))).toBe(
            'Set the rate limit to a whole number from 1 to 100,000 requests per minute, or leave it empty.',
        )
        expect(describeBotError(new BotsApiError('http_401', 401))).toMatch(/session has ended/)
        expect(describeBotError(new BotsApiError('http_403', 403))).toMatch(/not allowed/)
        expect(describeBotError(new BotsApiError('http_502', 502))).toBe('The gateway answered with HTTP 502.')
        expect(describeBotError(new TypeError('Failed to fetch'))).toBe('Could not reach the gateway.')
        expect(describeBotError(new DOMException('timed out', 'TimeoutError'))).toMatch(/did not answer in time/)
    })
})

describe('shortKeyHint', () => {
    it('shortens the long mask the gateway returns and keeps short hints', async () => {
        const { shortKeyHint } = await import('./bots-panel')
        expect(shortKeyHint('sk-' + '*'.repeat(100) + 'a1b2')).toBe('sk-...a1b2')
        expect(shortKeyHint('sk-...cdef')).toBe('sk-...cdef')
    })
})
