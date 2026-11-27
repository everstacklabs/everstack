import { describe, expect, it } from 'vitest'
import { safeLocalReturnURL } from './local-return-url'

describe('safeLocalReturnURL', () => {
  it('preserves a local device authorization route', () => {
    expect(safeLocalReturnURL('/device?code=ABCD-EFGH')).toBe(
      '/device?code=ABCD-EFGH',
    )
  })

  it.each([
    ['/traces?id=1#span', '/traces?id=1#span'],
    ['/settings/billing', '/settings/billing'],
    ['%2Fsettings%2Fbilling', '/settings/billing'],
  ])('keeps the local route %s', (input, expected) => {
    expect(safeLocalReturnURL(input)).toBe(expected)
  })

  it('uses the caller fallback', () => {
    expect(safeLocalReturnURL('/.//attacker.example', '/login')).toBe('/login')
  })

  it.each([
    undefined,
    '',
    'device?code=ABCD-EFGH',
    '//attacker.example/device',
    'https://attacker.example/device',
    'javascript:alert(1)',
    '/\\attacker.example/device',
    '\\\\attacker.example',
    'https:attacker.example',
    '%2F%2Fattacker.example',
    'https://app.everstack.ai@attacker.example',
    'https://app.everstack.ai.attacker.example',
    ' //attacker.example',
    '\t//attacker.example',
    '/\t/attacker.example',
    '/.//attacker.example',
    '/a/..//attacker.example',
    '/%2e//attacker.example',
    '/%2E%2E//attacker.example',
    '%2F.%2F%2Fattacker.example',
    '/%2F/attacker.example',
    '/%5Cattacker.example',
  ])('falls back for unsafe input %s', (input) => {
    expect(safeLocalReturnURL(input)).toBe('/')
  })
})
