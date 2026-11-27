import { describe, expect, it } from 'vitest'
import {
  addKeyCopy,
  applyTrialRefusal,
  applyTrialRemaining,
  displayModelName,
  isRunningLow,
  ownKeyChip,
  parseTrialHeader,
  runningLowLine,
  trialRefusalFromCode,
  trialStatusLine,
  trialTermsLine,
  type ModelTrial,
} from './model-trial'

const trial: ModelTrial = {
  state: 'active',
  eligible: true,
  granted: 20,
  used: 3,
  remaining: 17,
  model: 'gpt-5.4-mini',
  maxOutputTokens: 300,
  expiresAt: new Date(Date.UTC(2026, 10, 25, 12)),
  resumesAt: null,
}

describe('parseTrialHeader', () => {
  it('reads remaining=<n> and ignores anything else', () => {
    expect(parseTrialHeader('remaining=7')).toBe(7)
    expect(parseTrialHeader('remaining=0')).toBe(0)
    expect(parseTrialHeader(null)).toBeNull()
    expect(parseTrialHeader('nope')).toBeNull()
  })
})

describe('trialRefusalFromCode', () => {
  it('maps the gateway codes and nothing else', () => {
    expect(trialRefusalFromCode('model_trial_exhausted')).toBe('exhausted')
    expect(trialRefusalFromCode('model_trial_rate_limited')).toBe(
      'rate_limited',
    )
    expect(trialRefusalFromCode('model_trial_input_too_large')).toBe(
      'input_too_large',
    )
    expect(trialRefusalFromCode('spend_limit_exceeded')).toBeNull()
    expect(trialRefusalFromCode('model_trial_something_new')).toBeNull()
    expect(trialRefusalFromCode(undefined)).toBeNull()
  })
})

describe('applyTrialRefusal', () => {
  it('moves the local trial to the state the gateway just refused with', () => {
    expect(applyTrialRefusal(trial, 'exhausted')).toMatchObject({
      state: 'exhausted',
      remaining: 0,
      used: 20,
    })
    expect(applyTrialRefusal(trial, 'expired').state).toBe('expired')
    expect(applyTrialRefusal(trial, 'paused').state).toBe('paused')
    expect(applyTrialRefusal(trial, 'not_eligible')).toMatchObject({
      state: 'not_eligible',
      eligible: false,
    })
  })

  it('leaves an active trial alone on input-shaped refusals', () => {
    for (const reason of ['text_only', 'tools_unsupported', 'input_too_large', 'rate_limited'] as const) {
      expect(applyTrialRefusal(trial, reason)).toEqual(trial)
    }
  })
})

describe('copy', () => {
  it('names the model the way the design does', () => {
    expect(displayModelName('gpt-5.4-mini')).toBe('GPT-5.4 Mini')
    expect(displayModelName('claude-haiku-4-5')).toBe('claude-haiku-4-5')
  })

  it('writes the status line from the trial', () => {
    expect(trialStatusLine(trial)).toBe(
      `17 free requests left on GPT-5.4 Mini, text only, up to 300 output tokens each. They expire on ${new Intl.DateTimeFormat(undefined, { dateStyle: 'long' }).format(trial.expiresAt!)}. Then add your own OpenAI or Anthropic key.`,
    )
    expect(trialStatusLine({ ...trial, expiresAt: null, remaining: 1 })).toBe(
      '1 free request left on GPT-5.4 Mini, text only, up to 300 output tokens each. Then add your own OpenAI or Anthropic key.',
    )
  })

  it('folds the header count in and marks the trial used up at zero', () => {
    const seven = applyTrialRemaining(trial, 7)
    expect(seven).toMatchObject({ state: 'active', remaining: 7, used: 13 })
    const zero = applyTrialRemaining(trial, 0)
    expect(zero).toMatchObject({ state: 'exhausted', remaining: 0, used: 20 })
    // The card for that state names the same count the trial carried.
    expect(addKeyCopy(zero.state, zero).title).toBe(
      'You have used your 20 free requests. Add your own key to keep going.',
    )
    // A paused trial stays paused; only an active one is used up by a count.
    expect(applyTrialRemaining({ ...trial, state: 'paused' }, 0).state).toBe(
      'paused',
    )
  })

  it('writes the terms without the count, for next to the running-low line', () => {
    expect(trialTermsLine({ ...trial, expiresAt: null })).toBe(
      'Text only on GPT-5.4 Mini, up to 300 output tokens each.',
    )
    expect(trialTermsLine(trial)).not.toMatch(/\d+ free request/)
  })

  it('runs low at three', () => {
    expect(isRunningLow({ ...trial, remaining: 4 })).toBe(false)
    expect(isRunningLow({ ...trial, remaining: 3 })).toBe(true)
    expect(runningLowLine({ ...trial, remaining: 3 })).toBe(
      '3 free requests left. Add your own key to keep going.',
    )
  })

  it('uses the design copy for each card', () => {
    expect(addKeyCopy('exhausted', trial).title).toBe(
      'You have used your 20 free requests. Add your own key to keep going.',
    )
    expect(addKeyCopy('expired', { ...trial, state: 'expired' }).title).toMatch(
      /^Your free requests expired on .+\. Add your own OpenAI or Anthropic key to keep going\.$/,
    )
    expect(
      addKeyCopy('paused', { ...trial, resumesAt: new Date() }).title,
    ).toBe(
      'Free requests are paused until 00:00 UTC. Add your own OpenAI or Anthropic key to keep going now.',
    )
    expect(addKeyCopy('text_only', trial).title).toBe(
      'Free requests are text only. Add your own key to send images.',
    )
    expect(addKeyCopy('tools_unsupported', trial).title).toBe(
      'Free requests cover chat with GPT-5.4 Mini only. Add your own key to use tools or other models.',
    )
    expect(
      addKeyCopy(
        'input_too_large',
        trial,
        'Free requests accept up to 4000 bytes of text per request. Add your own key to send longer prompts.',
      ).title,
    ).toContain('4000 bytes')
    expect(addKeyCopy('exhausted', null).title).toBe(
      'You have used your free requests. Add your own key to keep going.',
    )
    expect(addKeyCopy('not_eligible', null).title).toContain(
      'not available for this organization',
    )
  })

  it('names the provider on the own-key chip', () => {
    expect(ownKeyChip('@openai/gpt-5.4-mini')).toBe('Using your OpenAI key')
    expect(ownKeyChip('@anthropic/claude-haiku-4-5')).toBe(
      'Using your Anthropic key',
    )
    expect(ownKeyChip('gpt-5.4-mini')).toBe('Using your own key')
  })
})
