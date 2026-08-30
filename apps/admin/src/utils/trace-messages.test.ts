import { describe, it, expect } from 'vitest'
import { parseConversation, hasStructuredConversation } from './trace-messages'

describe('parseConversation', () => {
  it('parses OpenAI chat messages with roles', () => {
    const raw = JSON.stringify([
      { role: 'system', content: 'You are helpful.' },
      { role: 'user', content: 'Hi' },
      { role: 'assistant', content: 'Hello!' },
    ])
    const msgs = parseConversation(raw)
    expect(msgs.map((m) => m.role)).toEqual(['system', 'user', 'assistant'])
    expect(msgs[2].content[0]).toEqual({ type: 'text', text: 'Hello!' })
  })

  it('parses assistant tool_calls (OpenAI) with JSON args', () => {
    const raw = JSON.stringify([
      {
        role: 'assistant',
        content: null,
        tool_calls: [
          { id: 'call_1', type: 'function', function: { name: 'get_weather', arguments: '{"city":"Paris"}' } },
        ],
      },
      { role: 'tool', tool_call_id: 'call_1', name: 'get_weather', content: '{"temp":18}' },
    ])
    const msgs = parseConversation(raw)
    const assistant = msgs.find((m) => m.role === 'assistant')!
    expect(assistant.toolCalls?.[0]).toMatchObject({ id: 'call_1', name: 'get_weather', args: { city: 'Paris' } })
    const tool = msgs.find((m) => m.role === 'tool')!
    expect(tool.toolCallId).toBe('call_1')
  })

  it('extracts reasoning_content (OpenAI-compatible) onto the assistant message', () => {
    const raw = JSON.stringify({
      choices: [
        {
          message: { role: 'assistant', content: 'The answer is 4.', reasoning_content: 'Let me add 2 and 2...' },
          finish_reason: 'stop',
        },
      ],
    })
    const msgs = parseConversation(raw)
    expect(msgs[0].reasoning).toBe('Let me add 2 and 2...')
    expect(msgs[0].content[0]).toEqual({ type: 'text', text: 'The answer is 4.' })
    expect(msgs[0].finishReason).toBe('stop')
  })

  it('extracts Anthropic thinking blocks into reasoning, not content', () => {
    const raw = JSON.stringify([
      {
        role: 'assistant',
        content: [
          { type: 'thinking', thinking: 'Considering the options...' },
          { type: 'text', text: 'Final answer.' },
        ],
      },
    ])
    const msgs = parseConversation(raw)
    expect(msgs[0].reasoning).toBe('Considering the options...')
    expect(msgs[0].content).toEqual([{ type: 'text', text: 'Final answer.' }])
  })

  it('parses multimodal image content parts', () => {
    const raw = JSON.stringify([
      {
        role: 'user',
        content: [
          { type: 'text', text: 'What is this?' },
          { type: 'image_url', image_url: { url: 'https://example.com/a.png' } },
        ],
      },
    ])
    const msgs = parseConversation(raw)
    expect(msgs[0].content).toEqual([
      { type: 'text', text: 'What is this?' },
      { type: 'image', url: 'https://example.com/a.png', detail: undefined },
    ])
  })

  it('returns [] for non-message data so callers can fall back', () => {
    expect(parseConversation('just a plain string')).toEqual([])
    expect(parseConversation('')).toEqual([])
    expect(hasStructuredConversation('{"unrelated":true}')).toBe(false)
  })

  it('accepts already-parsed objects (not just JSON strings)', () => {
    const msgs = parseConversation([{ role: 'user', content: 'hi' }])
    expect(msgs).toHaveLength(1)
    expect(msgs[0].role).toBe('user')
  })

  it('parses OTel gen_ai `parts` messages (text + tool_call + tool_call_response)', () => {
    const raw = JSON.stringify([
      { role: 'user', parts: [{ type: 'text', content: 'Weather in Paris?' }] },
      { role: 'assistant', parts: [{ type: 'tool_call', id: 'c1', name: 'get_weather', arguments: { city: 'Paris' } }] },
      { role: 'tool', parts: [{ type: 'tool_call_response', id: 'c1', response: '{"temp":18}' }] },
    ])
    const msgs = parseConversation(raw)
    expect(msgs[0].content[0]).toEqual({ type: 'text', text: 'Weather in Paris?' })
    const assistant = msgs.find((m) => m.role === 'assistant')!
    expect(assistant.toolCalls?.[0]).toMatchObject({ id: 'c1', name: 'get_weather', args: { city: 'Paris' } })
    const tool = msgs.find((m) => m.role === 'tool')!
    expect(tool.toolCallId).toBe('c1')
  })

  it('parses OpenAI input_audio into an audio part (data URI + format)', () => {
    const raw = JSON.stringify([
      { role: 'user', content: [{ type: 'input_audio', input_audio: { data: 'AAAA', format: 'wav' } }] },
    ])
    const msgs = parseConversation(raw)
    expect(msgs[0].content[0]).toEqual({ type: 'audio', url: 'data:audio/wav;base64,AAAA', format: 'wav' })
  })
})
