// @vitest-environment jsdom

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { ModelTrial } from '@/lib/model-trial'
import { InlineFirstRequest } from './first-request'

const navigate = vi.fn()
const mutateAsync = vi.fn()
const deleteKey = vi.fn()
const configureProvider = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
}))

vi.mock('@/hooks/vault/use-api-keys', () => ({
  useCreateApiKey: () => ({ mutateAsync, isPending: false }),
  useDeleteApiKey: () => ({ mutateAsync: deleteKey, isPending: false }),
}))

vi.mock('@/lib/api-url', () => ({
  getApiBaseUrl: () => 'https://acme.eu-gra-1.everstack.ai',
}))

// Icons load over the network and would set state after the jsdom
// environment is torn down; nothing here asserts on them.
vi.mock('@everstack/ui/icons', () => ({
  Iconify: { Icon: () => null },
}))

vi.mock('@/hooks/vault/use-providers', () => ({
  useConfigureProvider: () => ({
    mutateAsync: configureProvider,
    isPending: false,
  }),
}))

const TRIAL: ModelTrial = {
  state: 'active',
  eligible: true,
  granted: 20,
  used: 0,
  remaining: 20,
  model: 'gpt-5.4-mini',
  maxOutputTokens: 300,
  expiresAt: null,
  resumesAt: null,
}

const ok = (content: string, headers: Record<string, string> = {}) =>
  new Response(
    JSON.stringify({ model: 'gpt-5.4-mini', choices: [{ message: { content } }] }),
    { status: 200, headers },
  )

type RunnerProps = Partial<Parameters<typeof InlineFirstRequest>[0]>

function runnerElement(props: RunnerProps, onKey = vi.fn()) {
  return (
    <InlineFirstRequest
      activePath="gateway"
      firstAgentId={null}
      model="@openai/gpt-4.1-mini"
      testKey={{ id: 'key-1', secret: 'sk-test-key' }}
      onKey={onKey}
      {...props}
    />
  )
}

function renderRunner(props: RunnerProps) {
  const onKey = vi.fn()
  const view = render(runnerElement(props, onKey))
  return { onKey, rerender: (next: RunnerProps) => view.rerender(runnerElement(next, onKey)) }
}

describe('InlineFirstRequest', () => {
  afterEach(() => {
    cleanup()
    vi.unstubAllGlobals()
    navigate.mockReset()
    mutateAsync.mockReset()
    deleteKey.mockReset()
    configureProvider.mockReset()
  })

  it('points an agent with no model to the provider page instead of sending', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    renderRunner({ model: null, activePath: 'agent', firstAgentId: 'agent-1' })

    expect(screen.getByText('No model to send to yet')).toBeTruthy()
    expect(screen.queryByText('Send test request')).toBeNull()
    fireEvent.click(screen.getByText('Connect a provider'))
    expect(navigate).toHaveBeenCalledWith({ to: '/vault/llm-providers' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  // The agent path used to send the platform key to /v1/deploy/{id}/invoke,
  // which only takes deployment keys: every first request was a 401.
  it('runs an agent turn through the sessions API and keeps the session', async () => {
    const turn = (assistant_output: string) =>
      new Response(JSON.stringify({ turn: { status: 3, assistant_output } }), {
        status: 200,
      })
    const responses = [
      new Response(JSON.stringify({ session: { id: 'sess-1' } }), { status: 200 }),
      turn('Hello from your agent!'),
      turn('Second reply'),
    ]
    const fetchMock = vi.fn(async () => responses.shift()!)
    vi.stubGlobal('fetch', fetchMock)
    renderRunner({
      activePath: 'agent',
      firstAgentId: 'agent-1',
      agentModel: '@openai/gpt-5.4-mini',
    })

    fireEvent.click(screen.getByText('Send test request'))
    expect(await screen.findByText('Hello from your agent!')).toBeTruthy()
    expect(screen.getByText('gpt-5.4-mini')).toBeTruthy()

    fireEvent.change(screen.getByLabelText('Test message'), {
      target: { value: 'And again' },
    })
    fireEvent.click(screen.getByText('Send'))
    expect(await screen.findByText('Second reply')).toBeTruthy()

    const calls = fetchMock.mock.calls as unknown as [string, RequestInit][]
    expect(calls.map(([url]) => url)).toEqual([
      'https://acme.eu-gra-1.everstack.ai/v1/agents/sessions',
      'https://acme.eu-gra-1.everstack.ai/v1/agents/sessions/sess-1/turns',
      // The follow-up lands on the same session.
      'https://acme.eu-gra-1.everstack.ai/v1/agents/sessions/sess-1/turns',
    ])
    for (const [url, init] of calls) {
      expect(url).not.toContain('/v1/deploy/')
      const headers = init.headers as Record<string, string>
      expect(headers['x-evs-api-key']).toBe('sk-test-key')
      expect(headers.Authorization).toBeUndefined()
    }
  })

  it('shows why an agent turn failed instead of a reply', async () => {
    const responses = [
      new Response(JSON.stringify({ session: { id: 'sess-1' } }), { status: 200 }),
      new Response(
        JSON.stringify({
          turn: {
            status: 4,
            error:
              'LLM call failed: openai provider misconfigured: the configured API key is malformed',
          },
        }),
        { status: 200 },
      ),
    ]
    vi.stubGlobal('fetch', vi.fn(async () => responses.shift()!))
    renderRunner({ activePath: 'agent', firstAgentId: 'agent-1' })

    fireEvent.click(screen.getByText('Send test request'))
    expect(
      await screen.findByText('Your provider rejected its API key'),
    ).toBeTruthy()
    // A provider problem is fixed under Providers, not with a new API key.
    expect(screen.getByText('Open providers')).toBeTruthy()
    expect(screen.queryByText('Use a new key')).toBeNull()
  })

  it('sends a chat completion and shows the reply, model and latency', async () => {
    const fetchMock = vi.fn(
      async () =>
        new Response(
          JSON.stringify({
            model: 'gpt-4.1-mini-2025-04-14',
            choices: [{ message: { content: 'Hello from Everstack!' } }],
          }),
          { status: 200 },
        ),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))

    expect(await screen.findByText('Hello from Everstack!')).toBeTruthy()
    expect(screen.getByText('gpt-4.1-mini-2025-04-14')).toBeTruthy()
    expect(screen.getByText(/^\d+ ms$/)).toBeTruthy()

    const [url, init] = fetchMock.mock.calls[0] as unknown as [
      string,
      RequestInit,
    ]
    expect(url).toBe('https://acme.eu-gra-1.everstack.ai/v1/chat/completions')
    expect(JSON.parse(String(init.body)).model).toBe('@openai/gpt-4.1-mini')
    // The existing key is reused; no new key is minted.
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('explains a provider auth failure and links to the providers page', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          // The gateway's real shape for an upstream 401 (FailedPrecondition
          // rendered by the /v1 OpenAI layer as HTTP 500).
          new Response(
            JSON.stringify({
              error: {
                message:
                  'the upstream provider (anthropic) rejected the configured API key: anthropic returned 401: invalid x-api-key (type=authentication_error)',
                type: 'api_error',
                param: null,
                code: null,
              },
            }),
            { status: 500 },
          ),
      ),
    )
    renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))

    expect(
      await screen.findByText('Your provider rejected its API key'),
    ).toBeTruthy()
    expect(screen.getByText(/Anthropic refused the key/)).toBeTruthy()
    // The gateway's own words are kept behind the details disclosure.
    expect(screen.getByText(/Gateway response \(500\)/)).toBeTruthy()
    expect(screen.getByText(/rejected the configured API key/)).toBeTruthy()
    fireEvent.click(screen.getByText('Open providers'))
    expect(navigate).toHaveBeenCalledWith({ to: '/vault/llm-providers' })
  })

  it('names the model and the fix when the gateway cannot route it', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({
              error: {
                code: null,
                message:
                  'model not found: gpt-4.1-mini (no provider found for model (model may not be activated or configured))',
                param: null,
                type: 'invalid_request_error',
              },
            }),
            { status: 404 },
          ),
      ),
    )
    renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))

    expect(
      await screen.findByText('gpt-4.1-mini is not available here'),
    ).toBeTruthy()
    expect(screen.getByText(/No connected provider serves gpt-4.1-mini/)).toBeTruthy()
    // The raw JSON only lives behind the closed details disclosure.
    const raw = screen.getByText(/"invalid_request_error"/)
    expect(raw.closest('details')?.open).toBe(false)
    expect(screen.getByText('Open providers')).toBeTruthy()
  })

  it('revokes the rejected key before offering a fresh one', async () => {
    deleteKey.mockResolvedValue({})
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ error: { message: 'invalid api key' } }),
            { status: 401 },
          ),
      ),
    )
    const { onKey } = renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))

    expect(
      await screen.findByText('This API key was not accepted'),
    ).toBeTruthy()
    fireEvent.click(screen.getByText('Use a new key'))
    await waitFor(() => expect(onKey).toHaveBeenCalledWith(null))
    expect(deleteKey).toHaveBeenCalledWith('key-1')
    expect(mutateAsync).not.toHaveBeenCalled()
  })

  it('shows what is left on free requests and reads the count from the reply', async () => {
    const onTrialUpdate = vi.fn()
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ok('Hi!', { 'x-evs-model-trial': 'remaining=19' })),
    )
    renderRunner({
      model: 'gpt-5.4-mini',
      trial: TRIAL,
      usingTrial: true,
      onTrialUpdate,
    })

    expect(
      screen.getByText(/20 free requests left on GPT-5.4 Mini, text only/),
    ).toBeTruthy()
    fireEvent.click(screen.getByText('Send test request'))
    expect(await screen.findByText('Hi!')).toBeTruthy()
    expect(onTrialUpdate).toHaveBeenCalledWith({ kind: 'remaining', remaining: 19 })
  })

  it('sends only the latest message on a free request', async () => {
    const fetchMock = vi.fn(async () => ok('one')).mockImplementation(
      async () => ok('reply'),
    )
    vi.stubGlobal('fetch', fetchMock)
    renderRunner({ model: 'gpt-5.4-mini', trial: TRIAL, usingTrial: true })

    fireEvent.click(screen.getByText('Send test request'))
    await screen.findByText('reply')
    fireEvent.change(screen.getByLabelText('Test message'), {
      target: { value: 'Second message' },
    })
    fireEvent.click(screen.getByText('Send'))
    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(2))

    const [, init] = fetchMock.mock.calls[1] as unknown as [string, RequestInit]
    expect(JSON.parse(String(init.body)).messages).toEqual([
      { role: 'user', content: 'Second message' },
    ])
  })

  it('warns when free requests run low', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRunner({
      model: 'gpt-5.4-mini',
      trial: { ...TRIAL, remaining: 3, used: 17 },
      usingTrial: true,
    })
    expect(
      screen.getByText('3 free requests left. Add your own key to keep going.'),
    ).toBeTruthy()
  })

  it('replaces a refused free request with the add-key card and resends on save', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              message: 'You have used all your free requests.',
              type: 'insufficient_quota',
              param: null,
              code: 'model_trial_exhausted',
            },
          }),
          { status: 402 },
        ),
      )
      .mockResolvedValueOnce(ok('Hello again'))
    vi.stubGlobal('fetch', fetchMock)
    configureProvider.mockResolvedValue({})
    const onTrialUpdate = vi.fn()
    const { rerender } = renderRunner({
      model: 'gpt-5.4-mini',
      trial: { ...TRIAL, remaining: 1, used: 19 },
      usingTrial: true,
      onTrialUpdate,
    })
    expect(screen.getAllByText(/1 free request left/).length).toBeGreaterThan(0)

    fireEvent.click(screen.getByText('Send test request'))

    expect(
      await screen.findByText(
        'You have used your 20 free requests. Add your own key to keep going.',
      ),
    ).toBeTruthy()
    expect(
      screen.getByText('Your key is used only for requests from this organization.'),
    ).toBeTruthy()
    // The refusal is reported so the trial state moves with it, and the
    // "N left" line never contradicts the card, even before the parent
    // re-renders with the new state.
    expect(onTrialUpdate).toHaveBeenCalledWith({ kind: 'refused', reason: 'exhausted' })
    expect(screen.queryByText(/free requests? left/)).toBeNull()
    // The card is the one next step: no message box that can only be refused.
    expect(screen.queryByLabelText('Test message')).toBeNull()
    expect(screen.getAllByRole('form', { name: 'Add your own provider key' })).toHaveLength(1)

    // The trial re-read comes back exhausted: no model to send to, but the
    // conversation and its inline card stay put.
    rerender({
      model: null,
      trial: { ...TRIAL, state: 'exhausted', remaining: 0, used: 20 },
      usingTrial: false,
    })
    expect(screen.getByText('Say hello from Everstack')).toBeTruthy()
    expect(screen.getAllByRole('form', { name: 'Add your own provider key' })).toHaveLength(1)

    fireEvent.click(screen.getByRole('radio', { name: 'Anthropic' }))
    fireEvent.change(screen.getByLabelText('API key'), {
      target: { value: 'sk-ant-test' },
    })
    fireEvent.click(screen.getByText('Add key and resend'))

    expect(await screen.findByText('Hello again')).toBeTruthy()
    expect(configureProvider).toHaveBeenCalledWith({
      providerName: 'anthropic',
      apiKey: 'sk-ant-test',
      apiKeyName: 'Default',
      apiKeyWeight: 1,
      enabledModels: ['claude-haiku-4-5'],
    })
    // The resend goes to the provider that was just added, and the message
    // is not echoed a second time.
    const [, init] = fetchMock.mock.calls[1] as unknown as [string, RequestInit]
    const resent = JSON.parse(String(init.body))
    expect(resent.model).toBe('@anthropic/claude-haiku-4-5')
    expect(resent.messages).toEqual([
      { role: 'user', content: 'Say hello from Everstack' },
    ])
    expect(screen.getAllByText('Say hello from Everstack')).toHaveLength(1)
    expect(screen.getAllByText(/Using your Anthropic key/).length).toBeGreaterThan(0)
  })

  it('offers the add-key card up front when free requests are used up', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRunner({
      model: null,
      trial: { ...TRIAL, state: 'exhausted', remaining: 0, used: 20 },
    })
    expect(
      screen.getByText(
        'You have used your 20 free requests. Add your own key to keep going.',
      ),
    ).toBeTruthy()
    expect(screen.queryByText('Send test request')).toBeNull()
  })

  it('offers a key straight away on the gateway path when there are no free requests', () => {
    const fetchMock = vi.fn()
    vi.stubGlobal('fetch', fetchMock)
    const onKeyProviderChange = vi.fn()
    renderRunner({ model: null, trial: null, onKeyProviderChange })

    expect(
      screen.getByText('Add an OpenAI or Anthropic key to send your first request.'),
    ).toBeTruthy()
    expect(screen.queryByText('Send test request')).toBeNull()
    // The pick is lifted so the snippet can name the model it will enable.
    fireEvent.click(screen.getByRole('radio', { name: 'Anthropic' }))
    expect(onKeyProviderChange).toHaveBeenCalledWith('anthropic')
    fireEvent.click(screen.getByText('Other providers'))
    expect(navigate).toHaveBeenCalledWith({ to: '/vault/llm-providers' })
    expect(fetchMock).not.toHaveBeenCalled()
  })

  it('says why when free requests are paused or switched off', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRunner({
      model: null,
      trial: { ...TRIAL, state: 'paused', resumesAt: null },
    })
    expect(
      screen.getByText(
        'Free requests are paused. Add your own OpenAI or Anthropic key to keep going now.',
      ),
    ).toBeTruthy()
  })

  it('holds the message box place while the model is still loading', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRunner({ model: null, trial: null, loading: true })
    expect(screen.getByLabelText('Loading')).toBeTruthy()
    expect(screen.queryByText('No model to send to yet')).toBeNull()
    expect(screen.queryByRole('form')).toBeNull()
  })

  it('swaps the message box for the key card when the last free request lands', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn(async () => ok('Last one', { 'x-evs-model-trial': 'remaining=0' })),
    )
    const onTrialUpdate = vi.fn()
    const { rerender } = renderRunner({
      model: 'gpt-5.4-mini',
      trial: { ...TRIAL, remaining: 1, used: 19 },
      usingTrial: true,
      onTrialUpdate,
    })
    fireEvent.click(screen.getByText('Send test request'))
    expect(await screen.findByText('Last one')).toBeTruthy()
    expect(onTrialUpdate).toHaveBeenCalledWith({ kind: 'remaining', remaining: 0 })

    // The parent folds remaining=0 into a used-up trial.
    rerender({
      model: null,
      trial: { ...TRIAL, state: 'exhausted', remaining: 0, used: 20 },
      usingTrial: false,
      onTrialUpdate,
    })
    expect(screen.getByText('Last one')).toBeTruthy()
    expect(
      screen.getByText(
        'You have used your 20 free requests. Add your own key to keep going.',
      ),
    ).toBeTruthy()
    expect(screen.queryByLabelText('Test message')).toBeNull()
    expect(screen.queryByText(/free requests? left/)).toBeNull()
  })

  it('offers billing for a spend limit and a timed retry for a rate limit', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            error: {
              message: 'spend limit',
              type: 'insufficient_quota',
              code: 'spend_limit_exceeded',
            },
          }),
          { status: 402 },
        ),
      )
      .mockResolvedValueOnce(
        new Response('{"error":{"message":"slow down"}}', {
          status: 429,
          headers: { 'retry-after': '2' },
        }),
      )
      .mockResolvedValueOnce(ok('Through'))
    vi.stubGlobal('fetch', fetchMock)
    renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))
    expect(await screen.findByText('Your spend limit is reached')).toBeTruthy()
    fireEvent.click(screen.getByText('Open billing'))
    expect(navigate).toHaveBeenCalledWith(
      expect.objectContaining({ to: '/settings/billing' }),
    )

    fireEvent.change(screen.getByLabelText('Test message'), {
      target: { value: 'Again' },
    })
    fireEvent.click(screen.getByText('Send'))
    expect(await screen.findByText('Rate limit reached')).toBeTruthy()
    // Only the newest error offers a retry, and it waits out Retry-After.
    const wait = screen.getByRole('button', { name: /Try again in \d+s/ })
    expect((wait as HTMLButtonElement).disabled).toBe(true)
    // The live countdown is the only number on the card.
    expect(screen.getByText('Too many requests in a short time.')).toBeTruthy()
    const retry = await screen.findByRole(
      'button',
      { name: 'Try again' },
      { timeout: 3500 },
    )
    fireEvent.click(retry)
    expect(await screen.findByText('Through')).toBeTruthy()
    const [, init] = fetchMock.mock.calls[2] as unknown as [string, RequestInit]
    expect(JSON.parse(String(init.body)).messages.at(-1)).toEqual({
      role: 'user',
      content: 'Again',
    })
    expect(screen.getAllByText('Again')).toHaveLength(1)
  }, 10000)

  it('shows the own-key chip once a provider exists alongside a trial', () => {
    vi.stubGlobal('fetch', vi.fn())
    renderRunner({ model: '@openai/gpt-5.4-mini', trial: TRIAL })
    expect(screen.getByText('Using your OpenAI key')).toBeTruthy()
  })

  it('keeps the key and says so when revoking it fails', async () => {
    deleteKey.mockRejectedValue(new Error('boom'))
    vi.stubGlobal(
      'fetch',
      vi.fn(
        async () =>
          new Response(
            JSON.stringify({ error: { message: 'invalid api key' } }),
            { status: 401 },
          ),
      ),
    )
    const { onKey } = renderRunner({})

    fireEvent.click(screen.getByText('Send test request'))
    fireEvent.click(await screen.findByText('Use a new key'))

    expect(
      await screen.findByText(/Could not remove the rejected key/),
    ).toBeTruthy()
    expect(onKey).not.toHaveBeenCalled()
    // Sending again would reuse the rejected key, so no retry is offered.
    expect(screen.queryByRole('button', { name: /Try again/ })).toBeNull()
  })
})
