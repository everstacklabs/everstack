// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { LoadBalancerForm } from './load-balancer-form'
import type { LoadBalancerConfig } from './types'

vi.mock('@/hooks/deployments/use-gateway-models', () => ({
  useGatewayModels: () => ({ data: [] }),
}))
vi.mock('@everstack/ui/icons', () => ({ Iconify: { Icon: () => null } }))
vi.mock('@everstack/ui/components', () => {
  const Pass = ({ children }: { children?: ReactNode }) => <div>{children}</div>
  return {
    Label: Pass,
    Select: Pass,
    SelectContent: Pass,
    SelectItem: Pass,
    SelectTrigger: Pass,
    SelectValue: () => null,
    Switch: () => <input type="checkbox" />,
    Input: () => <input />,
    Button: Pass,
  }
})

afterEach(cleanup)

const config = {
  enabled: true,
  strategy: 'round_robin',
  keySource: 'api_key',
  fallback: { enabled: true, factors: [] },
} as unknown as LoadBalancerConfig

describe('LoadBalancerForm billed attempts', () => {
  it('says each attempt is billed on a billing (cloud) instance', () => {
    render(<LoadBalancerForm config={config} onChange={() => {}} billedAttempts />)
    const note = screen.getByRole('note')
    expect(note.textContent).toMatch(/Each attempt is billed/)
    expect(note.textContent).toMatch(/including the ones that lose the race/)
    expect(note.textContent).toMatch(/interrupted stream is charged for the tokens it generated/)
    expect(note.textContent).toMatch(/context summaries/)
    expect(note.textContent ?? '').not.toMatch(/[—–]/)
  })

  it('says nothing about billing on a self-hosted gateway', () => {
    render(<LoadBalancerForm config={config} onChange={() => {}} />)
    expect(screen.queryByRole('note')).toBeNull()
  })
})
