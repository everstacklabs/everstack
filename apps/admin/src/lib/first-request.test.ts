import { describe, expect, it, vi } from 'vitest'
import {
  describeAgentTurnError,
  describeTestRequestError,
  isUsableProvider,
  keyModelFor,
  parseRetryAfter,
  pickTestModel,
  quickstartModel,
  sendAgentTestRequest,
  sendTestRequest,
} from './first-request'

// The server lists every catalog provider, configured or not; unconfigured
// rows carry a catalog name but no active configuration.
const UNCONFIGURED = ['openai', 'anthropic', 'google', 'groq'].map((name) => ({
  catalog: { name },
  isActive: false,
}))

function configured(name: string, enabledModels: string[]) {
  return { catalog: { name }, isActive: true, configuration: { enabledModels } }
}

describe('isUsableProvider', () => {
  it('needs an active config with at least one enabled model', () => {
    expect(isUsableProvider({ catalog: { name: 'openai' } })).toBe(false)
    expect(
      isUsableProvider({
        catalog: { name: 'openai' },
        isActive: true,
        configuration: { enabledModels: [] },
      }),
    ).toBe(false)
    expect(isUsableProvider(configured('openai', ['gpt-4o']))).toBe(true)
  })
})

describe('pickTestModel', () => {
  it('returns null when only unconfigured catalog rows come back', () => {
    expect(pickTestModel([])).toBeNull()
    expect(pickTestModel(UNCONFIGURED)).toBeNull()
  })

  it('gives a Groq-only org a Groq model, not the unconfigured OpenAI row', () => {
    expect(
      pickTestModel([
        ...UNCONFIGURED.filter((p) => p.catalog.name !== 'groq'),
        configured('groq', ['llama-3.3-70b-versatile', 'llama-3.1-8b-instant']),
      ]),
    ).toEqual({ provider: 'groq', model: '@groq/llama-3.1-8b-instant' })
  })

  it('prefers providers in catalog order regardless of connection order', () => {
    expect(
      pickTestModel([
        configured('google', ['gemini-2.5-flash-lite']),
        configured('anthropic', ['claude-haiku-4-5', 'claude-sonnet-4-5']),
      ])?.model,
    ).toBe('@anthropic/claude-haiku-4-5')
  })

  it('uses an enabled model when the cheap pick is not enabled', () => {
    expect(pickTestModel([configured('openai', ['gpt-4o'])])?.model).toBe(
      '@openai/gpt-4o',
    )
  })

  it('falls back to an enabled model for providers without a cheap default', () => {
    expect(
      pickTestModel([
        configured('together', ['config', 'meta-llama/Llama-3-8b']),
      ])?.model,
    ).toBe('@together/meta-llama/Llama-3-8b')
  })
})

describe('quickstartModel', () => {
  it('uses the routable model when there is one', () => {
    expect(quickstartModel('@groq/llama-3.1-8b-instant', 'anthropic')).toBe(
      '@groq/llama-3.1-8b-instant',
    )
    expect(quickstartModel('gpt-5.4-mini')).toBe('gpt-5.4-mini')
  })

  it('otherwise names the model the add-key card enables for the picked provider', () => {
    expect(quickstartModel(null)).toBe('@openai/gpt-5.4-mini')
    expect(quickstartModel(null, 'anthropic')).toBe('@anthropic/claude-haiku-4-5')
    expect(quickstartModel(null, 'anthropic')).toBe(keyModelFor('anthropic'))
  })
})

describe('parseRetryAfter', () => {
  it('reads seconds and HTTP dates, and ignores anything else', () => {
    const now = Date.UTC(2026, 9, 27, 12, 0, 0)
    expect(parseRetryAfter('3', now)).toBe(3)
    expect(parseRetryAfter('1.2', now)).toBe(2)
    expect(parseRetryAfter('0', now)).toBeUndefined()
    expect(parseRetryAfter('Tue, 27 Oct 2026 12:00:05 GMT', now)).toBe(5)
    expect(parseRetryAfter('Tue, 27 Oct 2026 11:59:00 GMT', now)).toBeUndefined()
    expect(parseRetryAfter('soon', now)).toBeUndefined()
    expect(parseRetryAfter(null, now)).toBeUndefined()
  })
})

describe('describeTestRequestError', () => {
  it('maps a missing provider to an actionable title', () => {
    const err = describeTestRequestError(
      412,
      'no providers configured for this tenant',
    )
    expect(err.kind).toBe('no-provider')
    expect(err.title).toBe('No model provider configured')
  })

  // Real shape: the gateway maps an upstream 401/403 to FailedPrecondition,
  // which the /v1 OpenAI layer renders as HTTP 500 (or 400 straight from
  // grpc-gateway) with this message (upstream_errors.go, errors.go).
  const PROVIDER_REJECTED = JSON.stringify({
    error: {
      message:
        'the upstream provider (openai) rejected the configured API key: openai returned 401: Incorrect API key provided: sk-test*****abcd. (type=invalid_request_error, code=invalid_api_key)',
      type: 'api_error',
      param: null,
      code: null,
    },
  })

  it.each([500, 400])(
    'recognises a provider key rejection by its message (HTTP %i)',
    (status) => {
      const err = describeTestRequestError(status, PROVIDER_REJECTED)
      expect(err.kind).toBe('provider-auth')
      expect(err.title).toBe('Your provider rejected its API key')
      expect(err.detail).toContain('Openai refused the key')
      expect(err.raw).toContain('rejected the configured API key')
    },
  )

  it('names the requested model when the gateway cannot route it', () => {
    const err = describeTestRequestError(
      404,
      JSON.stringify({
        error: {
          code: null,
          message:
            'model not found: gpt-4o (no provider found for model (model may not be activated or configured))',
        },
      }),
      { requestedModel: '@openai/gpt-4o' },
    )
    expect(err.kind).toBe('model-unavailable')
    expect(err.title).toBe('gpt-4o is not available here')
    expect(err.detail).toContain('No connected provider serves gpt-4o')
    expect(err.raw).toContain('model not found')
  })

  it('maps 402 to a plan or spend limit and 429 to a rate limit with Retry-After', () => {
    const plan = describeTestRequestError(
      402,
      JSON.stringify({
        error: { message: 'spend limit', type: 'insufficient_quota', code: 'spend_limit_exceeded' },
      }),
    )
    expect(plan.kind).toBe('plan-limit')
    expect(plan.code).toBe('spend_limit_exceeded')
    expect(plan.title).toBe('Your spend limit is reached')

    const rate = describeTestRequestError(429, '{"error":{"message":"slow down"}}', {
      retryAfter: '4',
    })
    expect(rate.kind).toBe('rate-limited')
    expect(rate.retryAfterSeconds).toBe(4)
    expect(rate.detail).toContain('Try again in 4 seconds')
  })

  it('gives each 402 reason its own next step and never shows the reason', () => {
    const quota = (code: string) =>
      describeTestRequestError(
        402,
        JSON.stringify({
          error: { message: 'x', type: 'insufficient_quota', code },
        }),
      )
    const spend = quota('spend_limit_exceeded')
    expect(spend.kind).toBe('plan-limit')
    expect(spend.title).toBe('Your spend limit is reached')
    expect(spend.detail).toContain('00:00 UTC')
    expect(quota('insufficient_credit').title).toBe('Out of credit')
    expect(quota('wallet_frozen').title).toBe('Billing needs attention')
    // The older envelope carries the reason in error.type.
    expect(
      describeTestRequestError(
        402,
        '{"error":{"type":"spend_limit_exceeded","message":"Spend limit exceeded","code":402}}',
      ).title,
    ).toBe('Your spend limit is reached')
    for (const code of [
      'spend_limit_exceeded',
      'insufficient_credit',
      'wallet_frozen',
      'model_not_in_free_allowlist',
      'something_new',
    ]) {
      const { title, detail } = quota(code)
      expect(`${title} ${detail}`).not.toContain(code)
      expect(`${title} ${detail}`).not.toContain('_')
    }
  })

  it('treats a 401 without a provider message as our key being rejected', () => {
    const ours = describeTestRequestError(
      401,
      JSON.stringify({
        error: { message: 'invalid api key', type: 'authentication_error' },
      }),
    )
    expect(ours.kind).toBe('key-rejected')
  })

  it('does not blame the key for a 403 permission refusal', () => {
    const err = describeTestRequestError(
      403,
      JSON.stringify({ error: { message: 'feature not available on plan' } }),
    )
    expect(err.kind).toBe('failed')
  })
})

describe('sendTestRequest', () => {
  it('returns the reply, answering model and latency', async () => {
    const fetchImpl = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            model: 'gpt-4.1-mini-2025-04-14',
            choices: [{ message: { content: 'Hello from Everstack' } }],
          }),
          { status: 200 },
        ),
    )
    const times = [100, 342]
    const result = await sendTestRequest({
      url: 'https://acme.example/v1/chat/completions',
      apiKey: 'sk-test',
      body: { model: '@openai/gpt-4.1-mini', messages: [] },
      requestedModel: '@openai/gpt-4.1-mini',
      fetchImpl: fetchImpl as unknown as typeof fetch,
      now: () => times.shift() ?? 0,
    })
    expect(result).toEqual({
      ok: true,
      reply: 'Hello from Everstack',
      model: 'gpt-4.1-mini-2025-04-14',
      latencyMs: 242,
    })
    const init = (fetchImpl.mock.calls[0] as unknown[])[1] as RequestInit
    expect((init.headers as Record<string, string>).Authorization).toBe(
      'Bearer sk-test',
    )
  })

  it('reports an unreachable gateway in plain words', async () => {
    const result = await sendTestRequest({
      url: 'https://acme.example/v1/chat/completions',
      apiKey: 'sk-test',
      body: {},
      fetchImpl: (async () => {
        throw new TypeError('Failed to fetch')
      }) as unknown as typeof fetch,
    })
    expect(result).toMatchObject({ ok: false, kind: 'unreachable' })
  })
})

// Responses in the shape the gateway's REST surface sends: snake_case fields
// and numeric enums.
const SESSION_OPENED = JSON.stringify({
  session: { id: 'sess-1', tenant_id: 'tenant-1', agent_id: 'agent-1', status: 1 },
})
const turnReply = (turn: Record<string, unknown>) =>
  JSON.stringify({ turn: { id: 'turn-1', session_id: 'sess-1', ...turn } })

// Answers the two sessions API calls in order and records what was sent.
function agentGateway(...responses: Response[]) {
  const fetchImpl = vi.fn(async () => responses.shift() ?? new Response('', { status: 500 }))
  const calls = () =>
    (fetchImpl.mock.calls as unknown as [string, RequestInit][]).map(
      ([url, init]) => ({
        url,
        credentials: init.credentials,
        headers: init.headers as Record<string, string>,
        body: JSON.parse(String(init.body)) as Record<string, unknown>,
      }),
    )
  return { fetchImpl: fetchImpl as unknown as typeof fetch, calls }
}

const AGENT_INPUT = {
  base: 'https://acme.example',
  agentId: 'agent-1',
  apiKey: 'sk-mf-usracct-api01-test',
  message: 'Say hello from Everstack',
}

describe('sendAgentTestRequest', () => {
  it('opens a session, runs a turn and returns the reply', async () => {
    const gw = agentGateway(
      new Response(SESSION_OPENED, { status: 200 }),
      new Response(
        turnReply({ status: 3, assistant_output: 'Hello from Everstack', latency_ms: '868' }),
        { status: 200 },
      ),
    )
    const times = [100, 950]
    const result = await sendAgentTestRequest({
      ...AGENT_INPUT,
      agentModel: '@openai/gpt-5.4-mini',
      fetchImpl: gw.fetchImpl,
      now: () => times.shift() ?? 0,
    })
    expect(result).toEqual({
      ok: true,
      reply: 'Hello from Everstack',
      model: 'gpt-5.4-mini',
      latencyMs: 850,
      sessionId: 'sess-1',
    })
    const [open, turn] = gw.calls()
    expect(open.url).toBe('https://acme.example/v1/agents/sessions')
    expect(open.body).toEqual({ agent_id: 'agent-1' })
    expect(turn.url).toBe('https://acme.example/v1/agents/sessions/sess-1/turns')
    expect(turn.body).toEqual({ user_input: 'Say hello from Everstack' })
  })

  // The regression this replaces: the platform key was sent as a Bearer to
  // /v1/deploy/{agent}/invoke, which only takes deployment keys, so every
  // first agent request answered 401 "invalid API key".
  it('carries the key in the api-key header and never calls deploy invoke', async () => {
    const gw = agentGateway(
      new Response(SESSION_OPENED, { status: 200 }),
      new Response(turnReply({ status: 3, assistant_output: 'hi' }), { status: 200 }),
    )
    await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    for (const call of gw.calls()) {
      expect(call.url).not.toContain('/v1/deploy/')
      expect(call.headers['x-evs-api-key']).toBe(AGENT_INPUT.apiKey)
      expect(call.headers.Authorization).toBeUndefined()
      // No session cookie rides along: the key is what gets tested.
      expect(call.credentials).toBe('omit')
      // The tenant is the key's own: the client never names one.
      expect(call.body).not.toHaveProperty('tenant_id')
      expect(call.body).not.toHaveProperty('tenantId')
    }
  })

  it('reuses the open session for a follow-up message', async () => {
    const gw = agentGateway(
      new Response(turnReply({ status: 3, assistant_output: 'again' }), { status: 200 }),
    )
    const result = await sendAgentTestRequest({
      ...AGENT_INPUT,
      sessionId: 'sess-9',
      fetchImpl: gw.fetchImpl,
    })
    expect(result.ok && result.sessionId).toBe('sess-9')
    expect(gw.calls().map((c) => c.url)).toEqual([
      'https://acme.example/v1/agents/sessions/sess-9/turns',
    ])
  })

  // A turn the model call broke still answers HTTP 200.
  it('reports a failed turn as a failure, not as a reply', async () => {
    const gw = agentGateway(
      new Response(SESSION_OPENED, { status: 200 }),
      new Response(
        turnReply({
          status: 4,
          error:
            'LLM call failed: openai returned 403: Project `proj_x` does not have access to model `gpt-5.5`',
        }),
        { status: 200 },
      ),
    )
    const result = await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.kind).toBe('model-unavailable')
    expect(result.raw).toContain('does not have access to model')
    // The broken session is not carried into the retry.
    expect(result.sessionId).toBeUndefined()
  })

  it('reports a key the gateway refuses when the session is opened', async () => {
    const gw = agentGateway(
      new Response(JSON.stringify({ message: 'invalid API key' }), { status: 401 }),
    )
    const result = await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.kind).toBe('key-rejected')
    expect(gw.calls()).toHaveLength(1)
  })

  it('names a missing agent rather than a missing model', async () => {
    const gw = agentGateway(
      new Response(JSON.stringify({ message: 'agent not found' }), { status: 404 }),
    )
    const result = await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.title).toBe('Agent not found')
    expect(result.retryable).toBe(false)
  })

  it('reports an unreachable gateway', async () => {
    const fetchImpl = vi.fn(async () => {
      throw new TypeError('Failed to fetch')
    })
    const result = await sendAgentTestRequest({
      ...AGENT_INPUT,
      fetchImpl: fetchImpl as unknown as typeof fetch,
    })
    expect(result).toMatchObject({ ok: false, kind: 'unreachable' })
  })

  // A 200 is not a reply on its own: only a completed turn is.
  it.each([
    ['no turn in the body', JSON.stringify({})],
    ['a turn still running', turnReply({ status: 2 })],
    ['a body that is not JSON', 'upstream timeout'],
  ])('never shows %s as a working reply', async (_name, body) => {
    const gw = agentGateway(
      new Response(SESSION_OPENED, { status: 200 }),
      new Response(body, { status: 200 }),
    )
    const result = await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    expect(result.ok).toBe(false)
    if (result.ok) return
    expect(result.title).toBe('Your agent did not finish the turn')
  })

  it('says so when the agent finishes without text', async () => {
    const gw = agentGateway(
      new Response(SESSION_OPENED, { status: 200 }),
      new Response(turnReply({ status: 3, assistant_output: '' }), { status: 200 }),
    )
    const result = await sendAgentTestRequest({ ...AGENT_INPUT, fetchImpl: gw.fetchImpl })
    expect(result.ok && result.reply).toBe(
      'Your agent finished the turn without a text reply.',
    )
  })
})

describe('describeAgentTurnError', () => {
  it('points a rejected or malformed provider key at Providers', () => {
    const failure = describeAgentTurnError(
      'LLM call failed: openai provider misconfigured: the configured API key is malformed',
    )
    expect(failure.kind).toBe('provider-auth')
    expect(failure.detail).toContain('Openai refused the key')
  })

  it('points a missing provider at adding a key', () => {
    expect(describeAgentTurnError('LLM call failed: no providers configured').kind).toBe(
      'no-provider',
    )
  })

  it('keeps the runtime text for the details disclosure on anything else', () => {
    const failure = describeAgentTurnError('tool sandbox_exec timed out')
    expect(failure.kind).toBe('failed')
    expect(failure.title).toBe('Your agent could not finish the turn')
    expect(failure.raw).toBe('tool sandbox_exec timed out')
  })
})
