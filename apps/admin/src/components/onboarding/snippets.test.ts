import { describe, expect, it } from 'vitest'
import {
  AGENT_PLACEHOLDER,
  buildSnippets,
  KEY_ENV,
  KEY_PLACEHOLDER,
  SNIPPET_TABS,
} from './snippets'
import { quickstartModel } from '@/lib/first-request'

const BASE = 'https://acme.eu-gra-1.everstack.ai'

describe('buildSnippets (gateway path)', () => {
  const snippets = buildSnippets({
    path: 'gateway',
    base: BASE,
    model: '@anthropic/claude-haiku-4-5',
  })

  it('reads the key from EVERSTACK_API_KEY and shows the export that sets it', () => {
    for (const tab of SNIPPET_TABS) {
      const code = snippets[tab.value]
      expect(code).not.toContain('x-evs-api-key')
      expect(code).toContain(`export ${KEY_ENV}="${KEY_PLACEHOLDER}"`)
    }
    expect(snippets.curl).toContain(`Authorization: Bearer $${KEY_ENV}`)
    expect(snippets.python).toContain(`os.environ["${KEY_ENV}"]`)
    expect(snippets.node).toContain(`process.env.${KEY_ENV}`)
    expect(snippets.go).toContain(`os.Getenv("${KEY_ENV}")`)
  })

  it('points the OpenAI SDKs at this instance /v1 with the routable model', () => {
    expect(snippets.python).toContain('from openai import OpenAI')
    expect(snippets.python).toContain(`base_url="${BASE}/v1"`)
    expect(snippets.node).toContain('import OpenAI from "openai"')
    expect(snippets.node).toContain(`baseURL: "${BASE}/v1"`)
    expect(snippets.go).toContain(`option.WithBaseURL("${BASE}/v1")`)
    for (const tab of SNIPPET_TABS) {
      expect(snippets[tab.value]).toContain('@anthropic/claude-haiku-4-5')
    }
  })

  it('keeps every line short enough for the panel', () => {
    for (const tab of SNIPPET_TABS) {
      for (const line of snippets[tab.value].split('\n')) {
        expect(line.length, `${tab.value}: ${line}`).toBeLessThanOrEqual(72)
      }
    }
  })

  it('inlines the real key only in the export line, and only when passed', () => {
    const withKey = buildSnippets({
      path: 'gateway',
      base: BASE,
      model: '@openai/gpt-5.4-mini',
      apiKey: 'sk-evs-secret',
    })
    for (const tab of SNIPPET_TABS) {
      const code = withKey[tab.value]
      expect(code).toContain(`export ${KEY_ENV}="sk-evs-secret"`)
      expect(code).not.toContain(KEY_PLACEHOLDER)
      // One mention: the export. The code itself never carries the key.
      expect(code.split('sk-evs-secret')).toHaveLength(2)
    }
    expect(withKey.curl).toContain(`Authorization: Bearer $${KEY_ENV}`)
  })

  it('names a real model in every state, never a placeholder', () => {
    // Free requests active: the bare trial model.
    const trial = buildSnippets({
      path: 'gateway',
      base: BASE,
      model: quickstartModel('gpt-5.4-mini'),
    })
    expect(trial.curl).toContain('"model": "gpt-5.4-mini"')
    // Nothing routable (free requests used up, off, or never there): the
    // model a key for the picked provider will be enabled with.
    const openai = buildSnippets({
      path: 'gateway',
      base: BASE,
      model: quickstartModel(null),
    })
    expect(openai.curl).toContain('"model": "@openai/gpt-5.4-mini"')
    const anthropic = buildSnippets({
      path: 'gateway',
      base: BASE,
      model: quickstartModel(null, 'anthropic'),
    })
    expect(anthropic.python).toContain('model="@anthropic/claude-haiku-4-5"')
    for (const snippets of [trial, openai, anthropic]) {
      for (const tab of SNIPPET_TABS) {
        expect(snippets[tab.value]).not.toMatch(/<[A-Z_]+>/)
      }
    }
  })
})

describe('buildSnippets (agent path)', () => {
  const agentSnippets = () =>
    buildSnippets({
      path: 'agent',
      base: BASE,
      model: '@openai/gpt-5.4-mini',
      agentId: 'agent-42',
    })

  it('runs a turn through the sessions API with the api-key header', () => {
    const snippets = agentSnippets()
    expect(snippets.curl).toContain(`${BASE}/v1/agents/sessions \\`)
    expect(snippets.curl).toContain(
      `${BASE}/v1/agents/sessions/$SESSION_ID/turns`,
    )
    expect(snippets.curl).toContain('"agent_id": "agent-42"')
    expect(snippets.curl).toContain('"user_input"')
    expect(snippets.curl).toContain(`x-evs-api-key: $${KEY_ENV}`)
    expect(snippets.curl).toContain(`export ${KEY_ENV}="${KEY_PLACEHOLDER}"`)
  })

  // The deploy invoke endpoint takes a deployment key. A platform key there
  // is always a 401, which is what the first request used to hit.
  it('never sends the platform key to the deploy invoke endpoint', () => {
    const snippets = agentSnippets()
    for (const tab of SNIPPET_TABS) {
      expect(snippets[tab.value]).not.toContain('/v1/deploy/')
    }
    // A platform key is a Bearer only on the OpenAI verbs.
    expect(snippets.curl).not.toContain('Authorization: Bearer')
  })

  it('calls the SDK methods that exist, with the fields the API reads', () => {
    const snippets = agentSnippets()
    expect(snippets.python).toContain('sessions.run_turn(')
    expect(snippets.python).toContain('created["session"]["id"]')
    expect(snippets.python).toContain('user_input=')
    expect(snippets.python).not.toContain('send_message')
    expect(snippets.node).toContain('client.agents.run(')
    expect(snippets.node).toContain('"agent-42"')
    expect(snippets.node).toContain('res.turn.assistant_output || res.turn.error')
    expect(snippets.go).toContain('"agent_id": "agent-42"')
    expect(snippets.go).toContain('"user_input"')
    expect(snippets.go).toContain('created["session"]')
  })

  it('uses a plain-word agent stand-in before the agent exists', () => {
    const snippets = buildSnippets({
      path: 'agent',
      base: BASE,
      model: quickstartModel(null),
    })
    expect(snippets.curl).toContain(AGENT_PLACEHOLDER)
    for (const tab of SNIPPET_TABS) {
      expect(snippets[tab.value]).not.toMatch(/<[A-Z_]+>/)
    }
  })
})
