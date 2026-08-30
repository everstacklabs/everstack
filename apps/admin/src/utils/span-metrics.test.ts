import { describe, it, expect } from 'vitest'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import {
  getSpanTokens,
  getSpanCostUSD,
  getSpanTtftMs,
  isGenerationSpan,
} from './span-metrics'

// Minimal Span stub: the helpers only touch spanName + spanAttributes.
function span(spanName: string, attrs: Record<string, unknown>): Span {
  return { spanName, spanAttributes: attrs } as unknown as Span
}

describe('getSpanTokens', () => {
  it('reads a Claude Code llm_request span (bare keys + cache, no total)', () => {
    const s = span('claude_code.llm_request', {
      'span.type': 'llm_request',
      input_tokens: '1896',
      output_tokens: '8923',
      cache_read_tokens: '996379',
      cache_creation_tokens: '0',
    })
    const t = getSpanTokens(s)
    expect(t.input).toBe(1896)
    expect(t.output).toBe(8923)
    expect(t.cacheRead).toBe(996379)
    expect(t.cacheWrite).toBe(0)
    // no explicit total key -> sum of components
    expect(t.total).toBe(1896 + 8923 + 996379)
  })

  it('reads a gateway provider span (native keys, explicit total wins)', () => {
    const s = span('provider.anthropic.chat', {
      'llm.tokens.input': '100',
      'llm.tokens.output': '50',
      'llm.tokens.total': '150',
    })
    const t = getSpanTokens(s)
    expect(t.input).toBe(100)
    expect(t.output).toBe(50)
    expect(t.total).toBe(150)
  })

  it('reads the OTel GenAI semconv', () => {
    const s = span('chat gpt-4o', {
      'gen_ai.usage.input_tokens': '10',
      'gen_ai.usage.output_tokens': '20',
    })
    expect(getSpanTokens(s).total).toBe(30)
  })
})

describe('getSpanCostUSD', () => {
  it('prefers native cost then ingest-stamped cost.estimated_usd', () => {
    expect(getSpanCostUSD(span('x', { 'llm.cost.total': '0.42' }))).toBeCloseTo(0.42)
    expect(getSpanCostUSD(span('x', { 'cost.estimated_usd': '0.013' }))).toBeCloseTo(0.013)
    expect(getSpanCostUSD(span('x', {}))).toBe(0)
  })
})

describe('getSpanTtftMs', () => {
  it('normalises ns sources and accepts coding-agent ms', () => {
    expect(getSpanTtftMs(span('x', { 'llm.stream.time_to_first_token': '2000000' }))).toBe(2)
    expect(getSpanTtftMs(span('x', { ttft_ms: '36562' }))).toBe(36562)
    expect(getSpanTtftMs(span('x', {}))).toBe(0)
  })
})

describe('isGenerationSpan', () => {
  it('recognises generations across SDKs and rejects non-LLM spans', () => {
    expect(isGenerationSpan(span('claude_code.llm_request', { 'span.type': 'llm_request' }))).toBe(true)
    expect(isGenerationSpan(span('provider.openai.chat', {}))).toBe(true)
    expect(isGenerationSpan(span('x', { 'observation.type': 'GENERATION' }))).toBe(true)
    expect(isGenerationSpan(span('gen_ai.chat', {}))).toBe(true)
    expect(isGenerationSpan(span('claude_code.tool', { 'span.type': 'tool' }))).toBe(false)
    expect(isGenerationSpan(span('claude_code.interaction', { 'span.type': 'interaction' }))).toBe(false)
  })
})
