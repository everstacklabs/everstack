// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { OpenAISessionDetail } from './openai-session-detail'

vi.mock('@/hooks/auth/use-auth', () => ({
  useSession: () => ({ data: { user: { user: { id: 'user-1' } } } }),
}))

const json = (value: unknown, status = 200) =>
  new Response(JSON.stringify(value), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })

afterEach(() => vi.unstubAllGlobals())

describe('OpenAISessionDetail recovery presentation', () => {
  it('moves from loading to saved history and retains it on a refresh error', async () => {
    let resolveSession!: (response: Response) => void
    let sessionRequests = 0
    const first = new Promise<Response>((resolve) => {
      resolveSession = resolve
    })
    vi.stubGlobal(
      'fetch',
      vi.fn((input: RequestInfo | URL) => {
        const url = String(input)
        if (url.endsWith('/capabilities'))
          return Promise.resolve(
            json({
              enabled: true,
              environment_modes: ['self_hosted'],
              can_manage_connections: true,
              can_run_sessions: true,
              can_resolve_approvals: true,
              tool_types: [],
              file_operations: [],
            }),
          )
        if (url.includes('/events?'))
          return Promise.resolve(
            json({
              data: [
                {
                  cursor: 1,
                  event_id: 'recovered',
                  type: 'agent.session.item.recovered',
                  upstream_item_id: 'message-1',
                  payload: {
                    id: 'message-1',
                    role: 'assistant',
                    content: [{ text: 'Saved answer' }],
                  },
                },
                {
                  cursor: 2,
                  event_id: 'live',
                  type: 'agent.session.turn.item.done',
                  payload: {
                    item: {
                      id: 'message-1',
                      role: 'assistant',
                      content: [{ text: 'Saved answer' }],
                    },
                  },
                },
              ],
              next_cursor: 2,
              has_more: false,
            }),
          )
        if (url.endsWith('/usage'))
          return Promise.resolve(
            json({ status: 'unavailable', source: 'openai' }),
          )
        if (url.endsWith('/sessions/session-1')) {
          sessionRequests++
          if (sessionRequests === 1) return first
          return Promise.resolve(json({ error: 'refresh unavailable' }, 503))
        }
        return Promise.resolve(json({ data: [] }))
      }),
    )
    const cache = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    render(
      <QueryClientProvider client={cache}>
        <OpenAISessionDetail sessionId="session-1" />
      </QueryClientProvider>,
    )
    expect(screen.getByText('Loading session…')).toBeTruthy()
    resolveSession(
      json({
        id: 'session-1',
        connection_id: 'connection-1',
        model: 'gpt-5.2-codex',
        status: 'idle',
        readiness: 'ready',
        cleanup_status: 'active',
        environment_cleanup_status: 'active',
        environment: { type: 'self_hosted' },
      }),
    )
    expect(await screen.findByText('Saved answer')).toBeTruthy()
    expect(screen.getAllByText('Saved answer')).toHaveLength(1)
    await cache.invalidateQueries({ queryKey: ['openai-agents'] })
    await waitFor(() =>
      expect(screen.getByText('refresh unavailable')).toBeTruthy(),
    )
    expect(screen.getByText('Saved answer')).toBeTruthy()
  })
})
