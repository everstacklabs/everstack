// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import {
  coalesceOpenAIHistory,
  eventText,
  type OpenAIEvent,
} from '@/server/openai-agents'
import {
  ChatComposer,
  ChatAssistantMessage,
  ChatTranscript,
} from './chat-presentation'
import {
  openAIInputDisabled,
  openAITurnActive,
} from './openai-session-controls'

describe('shared agent chat presentation', () => {
  it('sends with Enter, preserves Shift+Enter, and labels the arrow action', () => {
    const send = vi.fn()
    const { rerender } = render(
      <ChatComposer
        id="composer"
        value="hello"
        onChange={() => {}}
        onSubmit={send}
        placeholder="Message agent…"
      />,
    )
    const input = screen.getByLabelText('Message agent…')
    fireEvent.keyDown(input, { key: 'Enter', shiftKey: true })
    expect(send).not.toHaveBeenCalled()
    fireEvent.keyDown(input, { key: 'Enter' })
    expect(send).toHaveBeenCalledOnce()
    expect(
      screen
        .getByRole('button', { name: 'Send message' })
        .hasAttribute('disabled'),
    ).toBe(false)
    rerender(
      <ChatComposer
        id="composer"
        value="hello"
        onChange={() => {}}
        onSubmit={send}
        placeholder="Unavailable"
        disabled
      />,
    )
    expect(
      screen
        .getByRole('button', { name: 'Send message' })
        .hasAttribute('disabled'),
    ).toBe(true)
  })

  it('keeps terminal, capability-failure, pending, and ambiguous inputs disabled', () => {
    const available = {
      environmentTerminal: false,
      ambiguousIntent: false,
      capabilitiesReady: true,
      pending: false,
    }
    expect(openAIInputDisabled(available)).toBe(false)
    expect(
      openAIInputDisabled({ ...available, environmentTerminal: true }),
    ).toBe(true)
    expect(
      openAIInputDisabled({ ...available, capabilitiesReady: false }),
    ).toBe(true)
    expect(openAIInputDisabled({ ...available, pending: true })).toBe(true)
    expect(openAIInputDisabled({ ...available, ambiguousIntent: true })).toBe(
      true,
    )
    expect(openAITurnActive('running')).toBe(true)
    expect(openAITurnActive('idle')).toBe(false)
  })

  it('renders one assistant message when recovered history overlaps live events', () => {
    const item = {
      id: 'message-1',
      role: 'assistant',
      content: [{ text: 'Saved answer' }],
    }
    const events: OpenAIEvent[] = [
      {
        cursor: 1,
        event_id: '1',
        type: 'agent.session.item.recovered',
        upstream_item_id: 'message-1',
        payload: item,
      },
      {
        cursor: 2,
        event_id: '2',
        type: 'agent.session.turn.output_text.delta',
        upstream_item_id: 'message-1',
        payload: { delta: 'Saved answer' },
      },
      {
        cursor: 3,
        event_id: '3',
        type: 'agent.session.turn.item.done',
        payload: { item },
      },
    ]
    const rows = coalesceOpenAIHistory(events)
    render(
      <ChatTranscript>
        {rows.map((event) => (
          <ChatAssistantMessage key={event.cursor}>
            {eventText(event)}
          </ChatAssistantMessage>
        ))}
      </ChatTranscript>,
    )
    expect(screen.getAllByText('Saved answer')).toHaveLength(1)
  })
})
