import { describe, expect, it } from 'vitest'
import { isFeatureGateError, cleanErrorMessage } from './feature-gate-error'

describe('isFeatureGateError', () => {
  it.each([
    { code: 9, message: '[failed_precondition] Voice requires a higher plan. feature not available: voice' },
    { code: 'failed_precondition', message: 'Alerts requires a higher plan. ' },
    { code: 9, message: 'Voice is an Enterprise feature. Upgrade at https://everstack.ai/pricing to access it.' },
    { message: '[failed_precondition] Alerts requires a higher plan.' }, // code only in message
  ])('detects a feature-gate denial (%o)', (err) => {
    expect(isFeatureGateError(err)).toBe(true)
  })

  it.each([
    { code: 9, message: '[failed_precondition] You must configure a provider first' }, // FP but not a gate
    { code: 16, message: 'unauthenticated' },
    { code: 7, message: 'permission denied' },
    { code: 2, message: 'Voice requires a higher plan.' }, // gate message but wrong code
    { message: 'network error' },
    null,
    undefined,
    'nope',
  ])('does NOT misclassify (%o)', (err) => {
    expect(isFeatureGateError(err)).toBe(false)
  })
})

describe('cleanErrorMessage', () => {
  it('strips the [code] prefix', () => {
    expect(cleanErrorMessage({ message: '[internal] boom' })).toBe('boom')
    expect(cleanErrorMessage(new Error('[unavailable] gateway down'))).toBe('gateway down')
  })
  it('falls back for empty input', () => {
    expect(cleanErrorMessage(null)).toBe('Something went wrong.')
    expect(cleanErrorMessage({})).toBe('Something went wrong.')
  })
})
