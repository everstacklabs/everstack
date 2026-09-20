import { describe, expect, it } from 'vitest'
import type { AgentSession } from '@/server/agents'
import type { OpenAISession } from '@/server/openai-agents'
import {
  buildSessionRows,
  filterSessionRows,
  sessionDestination,
} from './sessions-list'

const native = {
  id: 'native-1',
  createdAt: { seconds: 10n },
} as unknown as AgentSession
const openai = {
  id: 'openai-1',
  created_at: new Date(20_000).toISOString(),
  environment: { type: 'none' },
} as OpenAISession

describe('mixed agent sessions', () => {
  it('sorts both runtimes and routes each row to its own detail view', () => {
    const rows = buildSessionRows([native], [openai])
    expect(rows.map((row) => row.runtime)).toEqual(['openai', 'everstack'])
    expect(sessionDestination(rows[0]!)).toEqual({
      to: '/deployments/agents/openai-sessions/$sessionId',
      params: { sessionId: 'openai-1' },
    })
    expect(sessionDestination(rows[1]!)).toEqual({
      to: '/deployments/agents/sessions/$sessionId',
      params: { sessionId: 'native-1' },
    })
    expect(filterSessionRows(rows, 'openai').map((row) => row.id)).toEqual([
      'openai-1',
    ])
  })

  it('keeps native rows usable when OpenAI data is unavailable or omitted', () => {
    const rows = buildSessionRows([native], [])
    expect(rows).toHaveLength(1)
    expect(rows[0]?.runtime).toBe('everstack')
  })
})
