// @vitest-environment jsdom

// The setup card against a real query, not a mocked retry: a query with no
// data drops its error as soon as a refetch starts, which is exactly when the
// card is in the middle of re-resolving the assistant.
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from '@testing-library/react'
import { QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { NoModelProviderError } from '@/server/platform-agent'
import { AssistantSetupCard, useStickyError } from './assistant-setup'

const configureProvider = vi.fn()

vi.mock('@tanstack/react-router', () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock('@everstack/ui/icons', () => ({
  Iconify: { Icon: () => null },
}))

vi.mock('@/hooks/vault/use-providers', () => ({
  useConfigureProvider: () => ({
    mutateAsync: configureProvider,
    isPending: false,
  }),
}))

vi.mock('@/server/platform-agent', () => {
  class NoModelProviderError extends Error {}
  return { NoModelProviderError }
})

// The composer's wiring, reduced to what decides whether the card is shown.
function Composer({ resolveAssistant }: { resolveAssistant: () => Promise<{ id: string }> }) {
  const { data, error, refetch } = useQuery({
    queryKey: ['platform-agent', 'org-1'],
    queryFn: resolveAssistant,
    retry: false,
    staleTime: Infinity,
  })
  const setupError = useStickyError(error, !!data)
  return (
    <>
      {data ? <p>assistant ready</p> : null}
      {setupError ? (
        <AssistantSetupCard
          error={setupError}
          retry={async () => Boolean((await refetch()).data)}
          settleGapMs={20}
        />
      ) : null}
    </>
  )
}

function renderComposer(resolveAssistant: () => Promise<{ id: string }>) {
  const client = new QueryClient()
  return render(
    <QueryClientProvider client={client}>
      <Composer resolveAssistant={resolveAssistant} />
    </QueryClientProvider>,
  )
}

const KEY_TITLE = 'Add an OpenAI or Anthropic key to chat with the assistant.'

async function saveKey() {
  expect(await screen.findByText(KEY_TITLE)).toBeTruthy()
  fireEvent.change(screen.getByLabelText('API key'), {
    target: { value: 'sk-provider-key' },
  })
  fireEvent.click(screen.getByText('Add key'))
}

afterEach(() => {
  cleanup()
  configureProvider.mockReset()
})

describe('assistant setup against a real query', () => {
  it('stays up through its retries until the assistant exists', async () => {
    configureProvider.mockResolvedValue({})
    // Not routable on the first two tries after the key is saved.
    let calls = 0
    const seen: (string | null)[] = []
    const resolveAssistant = vi.fn(async () => {
      calls += 1
      // A real resolve is a network round trip, so the screen has rendered
      // by the time it answers.
      await new Promise((r) => setTimeout(r, 10))
      // What the user sees while each re-resolve is in flight.
      if (calls > 1) {
        seen.push(
          screen.queryByText('Starting the assistant')
            ? 'starting'
            : screen.queryByLabelText('API key')
              ? 'key form'
              : null,
        )
      }
      if (calls <= 3) throw new NoModelProviderError()
      return { id: 'assistant-1' }
    })
    renderComposer(resolveAssistant)

    await saveKey()

    expect(await screen.findByText('assistant ready')).toBeTruthy()
    expect(resolveAssistant).toHaveBeenCalledTimes(4)
    // Every retry ran under the progress line: the card never unmounted and
    // the empty key form never flashed back.
    expect(seen).toEqual(['starting', 'starting', 'starting'])
    expect(screen.queryByText(KEY_TITLE)).toBeNull()
  })

  it('says the key is saved when the assistant still is not up', async () => {
    configureProvider.mockResolvedValue({})
    const resolveAssistant = vi.fn(async () => {
      throw new NoModelProviderError()
    })
    renderComposer(resolveAssistant)

    await saveKey()

    expect(
      await screen.findByText(
        'Your key is saved. The assistant is still starting.',
      ),
    ).toBeTruthy()
    // The key is saved once: the form does not come back asking for it again.
    expect(screen.queryByLabelText('API key')).toBeNull()
    expect(configureProvider).toHaveBeenCalledTimes(1)

    // One more round on demand.
    const before = resolveAssistant.mock.calls.length
    fireEvent.click(screen.getByText('Try again'))
    await waitFor(() =>
      expect(resolveAssistant.mock.calls.length).toBeGreaterThan(before),
    )
  })
})
