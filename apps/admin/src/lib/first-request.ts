import { parseTrialHeader, trialRefusalFromCode } from '@/lib/model-trial'

// Pure helpers behind the onboarding "Send test request" runner: pick a cheap
// model for whichever provider is configured, and turn the gateway's reply into
// something a first-time user can act on. Kept free of React so the request
// and error mapping can be unit tested without rendering the launch center.

// Cheap, fast chat models per catalog provider, best first. A test request
// only has to prove the route works, so it should cost next to nothing. The
// first one the provider has enabled wins; otherwise the first enabled model.
const CHEAP_TEST_MODELS: Record<string, readonly string[]> = {
  openai: ['gpt-5.4-mini', 'gpt-5-mini', 'gpt-4.1-mini'],
  anthropic: ['claude-haiku-4-5', 'claude-haiku-4-5-20251001', 'claude-haiku-4.5'],
  google: ['gemini-2.5-flash-lite'],
  groq: ['llama-3.1-8b-instant'],
  mistral: ['mistral-small-latest'],
  deepseek: ['deepseek-chat'],
}

// The slice of a provider row (ProviderStatus) the picker reads.
export interface TestModelProvider {
  catalog?: { name?: string }
  isActive?: boolean
  configuration?: { enabledModels?: string[] }
}

// The slice of the gateway's ListModels reply (ProviderModels) the picker reads.
export interface ServableModels {
  provider: string
  models: string[]
}

// "config" is a placeholder entry the provider sheet also filters out.
function cleanModels(models: readonly string[] | undefined): string[] {
  return (models ?? []).filter((m) => m.trim().toLowerCase() !== 'config')
}

function enabledModels(p: TestModelProvider): string[] {
  return cleanModels(p.configuration?.enabledModels)
}

/**
 * ListConfiguredProviders returns every catalog provider, configured or not,
 * so a row only counts when it is active and has an enabled model. The
 * gateway skips configs with no enabled models, so those cannot route either.
 */
export function isUsableProvider(p: TestModelProvider): boolean {
  return p.isActive === true && enabledModels(p).length > 0
}

export interface TestModel {
  provider: string
  // Gateway model reference, e.g. "@openai/gpt-4.1-mini".
  model: string
}

// The model a freshly connected provider is enabled with, so a key added
// from the playground can route a request straight away.
export function cheapModelFor(provider: string): string | null {
  return CHEAP_TEST_MODELS[provider]?.[0] ?? null
}

// The providers a key can be added for inline, from the onboarding step.
export type KeyProvider = 'openai' | 'anthropic'
export const DEFAULT_KEY_PROVIDER: KeyProvider = 'openai'

// Gateway model reference a key added inline is enabled with, e.g.
// "@openai/gpt-5.4-mini". The add-key card enables exactly this model.
export function keyModelFor(provider: KeyProvider): string {
  return `@${provider}/${cheapModelFor(provider)}`
}

/**
 * The model the gateway quickstart snippet names. Always a real model, never
 * a placeholder: the routable test model when there is one (the bare trial
 * model while free requests are in use), otherwise the model a key for the
 * chosen provider would be enabled with, so the snippet already reads the
 * way it will work once that key is added.
 */
export function quickstartModel(
  testModel: string | null,
  keyProvider: KeyProvider = DEFAULT_KEY_PROVIDER,
): string {
  return testModel ?? keyModelFor(keyProvider)
}

function cheapest(provider: string, enabled: readonly string[]): string | null {
  if (enabled.length === 0) return null
  const preferred = CHEAP_TEST_MODELS[provider] ?? []
  return preferred.find((m) => enabled.includes(m)) ?? enabled[0]
}

function modelFor(p: TestModelProvider): TestModel | null {
  const provider = p.catalog?.name
  if (!provider || !isUsableProvider(p)) return null
  // The gateway only routes enabled models, so the cheap pick is used only
  // when it is enabled for this provider.
  const name = cheapest(provider, enabledModels(p))
  return name ? { provider, model: `@${provider}/${name}` } : null
}

/**
 * Picks the model the test request (and the gateway quickstart snippet) uses.
 * Only usable providers count (see isUsableProvider). Providers with a known
 * cheap model win, in catalog preference order, so the choice is stable no
 * matter which order the providers were connected in.
 */
export function pickTestModel(
  providers: readonly TestModelProvider[],
): TestModel | null {
  for (const name of Object.keys(CHEAP_TEST_MODELS)) {
    const match = providers.find((p) => p.catalog?.name === name)
    const picked = match ? modelFor(match) : null
    if (picked) return picked
  }
  for (const p of providers) {
    const picked = modelFor(p)
    if (picked) return picked
  }
  return null
}

/**
 * Same choice, read from what the gateway says it can serve (ListModels, the
 * data behind /v1/models). Covers instances whose providers come from YAML
 * config rather than the console, where ListConfiguredProviders is empty.
 */
export function pickServableModel(
  servable: readonly ServableModels[],
): TestModel | null {
  const rows = servable
    .map((s) => ({ provider: s.provider.toLowerCase(), models: cleanModels(s.models) }))
    .filter((s) => s.provider && s.models.length > 0)
  const ordered = [
    ...Object.keys(CHEAP_TEST_MODELS)
      .map((name) => rows.find((r) => r.provider === name))
      .filter((r): r is NonNullable<typeof r> => r !== undefined),
    ...rows,
  ]
  for (const row of ordered) {
    const name = cheapest(row.provider, row.models)
    if (name) return { provider: row.provider, model: `@${row.provider}/${name}` }
  }
  return null
}

export type TestRequestErrorKind =
  | 'no-provider'
  | 'key-rejected'
  | 'provider-auth'
  | 'model-unavailable'
  | 'plan-limit'
  | 'rate-limited'
  // A free-request refusal (error.code "model_trial_*"): the fix is an own key.
  | 'trial'
  | 'unreachable'
  | 'failed'

export interface TestRequestFailure {
  ok: false
  kind: TestRequestErrorKind
  // Plain-language headline and next step; never the gateway's raw body.
  title: string
  detail: string
  status?: number
  // The gateway's own error code when it sent one (e.g. "spend_limit_exceeded").
  code?: string
  // Seconds to wait, from a 429's Retry-After header.
  retryAfterSeconds?: number
  // False when sending again cannot help (a local step failed first).
  retryable?: boolean
  // `detail` without the "Try again in N seconds." tail, for a UI that
  // counts the wait down live instead of quoting a number that goes stale.
  detailWithoutWait?: string
  // The gateway's reply, for an optional details disclosure.
  raw?: string
  // Free requests left, when the gateway said so (x-evs-model-trial).
  trialRemaining?: number
}

export type TestRequestResult =
  | {
      ok: true
      reply: string
      model: string
      latencyMs: number
      trialRemaining?: number
    }
  | TestRequestFailure

// Pulls the human message out of the gateway's OpenAI-style envelope
// ({"error":{"message"}}), the native {"message"} shape, or plain text.
export function gatewayErrorMessage(raw: string): string {
  const text = raw.trim()
  if (!text) return ''
  try {
    const json = JSON.parse(text)
    const message =
      (typeof json?.error === 'string' ? json.error : json?.error?.message) ??
      json?.message
    if (typeof message === 'string' && message.trim()) return message.trim()
  } catch {
    // Not JSON: fall through to the raw text.
  }
  return text
}

// The gateway's machine-readable error code, if the body carries one.
export function gatewayErrorCode(raw: string): string | undefined {
  try {
    const code = JSON.parse(raw)?.error?.code
    return typeof code === 'string' && code ? code : undefined
  } catch {
    return undefined
  }
}

// The error's type (e.g. "insufficient_quota"); some refusals carry their
// reason here instead of in a string code.
function gatewayErrorType(raw: string): string | undefined {
  try {
    const type = JSON.parse(raw)?.error?.type
    return typeof type === 'string' && type ? type : undefined
  } catch {
    return undefined
  }
}

// Pretty-prints a JSON body for the details disclosure; leaves text alone.
function prettyRaw(raw: string): string | undefined {
  const text = raw.trim()
  if (!text) return undefined
  try {
    return JSON.stringify(JSON.parse(text), null, 2).slice(0, 2000)
  } catch {
    return text.slice(0, 2000)
  }
}

// Retry-After is either delta-seconds or an HTTP date.
export function parseRetryAfter(
  value: string | null | undefined,
  now: number = Date.now(),
): number | undefined {
  if (!value?.trim()) return undefined
  const seconds = Number(value)
  if (Number.isFinite(seconds)) {
    return seconds > 0 ? Math.ceil(seconds) : undefined
  }
  const at = Date.parse(value)
  if (Number.isNaN(at)) return undefined
  const delta = Math.ceil((at - now) / 1000)
  return delta > 0 ? delta : undefined
}

// Model name without the "@provider/" routing prefix, for copy.
export function bareModel(model: string | undefined): string | undefined {
  return model?.replace(/^@[^/]+\//, '') || undefined
}

function titleCase(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1)
}

// A 402 names its reason in error.code (or error.type on older envelopes).
// Each reason gets its own next step; the reason itself is never shown.
function quotaCopy(
  code: string | undefined,
  type: string | undefined,
): { title: string; detail: string } {
  const reason = code ?? type
  switch (reason) {
    case 'spend_limit_exceeded':
      return {
        title: 'Your spend limit is reached',
        detail:
          'This organization has hit the spend limit set in Billing. It resets at 00:00 UTC, or you can raise it now.',
      }
    case 'insufficient_credit':
      return {
        title: 'Out of credit',
        detail: 'Add credit in Billing, then send again.',
      }
    case 'wallet_frozen':
      return {
        title: 'Billing needs attention',
        detail:
          'Requests are on hold until the payment issue in Billing is resolved.',
      }
    case 'model_not_in_free_allowlist':
      return {
        title: 'This model is not on the free plan',
        detail:
          'Upgrade your plan in Billing, or send to a model the free plan includes.',
      }
    default:
      return {
        title: 'Spend or plan limit reached',
        detail:
          'A spend limit or plan allowance has been used up. Review your limits in Billing, then try again.',
      }
  }
}

export interface DescribeErrorContext {
  // The model the request asked for, named in the copy when it cannot route.
  requestedModel?: string
  retryAfter?: string | null
}

// Appends "Try again in N seconds." when a Retry-After header was sent.
function withRetry(detail: string, seconds: number | undefined): string {
  return seconds
    ? `${detail} Try again in ${seconds} second${seconds === 1 ? '' : 's'}.`
    : detail
}

/**
 * Maps a failed gateway response to a plain-language title and next step.
 * The gateway's own message never becomes the headline; it is kept in `raw`
 * for a details disclosure.
 */
export function describeTestRequestError(
  status: number,
  raw: string,
  context: DescribeErrorContext = {},
): TestRequestFailure {
  const message = gatewayErrorMessage(raw).slice(0, 300)
  const lower = message.toLowerCase()
  const code = gatewayErrorCode(raw)
  const base = { ok: false as const, status, code, raw: prettyRaw(raw) }

  if (status === 412 || lower.includes('no providers configured')) {
    return {
      ...base,
      kind: 'no-provider',
      title: 'No model provider configured',
      detail: 'Add a provider key so the gateway has a model to route to.',
    }
  }
  // The gateway reports a provider rejecting its configured key as
  // FailedPrecondition, which the /v1 OpenAI layer renders as HTTP 500 (or
  // 400), so this is matched on the message whatever the status.
  if (
    lower.includes('rejected the configured api key') ||
    (lower.includes('upstream provider') && lower.includes('api key'))
  ) {
    const provider = /upstream provider \(([a-z0-9_-]+)\)/i.exec(message)?.[1]
    return {
      ...base,
      kind: 'provider-auth',
      title: 'Your provider rejected its API key',
      detail: `${provider ? titleCase(provider) : 'The provider'} refused the key configured on this instance. Check the key under Providers, then try again.`,
    }
  }
  // 401 means the gateway did not accept our API key (unknown, revoked or
  // deleted). A 403 is a permission or plan refusal, not a bad key.
  if (status === 401) {
    return {
      ...base,
      kind: 'key-rejected',
      title: 'This API key was not accepted',
      detail:
        'The gateway did not recognise the key used for this request. Create a new onboarding key and send again.',
    }
  }
  if (
    status === 404 ||
    (lower.includes('model') &&
      (lower.includes('not found') ||
        lower.includes('not enabled') ||
        lower.includes('does not exist') ||
        lower.includes('unknown')))
  ) {
    const model = bareModel(context.requestedModel)
    return {
      ...base,
      kind: 'model-unavailable',
      title: model ? `${model} is not available here` : 'Model not available',
      detail: `No connected provider serves ${model ?? 'this model'}. Enable it under the provider that hosts it, or connect that provider, then send again.`,
    }
  }
  // Free-request refusals carry their own code and copy; the card that
  // renders them picks the design copy by reason, so the message is kept
  // only as a fallback.
  const refusal = trialRefusalFromCode(code)
  if (refusal === 'rate_limited') {
    const retryAfterSeconds = parseRetryAfter(context.retryAfter)
    return {
      ...base,
      kind: 'rate-limited',
      title: 'Free requests are limited to one every few seconds',
      detail: withRetry('Give it a moment.', retryAfterSeconds),
      detailWithoutWait: 'Give it a moment.',
      retryAfterSeconds,
    }
  }
  if (refusal) {
    return {
      ...base,
      kind: 'trial',
      title: 'Add your own key to keep going',
      detail: message,
    }
  }
  if (status === 402) {
    return { ...base, kind: 'plan-limit', ...quotaCopy(code, gatewayErrorType(raw)) }
  }
  if (status === 429) {
    const retryAfterSeconds = parseRetryAfter(context.retryAfter)
    return {
      ...base,
      kind: 'rate-limited',
      title: 'Rate limit reached',
      detail: retryAfterSeconds
        ? withRetry('Too many requests in a short time.', retryAfterSeconds)
        : 'Too many requests in a short time. Wait a moment, then try again.',
      detailWithoutWait: 'Too many requests in a short time.',
      retryAfterSeconds,
    }
  }
  if (status >= 500) {
    return {
      ...base,
      kind: 'failed',
      title: 'The provider did not complete the request',
      detail:
        'The upstream provider returned an error. Try again in a moment; if it keeps failing, check the provider under Providers.',
    }
  }
  return {
    ...base,
    kind: 'failed',
    title: 'The request was rejected',
    detail: `The gateway answered with status ${status}. Open the details below for its reply.`,
  }
}

// Reads the reply text out of an OpenAI chat completion or an agent invoke.
function replyText(json: any): string {
  const content = json?.choices?.[0]?.message?.content
  if (typeof content === 'string') return content
  if (Array.isArray(content)) {
    return content
      .map((part: any) => (typeof part?.text === 'string' ? part.text : ''))
      .join('')
  }
  return String(json?.outputText ?? json?.message ?? JSON.stringify(json))
}

export interface TestRequestInput {
  url: string
  apiKey: string
  body: Record<string, unknown>
  // The model the request asked for, shown when the reply does not echo one.
  requestedModel?: string
  fetchImpl?: typeof fetch
  now?: () => number
}

/**
 * Sends one request through this instance's gateway and reports the reply,
 * the model that answered, and the round-trip latency. The key is only used
 * for the Authorization header of this call; it is never logged or stored.
 */
export async function sendTestRequest({
  url,
  apiKey,
  body,
  requestedModel,
  fetchImpl = fetch,
  now = () => performance.now(),
}: TestRequestInput): Promise<TestRequestResult> {
  const started = now()
  let res: Response
  try {
    res = await fetchImpl(url, {
      method: 'POST',
      headers: {
        Authorization: `Bearer ${apiKey}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify(body),
    })
  } catch {
    return {
      ok: false,
      kind: 'unreachable',
      title: 'Could not reach the gateway',
      detail: 'Check your network connection and try again.',
    }
  }
  const raw = await res.text()
  const latencyMs = Math.round(now() - started)
  const remaining = parseTrialHeader(res.headers.get('x-evs-model-trial'))
  const trialRemaining = remaining === null ? undefined : remaining
  if (!res.ok) {
    return {
      ...describeTestRequestError(res.status, raw, {
        requestedModel,
        retryAfter: res.headers.get('retry-after'),
      }),
      trialRemaining,
    }
  }

  let reply = raw
  let model = requestedModel ?? ''
  try {
    const json = JSON.parse(raw)
    reply = replyText(json)
    if (typeof json?.model === 'string' && json.model) model = json.model
  } catch {
    // Keep the raw text as the reply.
  }
  return { ok: true, reply: reply.slice(0, 600), model, latencyMs, trialRemaining }
}

// ─── Agent path ──────────────────────────────────────────────────────

// The header a platform API key travels in everywhere outside the OpenAI
// verbs. Only /v1/chat/completions and its siblings take it as a Bearer.
const API_KEY_HEADER = 'x-evs-api-key'

// TurnStatus on the REST surface arrives as a number (grpc-gateway) or, on
// some encoders, as its enum name. 3 is TURN_STATUS_COMPLETED, 4 is FAILED.
function turnFailed(turn: any): boolean {
  const status = turn?.status
  return (
    status === 4 ||
    status === 'TURN_STATUS_FAILED' ||
    (typeof turn?.error === 'string' && turn.error.trim() !== '')
  )
}

// Only a finished turn counts as a reply. Anything else (no turn in the
// body, or one still pending or running) must not be shown as "Works".
function turnCompleted(turn: any): boolean {
  if (!turn || typeof turn !== 'object' || turnFailed(turn)) return false
  const status = turn.status
  return status === 3 || status === 'TURN_STATUS_COMPLETED'
}

/**
 * Maps a turn the agent could not finish to a plain-language title and next
 * step. A failed turn still answers HTTP 200: the reason is in `turn.error`,
 * almost always the model call. The runtime's own text is kept in `raw`.
 */
export function describeAgentTurnError(error: string): TestRequestFailure {
  const message = error.trim().slice(0, 600)
  const lower = message.toLowerCase()
  const base = { ok: false as const, raw: message || undefined }
  const provider = /\b([a-z0-9_-]+) (?:provider misconfigured|returned \d{3})/i.exec(
    message,
  )?.[1]
  const named = provider ? titleCase(provider) : 'The provider'

  if (lower.includes('no providers configured') || lower.includes('no provider')) {
    return {
      ...base,
      kind: 'no-provider',
      title: 'No model provider configured',
      detail: 'Your agent has no provider to call. Add a provider key, then send again.',
    }
  }
  if (
    lower.includes('does not have access to model') ||
    lower.includes('model_not_found') ||
    (lower.includes('model') &&
      (lower.includes('not found') ||
        lower.includes('not enabled') ||
        lower.includes('does not exist')))
  ) {
    return {
      ...base,
      kind: 'model-unavailable',
      title: "Your agent's model is not available",
      detail: `${named} does not serve the model this agent is set to. Enable it under Providers, or pick another model for the agent, then send again.`,
    }
  }
  if (
    lower.includes('provider misconfigured') ||
    lower.includes('rejected the configured api key') ||
    (lower.includes('api key') && /returned 40[13]/.test(lower))
  ) {
    return {
      ...base,
      kind: 'provider-auth',
      title: 'Your provider rejected its API key',
      detail: `${named} refused the key configured on this instance. Check the key under Providers, then try again.`,
    }
  }
  return {
    ...base,
    kind: 'failed',
    title: 'Your agent could not finish the turn',
    detail:
      'The request reached your agent, but its model call failed. Open the details below for the reason.',
  }
}

export interface AgentTestRequestInput {
  // This instance's gateway origin, no trailing slash.
  base: string
  agentId: string
  apiKey: string
  message: string
  // The session an earlier send opened, so the test reads as one
  // conversation. Omitted on the first send.
  sessionId?: string | null
  // The agent's model, shown next to the reply.
  agentModel?: string
  fetchImpl?: typeof fetch
  now?: () => number
}

// The reply, plus the session to send the next message on. A failed send
// carries no session, so the next one starts a fresh conversation.
export type AgentTestRequestResult = TestRequestResult & { sessionId?: string }

const UNREACHABLE: TestRequestFailure = {
  ok: false,
  kind: 'unreachable',
  title: 'Could not reach the gateway',
  detail: 'Check your network connection and try again.',
}

/**
 * Runs one turn on an agent through the sessions API, the same two calls the
 * SDK quickstart makes: open a session, then run a turn on it. The API key
 * goes in the api-key header; the tenant is always the key's own, so no
 * tenant is ever sent from here.
 *
 * (The deploy invoke endpoint is not used: it takes a deployment key, which a
 * starter agent does not have, so a platform key there is always a 401.)
 */
export async function sendAgentTestRequest({
  base,
  agentId,
  apiKey,
  message,
  sessionId,
  agentModel,
  fetchImpl = fetch,
  now = () => performance.now(),
}: AgentTestRequestInput): Promise<AgentTestRequestResult> {
  const started = now()
  const post = async (path: string, body: Record<string, unknown>) => {
    try {
      const res = await fetchImpl(`${base}${path}`, {
        method: 'POST',
        // The key alone authenticates, exactly as it will from the user's
        // own code: the console's session cookie is left out, so a reply
        // here proves the key works rather than the browser session.
        credentials: 'omit',
        headers: {
          [API_KEY_HEADER]: apiKey,
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(body),
      })
      return { status: res.status, ok: res.ok, raw: await res.text() }
    } catch {
      return null
    }
  }
  // An agent the key cannot see reads as a missing agent, not a missing model.
  const httpFailure = (status: number, raw: string): TestRequestFailure =>
    status === 404
      ? {
          ok: false,
          kind: 'failed',
          status,
          raw: prettyRaw(raw),
          title: 'Agent not found',
          detail:
            'This agent no longer exists on the instance. Create an agent, then send again.',
          retryable: false,
        }
      : describeTestRequestError(status, raw)

  let session = sessionId ?? null
  if (!session) {
    const opened = await post('/v1/agents/sessions', { agent_id: agentId })
    if (!opened) return UNREACHABLE
    if (!opened.ok) return httpFailure(opened.status, opened.raw)
    try {
      const id = JSON.parse(opened.raw)?.session?.id
      session = typeof id === 'string' && id ? id : null
    } catch {
      session = null
    }
    if (!session) {
      return {
        ok: false,
        kind: 'failed',
        raw: prettyRaw(opened.raw),
        title: 'Could not start a session with your agent',
        detail: 'The gateway did not return a session. Try again in a moment.',
      }
    }
  }

  const ran = await post(
    `/v1/agents/sessions/${encodeURIComponent(session)}/turns`,
    { user_input: message },
  )
  if (!ran) return UNREACHABLE
  if (!ran.ok) return httpFailure(ran.status, ran.raw)
  const latencyMs = Math.round(now() - started)

  let turn: any
  try {
    turn = JSON.parse(ran.raw)?.turn
  } catch {
    turn = undefined
  }
  if (turnFailed(turn)) {
    return describeAgentTurnError(typeof turn?.error === 'string' ? turn.error : '')
  }
  if (!turnCompleted(turn)) {
    return {
      ok: false,
      kind: 'failed',
      raw: prettyRaw(ran.raw),
      title: 'Your agent did not finish the turn',
      detail:
        'The gateway answered, but without a finished turn. Send again in a moment.',
    }
  }
  const output = turn?.assistant_output ?? turn?.assistantOutput
  const reply =
    typeof output === 'string' && output.trim()
      ? output
      : 'Your agent finished the turn without a text reply.'
  return {
    ok: true,
    reply: reply.slice(0, 600),
    model: bareModel(agentModel) ?? '',
    latencyMs,
    sessionId: session,
  }
}
