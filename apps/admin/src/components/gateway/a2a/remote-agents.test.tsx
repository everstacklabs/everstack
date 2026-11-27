// @vitest-environment jsdom
// Connections and runs panel: key reuse across remounts, readable errors, refusal handling.

import { cleanup, fireEvent, render, screen, waitFor, act } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { describeInteropError, isRetryableInteropError } from '@/hooks/gateway/use-interop'
import { RemoteAgents } from './remote-agents'

vi.mock('@/hooks/auth', () => ({
    useSession: () => ({ data: { user: { organizations: [{ id: 'org1' }] } } }),
}))
vi.mock('@/lib/api-url', () => ({ getApiBaseUrl: () => '' }))

const remotes = [
    {
        id: 'r1',
        name: 'alpha',
        endpoint: 'https://a.example/a2a',
        protocol: 'a2a-legacy',
        has_credential: true,
        created_at: '',
        updated_at: '',
    },
    {
        id: 'r2',
        name: 'beta',
        endpoint: 'https://b.example/a2a',
        protocol: 'a2a-1.0',
        routing_tenant: 't-1',
        has_credential: false,
        created_at: '',
        updated_at: '',
    },
]

type Handler = (body: any) => Promise<Response> | Response
let postRuns: Handler
let postRemotes: Handler
const runBodies: any[] = []
const runsById = new Map<string, any>()

const json = (status: number, body: unknown) =>
    new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })

function renderPage() {
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    return render(
        <QueryClientProvider client={client}>
            <RemoteAgents />
        </QueryClientProvider>,
    )
}

const submitButton = () =>
    screen.getByRole('button', { name: /^(Start run|Retry submission|View existing run|Send again|Sending…)$/ })

beforeEach(() => {
    runBodies.length = 0
    runsById.clear()
    // Plain-HTTP origin: crypto.randomUUID is not exposed.
    vi.stubGlobal('crypto', { getRandomValues: (a: Uint8Array) => a })
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
        const body = init?.body ? JSON.parse(String(init.body)) : undefined
        if (url === '/api/interop/remotes' && method === 'GET') return json(200, { remotes })
        if (url === '/api/interop/remotes' && method === 'POST') return postRemotes(body)
        if (url === '/api/interop/runs' && method === 'GET') return json(200, { runs: [...runsById.values()] })
        if (url === '/api/interop/runs' && method === 'POST') {
            runBodies.push(body)
            return postRuns(body)
        }
        const m = /^\/api\/interop\/runs\/([^/]+)$/.exec(url)
        if (m) return json(200, runsById.get(m[1]))
        return json(404, { error: 'not_found' })
    })
})
afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
})

describe('error helper', () => {
    it('maps codes to sentences', () => {
        expect(describeInteropError('rpc_-32601')).toMatch(/protocol setting/)
        expect(describeInteropError('invalid_envelope')).toMatch(/protocol setting/)
        expect(describeInteropError('invalid_rpc_error')).toMatch(/protocol setting/)
        expect(describeInteropError(new Error('credential_encryption_unavailable'))).toMatch(
            /no credential encryption key configured/,
        )
        expect(describeInteropError('http_502')).toBe('The remote agent had a server error (HTTP 502).')
        expect(describeInteropError('http_418')).toBe('The remote agent answered with HTTP 418.')
        expect(describeInteropError(new Error('request failed: 504'))).toBe('The gateway answered with HTTP 504.')
        expect(describeInteropError('rpc_-1')).toBe('The remote agent returned JSON-RPC error -1.')
        expect(describeInteropError(new TypeError('Failed to fetch'))).toMatch(/Could not reach the gateway/)
        expect(describeInteropError(new Error('name and endpoint are required'))).toBe(
            'Name and endpoint are required.',
        )
        expect(describeInteropError('brand_new_code')).toBe('The operation failed (brand_new_code).')
    })
    it('only calls ambiguous failures retryable', () => {
        for (const e of [
            new TypeError('Failed to fetch'),
            'transport_error',
            'read_failed',
            'operation_failed',
            'operation_in_progress',
            'http_503',
            new Error('request failed: 502'),
        ])
            expect(isRetryableInteropError(e)).toBe(true)
        for (const e of [
            'not_found',
            'invalid_request',
            'idempotency_conflict',
            'credential_encryption_unavailable',
            'http_401',
            new Error('request failed: 400'),
            'rpc_-32601',
        ])
            expect(isRetryableInteropError(e)).toBe(false)
    })
})

describe('external runs flow', () => {
    it('retries with the same key after an ambiguous failure, even across a remount', async () => {
        postRuns = () => Promise.reject(new TypeError('Failed to fetch'))
        const first = renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Use alpha' }))
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'do the thing' } })
        fireEvent.click(submitButton())
        expect(await screen.findByText(/Could not reach the gateway/)).toBeDefined()
        expect(screen.getByText(/will not send it twice/)).toBeDefined()
        expect(submitButton().textContent).toBe('Retry submission')
        const key = runBodies[0].idempotency_key
        expect(typeof key).toBe('string')
        expect(key.length).toBeGreaterThanOrEqual(8)

        // Fix 2: picking another connection shows a fresh form state.
        fireEvent.click(screen.getByRole('button', { name: 'Use beta' }))
        expect(submitButton().textContent).toBe('Start run')
        expect(screen.queryByText(/will not send it twice/)).toBeNull()
        fireEvent.click(screen.getByRole('button', { name: 'Use alpha' }))
        expect(submitButton().textContent).toBe('Retry submission')

        // Fix 9: tab switch (unmount + remount) keeps draft, connection and key.
        first.unmount()
        postRuns = () => {
            const run = {
                id: '11111111-1111-1111-1111-111111111111',
                remote_id: 'r1',
                remote_name: 'alpha',
                protocol: 'a2a-legacy',
                state: 'working',
                remote_task_id: 'task-1',
                output: '',
                created_at: new Date().toISOString(),
                updated_at: '',
            }
            runsById.set(run.id, run)
            return json(200, run)
        }
        renderPage()
        await screen.findByRole('button', { name: 'Use alpha' })
        expect((screen.getByLabelText('Task') as HTMLTextAreaElement).value).toBe('do the thing')
        await waitFor(() => expect(submitButton().textContent).toBe('Retry submission'))
        fireEvent.click(submitButton())
        await waitFor(() => expect(submitButton().textContent).toBe('View existing run'))
        expect(runBodies[1].idempotency_key).toBe(key)
        expect(runBodies[1].remote).toBe('alpha')

        // Editing the task mints a new key.
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'do another thing' } })
        expect(submitButton().textContent).toBe('Start run')
        fireEvent.click(submitButton())
        await waitFor(() => expect(runBodies.length).toBe(3))
        expect(runBodies[2].idempotency_key).not.toBe(key)
        await waitFor(() => expect(screen.getByRole('button', { name: 'New task' })).toBeDefined())
        fireEvent.click(screen.getByRole('button', { name: 'New task' }))
    })

    it('does not offer the retry note for definite errors', async () => {
        postRuns = () => json(404, { error: 'not_found' })
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Use beta' }))
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'x' } })
        fireEvent.click(submitButton())
        expect(await screen.findByText('Not found. It may have been removed.')).toBeDefined()
        expect(screen.queryByText(/will not send it twice/)).toBeNull()
    })

    it('enforces the UTF-8 byte limit', async () => {
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Use beta' }))
        // 32,769 characters, 65,538 bytes.
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'é'.repeat(32769) } })
        expect(screen.getByText('This task is 65,538 bytes, over the 65,536 byte limit.')).toBeDefined()
        expect((submitButton() as HTMLButtonElement).disabled).toBe(true)
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'é'.repeat(32768) } })
        expect(screen.queryByText(/over the 65,536 byte limit/)).toBeNull()
        expect((submitButton() as HTMLButtonElement).disabled).toBe(false)
    })

    it('shows a refused run and lets the next send use a new key', async () => {
        let n = 0
        postRuns = () => {
            n += 1
            const run = {
                id: `2222222${n}-2222-2222-2222-222222222222`,
                remote_id: 'r1',
                remote_name: 'alpha',
                protocol: 'a2a-legacy',
                state: 'rejected',
                last_error: 'rpc_-32601',
                output: '',
                created_at: new Date().toISOString(),
                updated_at: '',
            }
            runsById.set(run.id, run)
            return json(200, run)
        }
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Use alpha' }))
        fireEvent.change(screen.getByLabelText('Task'), { target: { value: 'refuse me' } })
        fireEvent.click(submitButton())
        expect(await screen.findByText(/refused this task, so no work was done/)).toBeDefined()
        expect(screen.getByText(/refused this task/).textContent).toMatch(/Check the protocol setting/)
        expect(submitButton().textContent).toBe('Send again')
        fireEvent.click(submitButton())
        await waitFor(() => expect(runBodies.length).toBe(2))
        expect(runBodies[1].idempotency_key).not.toBe(runBodies[0].idempotency_key)
        fireEvent.click(await screen.findByRole('button', { name: 'New task' }))
    })
})

describe('remote agent dialog', () => {
    it('defaults new remotes to legacy and hides the routing tenant', async () => {
        renderPage()
        fireEvent.click(await screen.findByRole('button', { name: 'Add remote agent' }))
        const trigger = await screen.findByRole('combobox', { name: 'Protocol' })
        expect(trigger.textContent).toContain('Legacy A2A 0.2 / 0.3')
        expect(screen.queryByLabelText('Remote routing tenant (optional)')).toBeNull()
    })

    it('shows the routing tenant for A2A 1.0 and the save error inline', async () => {
        postRemotes = () => json(503, { error: 'credential_encryption_unavailable' })
        renderPage()
        await screen.findByRole('button', { name: 'Use beta' })
        fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[1])
        const routing = (await screen.findByLabelText('Remote routing tenant (optional)')) as HTMLInputElement
        expect(routing.value).toBe('t-1')
        fireEvent.change(screen.getByLabelText('Bearer token (optional)'), { target: { value: 'secret-token' } })
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Save connection' }))
        })
        expect(await screen.findByText(/no credential encryption key configured/)).toBeDefined()
    })

    it('warns when a credentialed connection changes without replacing the token', async () => {
        renderPage()
        await screen.findByRole('button', { name: 'Use alpha' })
        fireEvent.click(screen.getAllByRole('button', { name: 'Edit' })[0])
        const help = await screen.findByText(/requires replacing or removing it/)
        expect(help.className).toContain('text-brand-main-200')
        fireEvent.change(screen.getByLabelText('Endpoint URL'), { target: { value: 'https://new.example/a2a' } })
        expect(screen.getByText(/requires replacing or removing it/).className).toContain('text-warning')
        fireEvent.click(screen.getByLabelText('Remove the saved credential'))
        expect(screen.getByText(/requires replacing or removing it/).className).toContain('text-brand-main-200')
    })
})
