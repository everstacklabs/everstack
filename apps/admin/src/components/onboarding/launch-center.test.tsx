// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useOnboardingStore } from '@/stores/onboarding-store'
import { LaunchCenter } from './launch-center'

let cloud = false
let selectedPath: 'agent' | 'gateway' | 'production' | null = null

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
  useSearch: () => ({}),
}))
vi.mock('@/lib/cloud-mode', () => ({
  isCloudManaged: () => cloud,
}))
vi.mock('@/hooks/use-onboarding', () => ({
  resolveCurrentStep: () => undefined,
  useOnboarding: () => ({
    selectedPath,
    activePath: selectedPath ?? 'agent',
    launchSteps: [],
    launchAllComplete: false,
    firstAgentId: null,
    firstAgent: null,
    hasOpenAI: false,
    hasProvider: false,
    testModel: null,
    trial: null,
    usingTrial: false,
  }),
}))
vi.mock('@/hooks/billing/use-model-trial', () => ({
  MODEL_TRIAL_QUERY_KEY: ['billing', 'model-trial'],
}))
// Heavy dialogs and data hooks the path chooser never renders.
vi.mock('@/components/deployments/agents', () => ({
  AgentFormDialog: () => null,
}))
vi.mock('@/components/providers', () => ({
  ConfigureProviderSheet: () => null,
}))
vi.mock('@/hooks/vault/use-api-keys', () => ({
  useCreateApiKey: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useDeleteApiKey: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock('@/hooks/deployments/use-agents', () => ({
  useCreateAgent: () => ({ mutateAsync: vi.fn(), isPending: false }),
  useUpdateAgent: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock('@/hooks/deployments/use-sandbox', () => ({
  useCreateSandbox: () => ({ mutateAsync: vi.fn(), isPending: false }),
}))
vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries: vi.fn(), setQueryData: vi.fn() }),
}))

describe('LaunchCenter path chooser', () => {
  beforeEach(() => {
    useOnboardingStore.setState({ selectedPath: null, hydrated: true })
  })
  afterEach(() => {
    cleanup()
    cloud = false
    selectedPath = null
  })

  it('hides Hosted Instance on a cloud-managed instance', () => {
    cloud = true
    render(<LaunchCenter />)
    expect(screen.getByText('Agent API')).toBeTruthy()
    expect(screen.getByText('Gateway Proxy')).toBeTruthy()
    expect(screen.queryByText('Hosted Instance')).toBeNull()
    expect(screen.queryByText(/self-hosted/)).toBeNull()
    expect(screen.getByText('Skip setup for now')).toBeTruthy()
  })

  it('sends a cloud user with a stored hosted path back to the chooser', () => {
    cloud = true
    selectedPath = 'production'
    render(<LaunchCenter />)
    expect(screen.getByText('Agent API')).toBeTruthy()
    expect(screen.queryByText('Hosted Instance')).toBeNull()
    expect(screen.queryByText('Open Everstack Cloud')).toBeNull()
  })

  it('keeps Hosted Instance for self-hosted installs', () => {
    render(<LaunchCenter />)
    expect(screen.getByText('Hosted Instance')).toBeTruthy()
    expect(screen.getByText(/self-hosted/)).toBeTruthy()
  })
})
