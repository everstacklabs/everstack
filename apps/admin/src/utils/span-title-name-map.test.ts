import { describe, it, expect } from 'vitest'
import { getSpanDisplayName, humanizeSpanName } from './span-title-name-map'

describe('getSpanDisplayName — external SDK + humanizer', () => {
  it('humanizes Claude Code telemetry span names', () => {
    expect(getSpanDisplayName('claude_code.llm_request')).toBe('Claude Code: LLM Request')
    expect(getSpanDisplayName('claude_code.interaction')).toBe('Claude Code: Interaction')
    expect(getSpanDisplayName('claude_code')).toBe('Claude Code')
  })

  it('uppercases known acronyms and splits underscores/dashes', () => {
    expect(humanizeSpanName('some_api_call')).toBe('Some API Call')
    expect(humanizeSpanName('tenantcheck-123')).toBe('Tenantcheck 123')
    expect(humanizeSpanName('mcp.tool_use')).toBe('MCP Tool Use')
  })

  it('renders unrecognized (user SDK) span names verbatim, not re-cased', () => {
    // v1: the name a user chose in startSpan() is authoritative — no humanizing.
    expect(getSpanDisplayName('Fetch user profile')).toBe('Fetch user profile')
    expect(getSpanDisplayName('handleWebhook')).toBe('handleWebhook')
    expect(getSpanDisplayName('process_order')).toBe('process_order')
    expect(getSpanDisplayName('my-custom-step')).toBe('my-custom-step')
  })
})

describe('getSpanDisplayName — semantic taxonomy names', () => {
  it('renders readable titles for the M1 emitter span names', () => {
    const cases: Record<string, string> = {
      'sandbox.fs.write': 'Sandbox FS: Write',
      'sandbox.exec.git': 'Sandbox Exec: git',
      'browser.navigate': 'Browser: Navigate',
      'memory.retrieve': 'Memory Retrieve',
      'workflow.run': 'Workflow Run',
      'workflow.node.provider': 'Workflow Node: Provider',
      'mcp.tool.call': 'MCP Tool Call',
      'a2a.call': 'A2A Call',
      'scorer.pipeline': 'Scorer Pipeline',
      'scorer.task_completion': 'Scorer: Task Completion',
      'vector.query': 'Vector Query',
      'embedding.embed': 'Embedding',
      'harness.run': 'Harness Run',
      'integration.github': 'Integration: Github',
    }
    for (const [name, want] of Object.entries(cases)) {
      expect(getSpanDisplayName(name), name).toBe(want)
    }
  })

  it('still maps the legacy gateway/provider names', () => {
    expect(getSpanDisplayName('gateway.chat.completion')).toBe('Chat Completion')
    expect(getSpanDisplayName('provider.openai.chat')).toBe('OpenAI API Call')
  })
})
