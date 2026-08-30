import { describe, expect, it } from 'vitest'
import { safeLocalReturnURL } from './local-return-url'

describe('safeLocalReturnURL', () => {
  it('preserves a local device authorization route', () => {
    expect(safeLocalReturnURL('/device?code=ABCD-EFGH')).toBe(
      '/device?code=ABCD-EFGH',
    )
  })

  it.each([
    undefined,
    '',
    'device?code=ABCD-EFGH',
    '//attacker.example/device',
    'https://attacker.example/device',
    'javascript:alert(1)',
  ])('falls back for unsafe input %s', (input) => {
    expect(safeLocalReturnURL(input)).toBe('/')
  })
})
