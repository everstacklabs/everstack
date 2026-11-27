// @vitest-environment jsdom

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NoModelProviderError } from '@/server/platform-agent'
import { AssistantSetupCard } from './assistant-setup'
import { QuickTopics } from './quick-topics'

const navigate = vi.fn()
const invalidateQueries = vi.fn(async () => undefined)
const configureProvider = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => navigate,
  Link: ({
    to,
    children,
    ...rest
  }: {
    to: string
    children: React.ReactNode
  }) => (
    <a href={to} {...rest}>
      {children}
    </a>
  ),
}))

vi.mock('@tanstack/react-query', () => ({
  useQueryClient: () => ({ invalidateQueries }),
}))

// Icons load over the network and would set state after the jsdom
// environment is torn down; nothing here asserts on them.
vi.mock('@everstack/ui/icons', () => ({
  Iconify: { Icon: () => null },
}))

vi.mock('@/hooks/vault/use-providers', () => ({
  useConfigureProvider: () => ({
    mutateAsync: configureProvider,
    isPending: false,
  }),
}))

// The real module builds API clients on import; only the error class matters.
vi.mock('@/server/platform-agent', () => {
  class NoModelProviderError extends Error {}
  return { NoModelProviderError }
})

afterEach(() => {
  cleanup()
  navigate.mockReset()
  invalidateQueries.mockClear()
  configureProvider.mockReset()
})

describe('AssistantSetupCard', () => {
  // The state a new organization lands in after onboarding on free requests:
  // no provider, so the assistant cannot be created. The composer used to
  // swallow the message here.
  it('offers the key step when no model provider is connected', async () => {
    configureProvider.mockResolvedValue({})
    const retry = vi.fn(async () => true)
    render(<AssistantSetupCard error={new NoModelProviderError()} retry={retry} />)

    expect(
      screen.getByText(
        'Add an OpenAI or Anthropic key to chat with the assistant.',
      ),
    ).toBeTruthy()
    fireEvent.change(screen.getByLabelText('API key'), {
      target: { value: 'sk-provider-key' },
    })
    fireEvent.click(screen.getByText('Add key'))

    await waitFor(() => expect(retry).toHaveBeenCalledTimes(1))
    expect(configureProvider).toHaveBeenCalledWith(
      expect.objectContaining({ providerName: 'openai', apiKey: 'sk-provider-key' }),
    )
    // The model list is re-read before the assistant is resolved again.
    expect(invalidateQueries).toHaveBeenCalledWith({
      queryKey: ['gateway-models'],
    })
  })

  it('links to every other provider', () => {
    render(
      <AssistantSetupCard
        error={new NoModelProviderError()}
        retry={async () => true}
      />,
    )
    fireEvent.click(screen.getByText('Other providers'))
    expect(navigate).toHaveBeenCalledWith({ to: '/vault/llm-providers' })
  })

  it('shows any other failure with its reason and a retry', async () => {
    const retry = vi.fn(async () => true)
    render(
      <AssistantSetupCard
        error={new Error('listAgents failed: unavailable')}
        retry={retry}
      />,
    )
    expect(screen.getByText('The assistant could not start.')).toBeTruthy()
    expect(screen.getByText('listAgents failed: unavailable')).toBeTruthy()
    expect(screen.queryByLabelText('API key')).toBeNull()

    fireEvent.click(screen.getByText('Try again'))
    await waitFor(() => expect(retry).toHaveBeenCalledTimes(1))
  })
})

describe('QuickTopics', () => {
  // Every pill used to set a value nothing read. Now each one acts.
  it('puts a starter prompt in the composer for what the assistant can do', () => {
    const onPrompt = vi.fn()
    render(<QuickTopics onPrompt={onPrompt} />)
    fireEvent.click(screen.getByText('Agents'))
    expect(onPrompt).toHaveBeenCalledWith(
      'List my agents and what each one does.',
    )
  })

  it('opens the product area for everything else', () => {
    render(<QuickTopics onPrompt={vi.fn()} />)
    const href = (label: string) =>
      screen.getByText(label).closest('a')?.getAttribute('href')
    expect(href('Observability')).toBe('/observability/traces')
    expect(href('Gateway')).toBe('/gateway')
    expect(href('Evaluations')).toBe('/evaluations')
    expect(href('Vault')).toBe('/vault/api-keys')
    expect(href('Sandbox')).toBe('/deployments/sandboxes')
  })
})
