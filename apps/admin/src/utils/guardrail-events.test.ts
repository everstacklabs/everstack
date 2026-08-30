import { describe, it, expect } from 'vitest'
import {
  isGuardrailEvent,
  parseGuardrailEvent,
  summarizeGuardrails,
  type RawSpanEvent,
} from './guardrail-events'

describe('isGuardrailEvent', () => {
  it('matches by event name convention', () => {
    expect(isGuardrailEvent({ name: 'guardrail.input' })).toBe(true)
    expect(isGuardrailEvent({ name: 'moderation.check' })).toBe(true)
    expect(isGuardrailEvent({ name: 'pii.scan' })).toBe(true)
    expect(isGuardrailEvent({ name: 'gen_ai.choice' })).toBe(false)
  })

  it('matches by attribute key when the name is generic', () => {
    expect(isGuardrailEvent({ name: 'check', attributes: { 'guardrail.rule': 'toxicity' } })).toBe(true)
    expect(isGuardrailEvent({ name: 'check', attributes: { foo: 'bar' } })).toBe(false)
  })
})

describe('parseGuardrailEvent', () => {
  it('normalizes a blocking outcome and pulls rule/latency', () => {
    const c = parseGuardrailEvent({
      name: 'guardrail.input',
      attributes: { result: 'block', rule: 'toxicity', latency_ms: '12', reason: 'hate speech' },
    })
    expect(c.outcome).toBe('block')
    expect(c.rule).toBe('toxicity')
    expect(c.latencyMs).toBe(12)
    expect(c.detail).toBe('hate speech')
  })

  it('maps varied outcome vocab to canonical outcomes', () => {
    expect(parseGuardrailEvent({ name: 'g', attributes: { action: 'allow' } }).outcome).toBe('pass')
    expect(parseGuardrailEvent({ name: 'g', attributes: { decision: 'deny' } }).outcome).toBe('block')
    expect(parseGuardrailEvent({ name: 'g', attributes: { outcome: 'flagged' } }).outcome).toBe('flag')
  })

  it('falls back to the event name when no outcome attribute is present', () => {
    expect(parseGuardrailEvent({ name: 'guardrail.blocked' }).outcome).toBe('block')
    expect(parseGuardrailEvent({ name: 'guardrail.passed' }).outcome).toBe('pass')
    expect(parseGuardrailEvent({ name: 'guardrail.ran' }).outcome).toBe('unknown')
  })

  it('drops a non-numeric latency', () => {
    expect(parseGuardrailEvent({ name: 'g', attributes: { latency_ms: 'n/a' } }).latencyMs).toBeUndefined()
  })
})

describe('summarizeGuardrails', () => {
  it('filters guardrail events and tallies outcomes', () => {
    const events: RawSpanEvent[] = [
      { name: 'gen_ai.choice' },
      { name: 'guardrail.input', attributes: { result: 'pass' } },
      { name: 'moderation.output', attributes: { result: 'block', rule: 'violence' } },
      { name: 'policy.pii', attributes: { result: 'flag' } },
    ]
    const s = summarizeGuardrails(events)
    expect(s.checks).toHaveLength(3)
    expect(s.passed).toBe(1)
    expect(s.blocked).toBe(1)
    expect(s.flagged).toBe(1)
    expect(s.hasBlock).toBe(true)
  })

  it('returns an empty summary when there are no guardrail events', () => {
    const s = summarizeGuardrails([{ name: 'gen_ai.choice' }, { name: 'llm.start' }])
    expect(s.checks).toHaveLength(0)
    expect(s.hasBlock).toBe(false)
  })
})
