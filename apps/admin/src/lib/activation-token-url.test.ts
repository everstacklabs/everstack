import { describe, expect, it } from 'vitest'
import { urlWithoutActivationToken } from './activation-token-url'

describe('urlWithoutActivationToken', () => {
  it('drops the token and its fallback flag, keeping the rest', () => {
    expect(
      urlWithoutActivationToken(
        'https://acme.example/settings/billing?upgrade_success=true&plan=pro&activation_token=tok&activation_fallback=true#plans',
      ),
    ).toBe('/settings/billing?upgrade_success=true&plan=pro#plans')
  })

  it('strips a token that arrives without upgrade_success', () => {
    expect(urlWithoutActivationToken('https://acme.example/settings/billing?activation_token=tok')).toBe(
      '/settings/billing',
    )
  })

  it('strips every copy of the parameter', () => {
    expect(
      urlWithoutActivationToken('https://acme.example/settings/billing?activation_token=a&x=1&activation_token=b'),
    ).toBe('/settings/billing?x=1')
  })

  it('returns null when there is nothing to strip', () => {
    expect(urlWithoutActivationToken('https://acme.example/settings/billing?plan=pro')).toBeNull()
    expect(urlWithoutActivationToken('not a url')).toBeNull()
  })
})
