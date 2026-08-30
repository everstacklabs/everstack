/**
 * guardrail-events: surface guardrail / policy / moderation checks recorded as
 * span events (D7).
 *
 * Guardrails are the part of an agent run most people care about in production
 * (did we block the unsafe input, did the PII filter fire) yet incumbents bury
 * them in a generic event log. This recognizes guardrail-shaped span events by
 * name convention and normalizes them into pass/block checks with the rule that
 * fired and how long it took, so the trace detail can show a safety summary
 * instead of a wall of untyped events.
 *
 * Convention (emitter is a backend follow-up; this renders whatever matches):
 *   event.name:        contains guardrail | policy | moderation | pii | safety
 *   attributes.result: pass | block | flag  (also action/outcome/decision)
 *   attributes.rule:   the rule/category that fired (also name/category/policy)
 *   attributes.latency_ms / duration_ms: check cost in milliseconds
 */

export type GuardrailOutcome = 'pass' | 'block' | 'flag' | 'unknown'

export interface RawSpanEvent {
  name: string
  attributes?: Record<string, string>
}

export interface GuardrailCheck {
  name: string
  outcome: GuardrailOutcome
  rule?: string
  detail?: string
  latencyMs?: number
}

const NAME_RE = /guardrail|policy|moderation|\bpii\b|safety/i

const OUTCOME_KEYS = ['result', 'outcome', 'action', 'decision', 'status']
const RULE_KEYS = ['rule', 'guardrail.rule', 'guardrail.name', 'category', 'policy', 'name']
const DETAIL_KEYS = ['detail', 'message', 'reason', 'violations', 'description']
const LATENCY_KEYS = ['latency_ms', 'duration_ms', 'guardrail.latency_ms', 'elapsed_ms']

function firstAttr(attrs: Record<string, string>, keys: string[]): string | undefined {
  for (const k of keys) {
    const v = attrs[k]
    if (v != null && v !== '') return v
  }
  return undefined
}

/** True when an event looks like a guardrail/policy/moderation check. */
export function isGuardrailEvent(event: RawSpanEvent): boolean {
  if (NAME_RE.test(event.name)) return true
  const attrs = event.attributes ?? {}
  return Object.keys(attrs).some((k) => NAME_RE.test(k))
}

function normalizeOutcome(raw: string | undefined, eventName: string): GuardrailOutcome {
  const v = (raw ?? '').toLowerCase()
  if (/block|deny|denied|reject|fail|violat/.test(v)) return 'block'
  if (/flag|warn|review/.test(v)) return 'flag'
  if (/pass|allow|ok|clean|success/.test(v)) return 'pass'
  // Fall back to the event name when no explicit outcome attribute is present.
  if (/block|denied|reject|violat/i.test(eventName)) return 'block'
  if (/pass|allow|clean/i.test(eventName)) return 'pass'
  return 'unknown'
}

/** Parse a single span event into a guardrail check. */
export function parseGuardrailEvent(event: RawSpanEvent): GuardrailCheck {
  const attrs = event.attributes ?? {}
  const latencyRaw = firstAttr(attrs, LATENCY_KEYS)
  const latencyMs = latencyRaw != null ? Number(latencyRaw) : undefined
  return {
    name: event.name,
    outcome: normalizeOutcome(firstAttr(attrs, OUTCOME_KEYS), event.name),
    rule: firstAttr(attrs, RULE_KEYS),
    detail: firstAttr(attrs, DETAIL_KEYS),
    latencyMs: latencyMs != null && Number.isFinite(latencyMs) ? latencyMs : undefined,
  }
}

export interface GuardrailSummary {
  checks: GuardrailCheck[]
  passed: number
  blocked: number
  flagged: number
  /** True if any check blocked: the run hit a guardrail. */
  hasBlock: boolean
}

/** Extract and summarize guardrail checks from a span's events. */
export function summarizeGuardrails(events: RawSpanEvent[]): GuardrailSummary {
  const checks = events.filter(isGuardrailEvent).map(parseGuardrailEvent)
  let passed = 0
  let blocked = 0
  let flagged = 0
  for (const c of checks) {
    if (c.outcome === 'pass') passed++
    else if (c.outcome === 'block') blocked++
    else if (c.outcome === 'flag') flagged++
  }
  return { checks, passed, blocked, flagged, hasBlock: blocked > 0 }
}
