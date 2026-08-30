import { describe, it, expect } from 'vitest'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import {
  getSpanInput,
  getSpanInputPayload,
  getSpanOutput,
  getSpanOutputPayload,
} from './traces-common'

const span = (spanAttributes: Record<string, string>): Span =>
  ({ spanAttributes }) as unknown as Span

describe('getSpanInput', () => {
  it('prefers a generation span\'s own messages over the trace-level payload', () => {
    expect(
      getSpanInput(
        span({ 'llm.request.messages': 'OWN', 'trace.input': 'TRACE' }),
      ),
    ).toBe('OWN')
  })

  it('falls back to trace.input for root spans that only carry the trace payload', () => {
    expect(getSpanInput(span({ 'trace.input': 'TRACE' }))).toBe('TRACE')
  })

  it('reads OTel-GenAI and OpenInference keys', () => {
    expect(getSpanInput(span({ 'gen_ai.input.messages': 'G' }))).toBe('G')
    expect(getSpanInput(span({ 'input.value': 'OI' }))).toBe('OI')
  })

  it('returns empty string when no input key is present', () => {
    expect(getSpanInput(span({ 'sandbox.command': 'ls' }))).toBe('')
  })

  it('exposes whether input came from the span or trace fallback', () => {
    expect(
      getSpanInputPayload(
        span({ 'llm.request.messages': 'OWN', 'trace.input': 'TRACE' }),
        { includeTraceFallback: true },
      ),
    ).toEqual({ value: 'OWN', key: 'llm.request.messages', scope: 'span' })

    expect(
      getSpanInputPayload(span({ 'trace.input': 'TRACE' }), {
        includeTraceFallback: true,
      }),
    ).toEqual({ value: 'TRACE', key: 'trace.input', scope: 'trace' })

    expect(
      getSpanInputPayload(span({ 'trace.input': 'TRACE' }), {
        includeTraceFallback: false,
      }),
    ).toBeUndefined()
  })
})

describe('getSpanOutput', () => {
  it('prefers the generation span\'s own choices over the trace-level payload', () => {
    expect(
      getSpanOutput(
        span({ 'llm.response.choices': 'OWN', 'trace.output': 'TRACE' }),
      ),
    ).toBe('OWN')
  })

  it('falls back to trace.output', () => {
    expect(getSpanOutput(span({ 'trace.output': 'TRACE' }))).toBe('TRACE')
  })

  it('reads embedding output and GenAI/OpenInference keys', () => {
    expect(getSpanOutput(span({ 'embedding.output': 'E' }))).toBe('E')
    expect(getSpanOutput(span({ 'gen_ai.output.messages': 'G' }))).toBe('G')
    expect(getSpanOutput(span({ 'output.value': 'OI' }))).toBe('OI')
  })

  it('returns empty string when no output key is present', () => {
    expect(getSpanOutput(span({ 'sandbox.command': 'ls' }))).toBe('')
  })

  it('exposes whether output came from the span or trace fallback', () => {
    expect(
      getSpanOutputPayload(
        span({ 'llm.response.choices': 'OWN', 'trace.output': 'TRACE' }),
        { includeTraceFallback: true },
      ),
    ).toEqual({ value: 'OWN', key: 'llm.response.choices', scope: 'span' })

    expect(
      getSpanOutputPayload(span({ 'trace.output': 'TRACE' }), {
        includeTraceFallback: true,
      }),
    ).toEqual({ value: 'TRACE', key: 'trace.output', scope: 'trace' })

    expect(
      getSpanOutputPayload(span({ 'trace.output': 'TRACE' }), {
        includeTraceFallback: false,
      }),
    ).toBeUndefined()
  })
})
