import { describe, expect, it, vi } from 'vitest'
import { reportIfFirstKey } from './first-key-event'

describe('reportIfFirstKey', () => {
  it('reports when the instance had no keys', () => {
    const capture = vi.fn()
    expect(reportIfFirstKey([], capture)).toBe(true)
    expect(capture).toHaveBeenCalledWith('first_key_created', {})
  })

  it('stays quiet when keys already existed', () => {
    const capture = vi.fn()
    expect(reportIfFirstKey([{ id: 'k1' }], capture)).toBe(false)
    expect(capture).not.toHaveBeenCalled()
  })

  it('stays quiet when the list never loaded', () => {
    const capture = vi.fn()
    expect(reportIfFirstKey(undefined, capture)).toBe(false)
    expect(capture).not.toHaveBeenCalled()
  })

  it('never lets a throwing capture fail key creation', () => {
    expect(() =>
      reportIfFirstKey([], () => {
        throw new Error('blocked')
      }),
    ).not.toThrow()
  })
})
