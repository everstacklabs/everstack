import { describe, it, expect } from 'vitest'
import {
  categoryFromObservationType,
  categoryFromSpanName,
  importanceForCategory,
  type SpanCategory,
} from './traces-common'
import {
  categoryIcons,
  categoryColors,
  categoryLabels,
  categoryTimelineColors,
  categoryChipSolid,
} from './span-display-helpers'

describe('categoryFromObservationType', () => {
  it('maps semantic observation types to UI categories', () => {
    const cases: Record<string, SpanCategory> = {
      AGENT: 'agent',
      TOOL: 'tool',
      RETRIEVER: 'retriever',
      EMBEDDING: 'embedding',
      CACHE: 'cache',
      SANDBOX: 'sandbox',
      BROWSER: 'browser',
      COMPUTER: 'computer',
      GUARDRAIL: 'guardrail',
      SCORER: 'scorer',
      WORKFLOW: 'workflow',
      CHAIN: 'workflow',
      CONTROL: 'control',
      HTTP: 'http',
      INTEGRATION: 'integration',
      HARNESS: 'harness',
      MEDIA: 'media',
    }
    for (const [obsType, want] of Object.entries(cases)) {
      expect(categoryFromObservationType(obsType)).toBe(want)
      // case-insensitive
      expect(categoryFromObservationType(obsType.toLowerCase())).toBe(want)
    }
  })

  it('returns undefined for legacy/compat types so name-based mapping still runs', () => {
    for (const t of ['SPAN', 'EVENT', 'GENERATION', 'LLM', '', undefined]) {
      expect(categoryFromObservationType(t)).toBeUndefined()
    }
  })
})

describe('categoryFromSpanName', () => {
  it('routes Claude Code SDK spans to their semantic category', () => {
    expect(categoryFromSpanName('claude_code.interaction')).toBe('agent')
    expect(categoryFromSpanName('claude_code.session')).toBe('agent')
    expect(categoryFromSpanName('claude_code.llm_request')).toBe('provider')
    expect(categoryFromSpanName('claude_code.api_request')).toBe('provider')
    expect(categoryFromSpanName('claude_code.tool_decision')).toBe('tool')
    expect(categoryFromSpanName('claude_code.tool_result')).toBe('tool')
  })

  it('handles other coding-agent prefixes', () => {
    expect(categoryFromSpanName('gemini_cli.turn')).toBe('agent')
    expect(categoryFromSpanName('codex.tool.exec')).toBe('tool')
    expect(categoryFromSpanName('aider.completion')).toBe('provider')
  })

  it('maps `namespace:operation` app spans by their leading token', () => {
    expect(categoryFromSpanName('db:select-conversation-with-lead')).toBe('cache')
    expect(categoryFromSpanName('memory:add-message-to-history')).toBe('retriever')
    expect(categoryFromSpanName('llm:primary-turn')).toBe('provider')
    expect(categoryFromSpanName('process-message')).toBe('workflow')
    expect(categoryFromSpanName('e2e:state-machine-roundtrip')).toBe('workflow')
    expect(categoryFromSpanName('sms:send')).toBe('http')
    expect(categoryFromSpanName('effect:emit-event')).toBe('control')
  })

  it('recognises the OpenTelemetry GenAI semconv', () => {
    expect(categoryFromSpanName('gen_ai.chat gpt-4o')).toBe('provider')
    expect(categoryFromSpanName('embeddings text-embedding-3-small')).toBe('embedding')
  })

  it('returns undefined for genuinely unknown names so the caller falls back to internal', () => {
    expect(categoryFromSpanName('')).toBeUndefined()
    expect(categoryFromSpanName(undefined)).toBeUndefined()
    expect(categoryFromSpanName('xyzzy-frobnicate')).toBeUndefined()
  })
})

describe('importanceForCategory', () => {
  it('treats execution roots as primary, the rest as secondary', () => {
    expect(importanceForCategory('agent')).toBe('primary')
    expect(importanceForCategory('workflow')).toBe('primary')
    expect(importanceForCategory('tool')).toBe('secondary')
    expect(importanceForCategory('sandbox')).toBe('secondary')
  })
})

describe('category style maps are exhaustive', () => {
  // Every category produced by categoryFromObservationType must have an entry in
  // each style map, or the tree/timeline render falls back to undefined.
  const semanticCategories: SpanCategory[] = [
    'agent', 'tool', 'retriever', 'embedding', 'sandbox', 'browser', 'computer',
    'guardrail', 'scorer', 'workflow', 'control', 'http', 'integration', 'harness', 'media',
  ]
  it('has icon, color, label, timeline color, and solid chip for every semantic category', () => {
    for (const c of semanticCategories) {
      expect(categoryIcons[c], `icon for ${c}`).toBeDefined()
      expect(categoryColors[c], `color for ${c}`).toBeDefined()
      expect(categoryLabels[c], `label for ${c}`).toBeTruthy()
      expect(categoryTimelineColors[c], `timeline color for ${c}`).toBeDefined()
      expect(categoryChipSolid[c], `solid chip for ${c}`).toBeDefined()
    }
  })
})
