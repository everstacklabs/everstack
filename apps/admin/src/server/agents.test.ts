import { beforeEach, describe, expect, it, vi } from 'vitest'

const rpc = vi.hoisted(() => ({
  getActiveAgentRevision: vi.fn(),
}))

vi.mock('@/server', () => ({
  createServerTransport: vi.fn(() => ({})),
}))

vi.mock('@/lib/api-url', () => ({
  getApiBaseUrl: vi.fn(() => ''),
}))

vi.mock('@everstack/client', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@everstack/client')>()
  return {
    ...actual,
    createClientFor: vi.fn(() => () => rpc),
    create: vi.fn((_schema, value) => value),
  }
})

import { Code, ConnectError } from '@everstack/client'
import { getActiveAgentRevision } from './agents'

describe('getActiveAgentRevision', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('returns null when a legacy agent has no active revision', async () => {
    rpc.getActiveAgentRevision.mockRejectedValue(
      new ConnectError('revision not found', Code.NotFound),
    )

    await expect(
      getActiveAgentRevision('agent-1', 'tenant-1'),
    ).resolves.toBeNull()
  })

  it('normalizes an empty successful response to null', async () => {
    rpc.getActiveAgentRevision.mockResolvedValue({})

    await expect(
      getActiveAgentRevision('agent-1', 'tenant-1'),
    ).resolves.toBeNull()
  })

  it('returns the active revision when one exists', async () => {
    const revision = { id: 'revision-1', agentId: 'agent-1' }
    rpc.getActiveAgentRevision.mockResolvedValue({ revision })

    await expect(getActiveAgentRevision('agent-1', 'tenant-1')).resolves.toBe(
      revision,
    )
  })
})
