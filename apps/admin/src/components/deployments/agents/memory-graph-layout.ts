import type { Node, Edge } from '@xyflow/react'
import type { AgentMemoryEntry } from '@/server/agents'

/* ── Types ── */

export interface MemoryNodeData {
  memory: AgentMemoryEntry
  nodeWidth: number
  nodeHeight: number
  typeColor: string
  typeBg: string
  laneLabel: string
  [key: string]: unknown
}

export type MemoryGraphNode = Node<MemoryNodeData>
export type MemoryGraphEdge = Edge<{
  edgeKind: 'superseded' | 'same_fact_key' | 'same_session'
}>

/* ── Color constants ── */

export const TYPE_STROKE: Record<string, string> = {
  fact: '#6366f1',
  instruction: '#9333ea',
  session_summary: '#059669',
  document: '#0891b2',
}

export const TYPE_BG: Record<string, string> = {
  fact: 'rgba(99,102,241,0.14)',
  instruction: 'rgba(147,51,234,0.14)',
  session_summary: 'rgba(5,150,105,0.14)',
  document: 'rgba(8,145,178,0.14)',
}

export const SCOPE_RING: Record<string, string> = {
  agent: '#525252',
  user: '#525252',
  global: '#525252',
}

const DEFAULT_STROKE = '#525252'

/* ── Layout algorithm ── */

const NODE_WIDTH = 220
const NODE_HEIGHT = 112
const COLUMN_GAP = 72
const ROW_GAP = 36
const LANE_GAP = 88
const TYPE_ORDER = ['fact', 'instruction', 'session_summary', 'document']
const SCOPE_ORDER = ['agent', 'user', 'global']
const TYPE_LABELS: Record<string, string> = {
  fact: 'Facts',
  instruction: 'Instructions',
  session_summary: 'Summaries',
  document: 'Documents',
}
const SCOPE_LABELS: Record<string, string> = {
  agent: 'Agent scope',
  user: 'User scope',
  global: 'Global scope',
}

function toMillis(value: AgentMemoryEntry['createdAt']): number {
  if (!value) return 0
  const seconds = Number(value.seconds ?? 0)
  const nanos = Number(value.nanos ?? 0)
  return seconds * 1000 + Math.floor(nanos / 1_000_000)
}

function compareMemories(a: AgentMemoryEntry, b: AgentMemoryEntry): number {
  const typeDelta =
    TYPE_ORDER.indexOf(a.memoryType) - TYPE_ORDER.indexOf(b.memoryType)
  if (typeDelta !== 0) return typeDelta

  const timeDelta =
    toMillis(b.updatedAt ?? b.createdAt) - toMillis(a.updatedAt ?? a.createdAt)
  if (timeDelta !== 0) return timeDelta

  return (b.accessCount ?? 0) - (a.accessCount ?? 0)
}

export function computeLayout(memories: AgentMemoryEntry[]): {
  nodes: MemoryGraphNode[]
  edges: MemoryGraphEdge[]
} {
  if (memories.length === 0) return { nodes: [], edges: [] }

  const nodes: MemoryGraphNode[] = []

  const scopes = [
    ...SCOPE_ORDER.filter((scope) => memories.some((m) => m.scope === scope)),
    ...Array.from(new Set(memories.map((m) => m.scope))).filter(
      (scope) => !SCOPE_ORDER.includes(scope),
    ),
  ]

  let laneY = 0
  for (const scope of scopes) {
    const laneMemories = memories
      .filter((m) => m.scope === scope)
      .sort(compareMemories)
    if (laneMemories.length === 0) continue

    let laneHeight = NODE_HEIGHT
    for (const type of TYPE_ORDER.concat(
      Array.from(new Set(laneMemories.map((m) => m.memoryType))).filter(
        (type) => !TYPE_ORDER.includes(type),
      ),
    )) {
      const typed = laneMemories.filter((m) => m.memoryType === type)
      if (typed.length === 0) continue

      const typeIndex = TYPE_ORDER.includes(type)
        ? TYPE_ORDER.indexOf(type)
        : TYPE_ORDER.length
      const columnX = typeIndex * (NODE_WIDTH + COLUMN_GAP)
      typed.forEach((m, index) => {
        const y = laneY + index * (NODE_HEIGHT + ROW_GAP)
        laneHeight = Math.max(
          laneHeight,
          (index + 1) * NODE_HEIGHT + index * ROW_GAP,
        )

        nodes.push({
          id: m.id,
          type: 'memoryNode',
          position: { x: columnX, y },
          data: {
            memory: m,
            nodeWidth: NODE_WIDTH,
            nodeHeight: NODE_HEIGHT,
            typeColor: TYPE_STROKE[m.memoryType] ?? DEFAULT_STROKE,
            typeBg: TYPE_BG[m.memoryType] ?? 'rgba(82,82,82,0.12)',
            laneLabel: `${SCOPE_LABELS[scope] ?? scope} / ${TYPE_LABELS[type] ?? type}`,
          },
        })
      })
    }

    laneY += laneHeight + LANE_GAP
  }

  /* ── 2. Compute edges ── */
  const edges: MemoryGraphEdge[] = []
  const memById = new Map(memories.map((m) => [m.id, m]))

  // Pass 1: supersededBy → directed dashed amber
  for (const m of memories) {
    if (m.supersededBy && memById.has(m.supersededBy)) {
      edges.push({
        id: `sup-${m.id}`,
        source: m.id,
        target: m.supersededBy,
        type: 'memoryEdge',
        data: { edgeKind: 'superseded' },
      })
    }
  }

  // Pass 2: same factKey → chain (not mesh)
  const byFactKey = new Map<string, AgentMemoryEntry[]>()
  for (const m of memories) {
    if (!m.factKey) continue
    const arr = byFactKey.get(m.factKey)
    if (arr) arr.push(m)
    else byFactKey.set(m.factKey, [m])
  }
  for (const [, group] of byFactKey) {
    if (group.length < 2) continue
    for (let i = 0; i < group.length - 1; i++) {
      edges.push({
        id: `fk-${group[i].id}-${group[i + 1].id}`,
        source: group[i].id,
        target: group[i + 1].id,
        type: 'memoryEdge',
        data: { edgeKind: 'same_fact_key' },
      })
    }
  }

  // Pass 3: same sourceSessionId → chain, only groups ≤ 4
  const bySession = new Map<string, AgentMemoryEntry[]>()
  for (const m of memories) {
    if (!m.sourceSessionId) continue
    const arr = bySession.get(m.sourceSessionId)
    if (arr) arr.push(m)
    else bySession.set(m.sourceSessionId, [m])
  }
  for (const [, group] of bySession) {
    if (group.length < 2 || group.length > 4) continue
    for (let i = 0; i < group.length - 1; i++) {
      edges.push({
        id: `ss-${group[i].id}-${group[i + 1].id}`,
        source: group[i].id,
        target: group[i + 1].id,
        type: 'memoryEdge',
        data: { edgeKind: 'same_session' },
      })
    }
  }

  return { nodes, edges }
}

/** Build adjacency map: nodeId → set of connected nodeIds */
export function buildAdjacency(
  edges: MemoryGraphEdge[],
): Map<string, Set<string>> {
  const adj = new Map<string, Set<string>>()
  for (const e of edges) {
    if (!adj.has(e.source)) adj.set(e.source, new Set())
    if (!adj.has(e.target)) adj.set(e.target, new Set())
    adj.get(e.source)!.add(e.target)
    adj.get(e.target)!.add(e.source)
  }
  return adj
}
