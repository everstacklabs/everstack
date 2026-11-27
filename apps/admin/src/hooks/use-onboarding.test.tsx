// @vitest-environment jsdom

import { renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useOnboardingStore } from '@/stores/onboarding-store'
import { resolveCurrentStep, useOnboarding } from './use-onboarding'
import type { ModelTrial } from '@/lib/model-trial'

type ProviderRow = {
  catalog: { name: string }
  isActive: boolean
  configuration?: { enabledModels: string[] }
}

// ListConfiguredProviders returns every catalog provider, configured or not.
const CATALOG_ROWS: ProviderRow[] = ['openai', 'anthropic', 'groq'].map(
  (name) => ({ catalog: { name }, isActive: false }),
)

let providers: ProviderRow[] = []
let apiKeys: { id: string }[] = []
let servable: { provider: string; models: string[] }[] = []
let trial: ModelTrial | null = null

vi.mock('@/hooks/vault/use-providers', () => ({
  useConfiguredProviders: () => ({ data: { providers }, isLoading: false }),
}))
vi.mock('@/hooks/vault/use-api-keys', () => ({
  useApiKeys: () => ({ data: apiKeys, isLoading: false }),
}))
vi.mock('@/hooks/deployments/use-agents', () => ({
  useAgents: () => ({ data: [], isLoading: false }),
}))
vi.mock('@/hooks/deployments/use-gateway-models', () => ({
  useGatewayModels: () => ({ data: servable, isLoading: false }),
}))
vi.mock('@/hooks/billing/use-model-trial', () => ({
  useModelTrial: () => ({ data: trial, isLoading: false }),
}))
vi.mock('@/hooks/use-instance-has-data', () => ({
  useInstanceHasData: () => ({ hasInstanceData: false }),
}))

describe('useOnboarding launch steps', () => {
  beforeEach(() => {
    providers = CATALOG_ROWS
    apiKeys = []
    servable = []
    trial = null
    useOnboardingStore.setState({ selectedPath: null, hydrated: true })
  })

  it('starts the gateway path with the provider key, with no density step', () => {
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.launchSteps.map((s) => s.id)).toEqual([
      'provider',
      'api-key',
      'test',
    ])
  })

  it('drops the density step from the agent path too', () => {
    useOnboardingStore.setState({ selectedPath: 'agent' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.launchSteps.map((s) => s.id)).toEqual([
      'provider',
      'agent',
      'sandbox',
      'api-key',
      'test',
    ])
  })

  it('treats unconfigured catalog rows as no provider', () => {
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.hasProvider).toBe(false)
    expect(result.current.hasOpenAI).toBe(false)
    expect(result.current.testModel).toBeNull()
    expect(result.current.launchSteps[0]).toMatchObject({
      id: 'provider',
      complete: false,
    })
  })

  it('counts progress over the remaining steps for a Groq-only org', () => {
    providers = [
      ...CATALOG_ROWS.filter((p) => p.catalog.name !== 'groq'),
      {
        catalog: { name: 'groq' },
        isActive: true,
        configuration: { enabledModels: ['llama-3.1-8b-instant'] },
      },
    ]
    apiKeys = [{ id: 'k1' }]
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.launchCompletedCount).toBe(2)
    expect(result.current.launchSteps).toHaveLength(3)
    expect(result.current.launchAllComplete).toBe(false)
    expect(result.current.testModel?.model).toBe('@groq/llama-3.1-8b-instant')
  })

  it('runs on free requests when there is no key of its own', () => {
    trial = {
      state: 'active',
      eligible: true,
      granted: 20,
      used: 0,
      remaining: 20,
      model: 'gpt-5.4-mini',
      maxOutputTokens: 300,
      expiresAt: null,
      resumesAt: null,
    }
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.usingTrial).toBe(true)
    expect(result.current.hasProvider).toBe(true)
    // Bare model: the gateway refuses the @openai/ form on the trial.
    expect(result.current.testModel?.model).toBe('gpt-5.4-mini')
    expect(result.current.launchSteps[0]).toMatchObject({
      complete: true,
      actionLabel: 'Free requests active',
    })
  })

  it('does not run the agent path on free requests', () => {
    trial = {
      state: 'active',
      eligible: true,
      granted: 20,
      used: 0,
      remaining: 20,
      model: 'gpt-5.4-mini',
      maxOutputTokens: 300,
      expiresAt: null,
      resumesAt: null,
    }
    useOnboardingStore.setState({ selectedPath: 'agent' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.usingTrial).toBe(false)
    expect(result.current.hasProvider).toBe(false)
    expect(result.current.hasOpenAI).toBe(false)
    expect(result.current.testModel).toBeNull()
  })

  it('prefers the own key over free requests', () => {
    trial = {
      state: 'active',
      eligible: true,
      granted: 20,
      used: 0,
      remaining: 20,
      model: 'gpt-5.4-mini',
      maxOutputTokens: 300,
      expiresAt: null,
      resumesAt: null,
    }
    servable = [{ provider: 'anthropic', models: ['claude-haiku-4-5'] }]
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.usingTrial).toBe(false)
    expect(result.current.testModel?.model).toBe('@anthropic/claude-haiku-4-5')
  })

  it('does not count used-up free requests as a provider', () => {
    trial = {
      state: 'exhausted',
      eligible: true,
      granted: 20,
      used: 20,
      remaining: 0,
      model: 'gpt-5.4-mini',
      maxOutputTokens: 300,
      expiresAt: null,
      resumesAt: null,
    }
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.usingTrial).toBe(false)
    expect(result.current.hasProvider).toBe(false)
    expect(result.current.testModel).toBeNull()
  })

  it('counts a provider the gateway can serve from YAML config', () => {
    // No console-configured provider, but ListModels (the data behind
    // /v1/models) says the gateway routes Anthropic.
    servable = [{ provider: 'anthropic', models: ['claude-haiku-4-5'] }]
    useOnboardingStore.setState({ selectedPath: 'gateway' })
    const { result } = renderHook(() => useOnboarding())
    expect(result.current.hasProvider).toBe(true)
    expect(result.current.hasOpenAI).toBe(false)
    expect(result.current.testModel?.model).toBe('@anthropic/claude-haiku-4-5')
    expect(result.current.launchSteps[0].complete).toBe(true)
  })
})

describe('resolveCurrentStep', () => {
  const steps = [
    { id: 'provider', complete: true },
    { id: 'api-key', complete: false },
    { id: 'test', complete: false },
  ]

  it('honours a step pinned in the URL', () => {
    expect(resolveCurrentStep(steps, 'test')?.id).toBe('test')
  })

  it('falls back to the first incomplete step for a stale density link', () => {
    expect(resolveCurrentStep(steps, 'density')?.id).toBe('api-key')
  })

  it('lands on the last step once everything is complete', () => {
    const done = steps.map((s) => ({ ...s, complete: true }))
    expect(resolveCurrentStep(done, 'density')?.id).toBe('test')
  })
})
