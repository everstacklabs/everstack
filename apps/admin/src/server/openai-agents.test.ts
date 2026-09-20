import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  coalesceOpenAIHistory,
  eventText,
  openAIEventRole,
  openAIRequest,
  type OpenAIEvent,
} from './openai-agents'
import { setActiveOrgId } from '@/lib/active-org'

afterEach(() => {
  setActiveOrgId(null)
  vi.unstubAllGlobals()
})

it('scopes cookie-authenticated requests to the active organization', async () => {
  setActiveOrgId('org-active')
  const fetchMock = vi.fn().mockResolvedValue(
    new Response('{}', {
      status: 200,
      headers: { 'Content-Type': 'application/json' },
    }),
  )
  vi.stubGlobal('fetch', fetchMock)
  await openAIRequest('/capabilities')
  expect(new Headers(fetchMock.mock.calls[0]![1].headers).get('x-org-id')).toBe(
    'org-active',
  )
})

describe('OpenAI session history recovery', () => {
  it('merges live nested item events with recovered messages and preserves speaker roles', () => {
    const user = {
      id: 'user-1',
      role: 'user',
      content: [{ text: 'Say hello' }],
    }
    const assistant = {
      id: 'assistant-1',
      role: 'assistant',
      content: [{ text: 'Hello' }],
    }
    const event = (
      cursor: number,
      type: string,
      payload: Record<string, unknown>,
      id?: string,
    ): OpenAIEvent => ({
      cursor,
      event_id: String(cursor),
      type,
      payload,
      upstream_item_id: id,
    })
    // The real provider can deliver a saved snapshot before buffered SSE.
    const history = coalesceOpenAIHistory([
      event(1, 'agent.session.item.recovered', user, user.id),
      event(2, 'agent.session.item.recovered', assistant, assistant.id),
      event(3, 'agent.session.turn.item.added', { item: user }),
      event(
        4,
        'agent.session.turn.output_text.delta',
        { delta: 'Hello' },
        assistant.id,
      ),
      event(
        5,
        'agent.session.turn.output_text.done',
        { text: 'Hello' },
        assistant.id,
      ),
      event(6, 'agent.session.turn.item.done', { item: assistant }),
      // A distinct later message with identical text must remain visible.
      event(7, 'agent.session.turn.item.done', {
        item: { ...assistant, id: 'assistant-2' },
      }),
    ])
    expect(history.map(eventText)).toEqual(['Say hello', 'Hello', 'Hello'])
    expect(history.map(openAIEventRole)).toEqual([
      'user',
      'assistant',
      'assistant',
    ])
    expect(history.map((row) => row.cursor)).toEqual([1, 2, 7])
    expect(
      openAIEventRole(
        event(
          8,
          'agent.session.turn.output_text.delta',
          { delta: 'Hi' },
          'assistant-3',
        ),
      ),
    ).toBe('assistant')
  })

  it('joins streamed fragments and replaces them with the saved message without hiding tool failures', () => {
    const event = (
      cursor: number,
      payload: Record<string, unknown>,
    ): OpenAIEvent => ({
      cursor,
      event_id: String(cursor),
      type: 'agent.session.output_text.delta',
      upstream_item_id: 'message-1',
      payload,
    })
    const fragments = [
      event(1, { delta: 'Hello ' }),
      event(2, { delta: 'world' }),
    ]
    expect(coalesceOpenAIHistory(fragments).map(eventText)).toEqual([
      'Hello world',
    ])
    const failure: OpenAIEvent = {
      cursor: 3,
      event_id: 'failed',
      type: 'everstack.tool.result',
      payload: {},
      tool_failed: true,
    }
    const recovered = {
      ...event(4, { content: [{ type: 'output_text', text: 'Hello world!' }] }),
      type: 'agent.session.item.recovered',
    }
    const history = coalesceOpenAIHistory([
      ...fragments,
      failure,
      recovered,
      event(5, { delta: '!' }),
    ])
    expect(history.map(eventText)).toEqual(['Hello world!', ''])
    expect(history[1]?.tool_failed).toBe(true)
    expect(history[0]?.cursor).toBe(1)
  })
})
