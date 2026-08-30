// @vitest-environment jsdom

import { cleanup, render } from '@testing-library/react'
import { afterEach, describe, expect, it } from 'vitest'
import { EverstackLogo } from './everstack-logo'

afterEach(cleanup)

describe('EverstackLogo', () => {
  it('bounds the mark instead of falling back to the PNG dimensions', () => {
    const { getByRole } = render(<EverstackLogo />)
    const logo = getByRole('img', { name: 'Everstack' })

    expect(logo.style.width).toBe('30px')
    expect(logo.style.height).toBe('32px')
  })

  it('uses an explicit compact mark size', () => {
    const { getByRole } = render(<EverstackLogo size="sm" />)
    const logo = getByRole('img', { name: 'Everstack' })

    expect(logo.style.width).toBe('23px')
    expect(logo.style.height).toBe('24px')
  })

  it('uses an explicit compact wordmark size', () => {
    const { getByRole } = render(
      <EverstackLogo variant="wordmark" size="sm" />,
    )
    const logo = getByRole('img', { name: 'Everstack' })

    expect(logo.style.width).toBe('112px')
    expect(logo.style.height).toBe('18px')
  })
})
