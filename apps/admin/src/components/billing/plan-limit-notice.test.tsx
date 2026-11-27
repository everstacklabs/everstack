// @vitest-environment jsdom

import { render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'
import { PlanLimitNotice } from './plan-limit-notice'

vi.mock('@tanstack/react-router', () => ({
  Link: ({
    to,
    children,
    className,
  }: {
    to: string
    children: ReactNode
    className?: string
  }) => (
    <a href={to} className={className}>
      {children}
    </a>
  ),
}))

describe('PlanLimitNotice', () => {
  it('shows the cap and an Upgrade link to billing', () => {
    render(
      <PlanLimitNotice
        error={{
          code: 8,
          rawMessage:
            'Channel limit reached: your plan includes 3 and you have 3. Upgrade your plan to add more channels: https://everstack.ai/pricing',
        }}
      />,
    )
    expect(screen.getByRole('status').textContent).toContain(
      'your plan includes 3 and you have 3',
    )
    expect(screen.getByText('Upgrade').getAttribute('href')).toBe(
      '/settings/billing',
    )
    expect(screen.getByRole('status').className).toContain('bg-warning-surface')
  })

  it('renders nothing for other errors', () => {
    const { container } = render(
      <PlanLimitNotice error={{ code: 13, rawMessage: 'internal' }} />,
    )
    expect(container.innerHTML).toBe('')
  })
})
