// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import type { AgentSession } from '@/server/agents'
import type { OpenAISession } from '@/server/openai-agents'
import { SessionsList } from './sessions-list'

vi.mock('@tanstack/react-router', () => ({ useNavigate: () => vi.fn() }))
vi.mock('@/hooks/deployments/use-agents', () => ({
  useAgents: () => ({ data: [] }),
  useCancelSession: () => ({ isPending: false, mutateAsync: vi.fn() }),
}))
vi.mock('@/ui/table', () => ({
  ResponsiveTable: ({ data }: { data: Array<{ id: string }> }) => (
    <div data-testid="rows">{data.map((row) => row.id).join(',')}</div>
  ),
}))
vi.mock('@everstack/ui/components', () => ({
  toast: { success: vi.fn(), error: vi.fn() },
}))
vi.mock('@everstack/ui/icons', () => ({
  Iconify: { Icon: () => null },
}))
vi.mock('@everstack/utils/functions/index', () => ({
  formatTimestamp: () => '',
}))
vi.mock('@/components/deployments/channels/channel-status-badge', () => ({
  PlatformSourceBadge: () => null,
}))

afterEach(cleanup)

const native = {
  id: 'native-1',
  agentId: 'agent-1',
  createdAt: { seconds: 10n },
  totalTokens: 0,
  turnCount: 0,
} as unknown as AgentSession
const openai = {
  id: 'openai-1',
  model: 'gpt-5',
  status: 'running',
  created_at: new Date(20_000).toISOString(),
  environment: { type: 'none' },
} as OpenAISession

describe('SessionsList runtime controls', () => {
  it('keeps mixed-runtime controls available after the final OpenAI row disappears', () => {
    const { rerender } = render(
      <SessionsList
        sessions={[native]}
        openAISessions={[openai]}
        mixedRuntimes
      />,
    )

    fireEvent.click(screen.getByRole('button', { name: 'OpenAI' }))
    expect(screen.getByTestId('rows').textContent).toContain('openai-1')

    rerender(
      <SessionsList sessions={[native]} openAISessions={[]} mixedRuntimes />,
    )
    expect(screen.getByRole('button', { name: 'All runtimes' })).not.toBeNull()
    expect(screen.getByTestId('rows').textContent).toBe('')

    fireEvent.click(screen.getByRole('button', { name: 'All runtimes' }))
    expect(screen.getByTestId('rows').textContent).toContain('native-1')
  })

  it('keeps agent-scoped lists native-only by default', () => {
    render(<SessionsList sessions={[native]} />)
    expect(screen.queryByRole('button', { name: 'All runtimes' })).toBeNull()
  })
})
