// Pure helpers for the free model requests ("model trial") the playground can
// run on before an organization adds its own provider key. Copy follows
// docs/design/free-trial-onboarding.md section 7.2. React-free so it can be
// unit tested on its own.

export type ModelTrialState =
  | 'active'
  | 'exhausted'
  | 'expired'
  | 'paused'
  | 'not_eligible'

export interface ModelTrial {
  state: ModelTrialState
  eligible: boolean
  granted: number
  used: number
  remaining: number
  // The one model free requests are served on, bare (e.g. "gpt-5.4-mini").
  // Sent as-is: the gateway refuses the "@openai/" form on the trial.
  model: string
  maxOutputTokens: number
  // Unset until the first free request is taken.
  expiresAt: Date | null
  // Set while paused by the daily ceiling: the next 00:00 UTC.
  resumesAt: Date | null
}

// Below this many remaining requests the playground nudges toward a key.
export const RUNNING_LOW_AT = 3

// Reasons the gateway refuses a free request, from error.code
// "model_trial_<reason>" (402, or 429 for rate_limited).
export type TrialRefusal =
  | 'exhausted'
  | 'expired'
  | 'paused'
  | 'rate_limited'
  | 'text_only'
  | 'tools_unsupported'
  | 'input_too_large'
  | 'model_unsupported'
  | 'not_eligible'

const REFUSALS: readonly TrialRefusal[] = [
  'exhausted',
  'expired',
  'paused',
  'rate_limited',
  'text_only',
  'tools_unsupported',
  'input_too_large',
  'model_unsupported',
  'not_eligible',
]

const CODE_PREFIX = 'model_trial_'

export function trialRefusalFromCode(
  code: string | undefined,
): TrialRefusal | null {
  if (!code?.startsWith(CODE_PREFIX)) return null
  const reason = code.slice(CODE_PREFIX.length) as TrialRefusal
  return REFUSALS.includes(reason) ? reason : null
}

// "remaining=7" from the x-evs-model-trial response header.
export function parseTrialHeader(
  value: string | null | undefined,
): number | null {
  const match = /(?:^|[;,\s])remaining=(\d+)/.exec(value ?? '')
  return match ? Number(match[1]) : null
}

// "gpt-5.4-mini" -> "GPT-5.4 Mini"; anything unfamiliar is returned as-is.
export function displayModelName(model: string): string {
  const match = /^gpt-([0-9][0-9a-z.]*)(?:-(.+))?$/i.exec(model)
  if (!match) return model
  const suffix = match[2]
    ? ' ' +
      match[2]
        .split('-')
        .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
        .join(' ')
    : ''
  return `GPT-${match[1]}${suffix}`
}

export function formatTrialDate(date: Date): string {
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'long' }).format(date)
}

function plural(n: number): string {
  return `${n} free request${n === 1 ? '' : 's'}`
}

// What a free request covers and when they expire, without the count.
export function trialTermsLine(trial: ModelTrial): string {
  const terms = `Text only on ${displayModelName(trial.model)}, up to ${trial.maxOutputTokens} output tokens each.`
  return trial.expiresAt
    ? `${terms} They expire on ${formatTrialDate(trial.expiresAt)}.`
    : terms
}

// Screen 5: the line under the message box while free requests are in use.
export function trialStatusLine(trial: ModelTrial): string {
  const head = `${plural(trial.remaining)} left on ${displayModelName(trial.model)}, text only, up to ${trial.maxOutputTokens} output tokens each.`
  const expiry = trial.expiresAt
    ? ` They expire on ${formatTrialDate(trial.expiresAt)}.`
    : ''
  return `${head}${expiry} Then add your own OpenAI or Anthropic key.`
}

export function isRunningLow(trial: ModelTrial): boolean {
  return trial.state === 'active' && trial.remaining <= RUNNING_LOW_AT
}

// Screen 7. Shown with trialTermsLine, so the count appears once.
export function runningLowLine(trial: ModelTrial): string {
  return `${plural(trial.remaining)} left. Add your own key to keep going.`
}

// What a send tells the trial reader: the exact count from the response
// header, a refusal the gateway just made, or "re-read, the header was not
// readable".
export type TrialUpdate =
  | { kind: 'remaining'; remaining: number }
  | { kind: 'refused'; reason: TrialRefusal }
  | { kind: 'reread' }

// Folds a refusal the gateway just sent into the local trial, so the status
// under the composer agrees with the card the moment it appears, before the
// re-read lands. Input-shaped refusals (text only, too large, tools) leave
// the trial as it is: it can still serve the next plain message.
export function applyTrialRefusal(
  trial: ModelTrial,
  reason: TrialRefusal,
): ModelTrial {
  switch (reason) {
    case 'exhausted':
      return { ...trial, state: 'exhausted', remaining: 0, used: trial.granted }
    case 'expired':
      return { ...trial, state: 'expired', remaining: 0 }
    case 'paused':
      return { ...trial, state: 'paused' }
    case 'not_eligible':
      return { ...trial, state: 'not_eligible', eligible: false, remaining: 0 }
    default:
      return trial
  }
}

// Folds the exact count from the x-evs-model-trial header into the local
// trial. The last free request leaves it at zero, which the server reports
// as used up, so the state moves with it: the playground then offers a key
// instead of a send that can only be refused.
export function applyTrialRemaining(
  trial: ModelTrial,
  remaining: number,
): ModelTrial {
  const next = {
    ...trial,
    remaining,
    used: Math.max(0, trial.granted - remaining),
  }
  return remaining === 0 && trial.state === 'active'
    ? { ...next, state: 'exhausted' }
    : next
}

export interface AddKeyCopy {
  title: string
}

// Screens 8 to 11: the headline of the inline add-your-key card. `reason` is
// the gateway's refusal when one just happened, otherwise the trial's state.
// `gatewayMessage` covers reasons whose numbers only the gateway knows.
export function addKeyCopy(
  reason: TrialRefusal | ModelTrialState,
  trial: ModelTrial | null,
  gatewayMessage?: string,
): AddKeyCopy {
  const name = displayModelName(trial?.model ?? 'gpt-5.4-mini')
  switch (reason) {
    case 'exhausted':
      return {
        title: trial?.granted
          ? `You have used your ${trial.granted} free requests. Add your own key to keep going.`
          : 'You have used your free requests. Add your own key to keep going.',
      }
    case 'expired':
      return {
        title: trial?.expiresAt
          ? `Your free requests expired on ${formatTrialDate(trial.expiresAt)}. Add your own OpenAI or Anthropic key to keep going.`
          : 'Your free requests have expired. Add your own OpenAI or Anthropic key to keep going.',
      }
    case 'paused':
      return {
        title: trial?.resumesAt
          ? 'Free requests are paused until 00:00 UTC. Add your own OpenAI or Anthropic key to keep going now.'
          : 'Free requests are paused. Add your own OpenAI or Anthropic key to keep going now.',
      }
    case 'text_only':
      return {
        title: 'Free requests are text only. Add your own key to send images.',
      }
    case 'tools_unsupported':
    case 'model_unsupported':
      return {
        title: `Free requests cover chat with ${name} only. Add your own key to use tools or other models.`,
      }
    case 'input_too_large':
      return {
        title:
          gatewayMessage ||
          'This message is too long for a free request. Add your own key to send longer prompts.',
      }
    case 'rate_limited':
      return {
        title:
          gatewayMessage ||
          'Free requests are limited to one every few seconds. Try again shortly.',
      }
    default:
      return {
        title:
          'Free requests are not available for this organization. Add your own OpenAI or Anthropic key to keep going.',
      }
  }
}

// The add-key card when there are no free requests to speak of (switched
// off, or unavailable): a plain first step, not a refusal.
export const FIRST_KEY_TITLE =
  'Add an OpenAI or Anthropic key to send your first request.'

// Screen 12: "Using your OpenAI key". Reads the provider off a gateway model
// reference such as "@openai/gpt-5.4-mini".
export function ownKeyChip(model: string): string {
  const provider = /^@([a-z0-9_-]+)\//i.exec(model)?.[1]
  const names: Record<string, string> = {
    openai: 'OpenAI',
    anthropic: 'Anthropic',
    google: 'Google',
    groq: 'Groq',
    mistral: 'Mistral',
    deepseek: 'DeepSeek',
  }
  const label = provider ? (names[provider] ?? provider) : null
  return label ? `Using your ${label} key` : 'Using your own key'
}
