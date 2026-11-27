// @vitest-environment jsdom
//
// Session replay masks the text of every element matching
// REPLAY_MASK_TEXT_SELECTOR, checking ancestors the way rrweb does. These
// tests render the shared components that show secrets and customer content
// and assert that the customer text inside them is masked, so a refactor that
// drops the marker fails here rather than shipping prompts to analytics.
import { render, screen, waitFor } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import { beforeAll, describe, expect, it } from 'vitest'
import {
  ChatAssistantMessage,
  ChatTranscript,
  ChatUserMessage,
} from '@/components/deployments/agents/chat-presentation'
import { AgentMarkdown } from '@/components/deployments/agents/agent-markdown'
import { ToolCallCard } from '@/components/deployments/agents/tool-call-card'
import { MustacheTextarea } from '@/components/evaluations/mustache-textarea'
import { ResponsiveTable } from '@/ui/table'
import { isReplayReadableRoute, REPLAY_MASK_TEXT_SELECTOR } from './replay-privacy'

const SECRET = 'customer-prompt-7f3a'

beforeAll(() => {
  globalThis.ResizeObserver ??= class {
    observe() {}
    unobserve() {}
    disconnect() {}
  } as unknown as typeof ResizeObserver
})

function withProviders(node: ReactNode) {
  return <QueryClientProvider client={new QueryClient()}>{node}</QueryClientProvider>
}

/** Every text node containing SECRET sits inside a masked element. */
function expectSecretMasked(container: HTMLElement) {
  const walker = document.createTreeWalker(container, NodeFilter.SHOW_TEXT)
  let found = 0
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    if (!node.textContent?.includes(SECRET)) continue
    found++
    expect(node.parentElement?.closest(REPLAY_MASK_TEXT_SELECTOR), node.textContent).not.toBeNull()
  }
  expect(found, 'secret text was not rendered at all').toBeGreaterThan(0)
}

describe('shared renderers of customer content are masked in replay', () => {
  it('chat transcript and messages', () => {
    for (const node of [
      <ChatTranscript><span>{SECRET}</span></ChatTranscript>,
      <ChatUserMessage><span>{SECRET}</span></ChatUserMessage>,
      <ChatAssistantMessage><span>{SECRET}</span></ChatAssistantMessage>,
    ]) {
      const { container, unmount } = render(node)
      expectSecretMasked(container)
      unmount()
    }
  })

  it('agent markdown (completions rendered as HTML)', async () => {
    const { container } = render(<AgentMarkdown>{`Hello **${SECRET}**`}</AgentMarkdown>)
    await waitFor(() => expect(container.textContent).toContain(SECRET))
    expectSecretMasked(container)
  })

  it('tool call card', () => {
    const { container } = render(
      withProviders(<ToolCallCard toolCallId="t1" toolName="sandbox_exec" toolArgs={JSON.stringify({ command: SECRET })} toolResult={SECRET} />),
    )
    // Collapsed by default: the root still carries the marker.
    expect(container.querySelector('[data-ph-mask]')).not.toBeNull()
    const text = screen.queryAllByText(new RegExp(SECRET))
    for (const el of text) expect(el.closest(REPLAY_MASK_TEXT_SELECTOR)).not.toBeNull()
  })

  it('table cells (trace lists, logs, datasets, eval runs)', () => {
    const { container } = render(
      withProviders(
        <ResponsiveTable
          tableId="replay-test"
          enableColumnPersistence={false}
          columns={[{ id: 'input', header: 'Input', accessor: 'input' }]}
          data={[{ input: SECRET }]}
          rowKey={(row: { input: string }) => row.input}
        />,
      ),
    )
    expectSecretMasked(container)
    // Column headers stay readable.
    const header = screen.getByText('Input')
    expect(header.closest(REPLAY_MASK_TEXT_SELECTOR)).toBeNull()
  })

  it('prompt template editor highlight mirror', () => {
    const { container } = render(<MustacheTextarea value={`Answer: ${SECRET}`} onChange={() => {}} />)
    expectSecretMasked(container)
  })
})

describe('page content is masked by default', () => {
  it('masks sensitive routes and anything not on the allowlist', () => {
    for (const path of [
      '/',
      '/observability/traces',
      '/chat',
      '/deployments/agents/a1/sessions',
      '/deployments/sandboxes/s1',
      '/evaluations/datasets/d1',
      '/vault',
      '/settings/integrations',
      '/settings/members',
      '/some-future-page',
    ]) {
      expect(isReplayReadableRoute(path), path).toBe(false)
    }
  })

  it('leaves the allowlisted pages readable', () => {
    for (const path of ['/license', '/license/status-and-usage', '/settings/billing', '/settings/billing/']) {
      expect(isReplayReadableRoute(path), path).toBe(true)
    }
    expect(isReplayReadableRoute('/settings/billing-export')).toBe(false)
  })
})
