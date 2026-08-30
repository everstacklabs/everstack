import {
  createContext,
  useState,
  useMemo,
  useCallback,
  useContext,
} from 'react'
import type { CSSProperties, ReactNode } from 'react'
import {
  ReactFlow,
  ReactFlowProvider,
  Background,
  BackgroundVariant,
  MiniMap,
  BaseEdge,
  Controls,
  Panel,
  getSmoothStepPath,
  MarkerType,
} from '@xyflow/react'
import type { EdgeProps } from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { X } from 'lucide-react'
import { ui } from '@everstack/ui'
import type { AgentMemoryEntry } from '@/server/agents'
import {
  computeLayout,
  buildAdjacency,
  TYPE_STROKE,
  TYPE_BG,
  SCOPE_RING,
} from './memory-graph-layout'
import type { MemoryGraphNode, MemoryGraphEdge } from './memory-graph-layout'
import MemoryGraphNodeComponent from './memory-graph-node'

const { Badge } = ui

/* ── Context ── */

export interface MemoryGraphContextValue {
  hoveredId: string | null
  selectedId: string | null
  connectedIds: Set<string> | null
  onHover: (id: string | null) => void
  onSelect: (id: string | null) => void
}

export const MemoryGraphContext = createContext<MemoryGraphContextValue | null>(
  null,
)

/* ── Edge component ── */

const EDGE_STYLES: Record<string, CSSProperties> = {
  superseded: {
    stroke: '#fbbf24',
    strokeWidth: 2,
    strokeDasharray: '6 4',
  },
  same_fact_key: {
    stroke: '#737373',
    strokeWidth: 2,
  },
  same_session: {
    stroke: '#525252',
    strokeWidth: 1.5,
    strokeDasharray: '2 4',
  },
}

// Light-mode edge palette: amber darkened for contrast on light surfaces;
// neutral strokes lightened so they read as subtle links rather than heavy lines.
const EDGE_STYLES_LIGHT: Record<string, CSSProperties> = {
  superseded: {
    stroke: '#d97706',
    strokeWidth: 2,
    strokeDasharray: '6 4',
  },
  same_fact_key: {
    stroke: '#a3a3a3',
    strokeWidth: 2,
  },
  same_session: {
    stroke: '#c4c4c4',
    strokeWidth: 1.5,
    strokeDasharray: '2 4',
  },
}

function MemoryEdgeComponent({
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
  markerEnd,
  style,
  source,
  target,
}: EdgeProps<MemoryGraphEdge>) {
  const ctx = useContext(MemoryGraphContext)
  const chartMode = ui.useChartMode()
  const edgeStyles = chartMode === 'light' ? EDGE_STYLES_LIGHT : EDGE_STYLES
  const kind = data?.edgeKind ?? 'same_session'
  const [path] = getSmoothStepPath({
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    borderRadius: 18,
  })
  const hovered = ctx?.hoveredId
  const dimmed = hovered ? hovered !== source && hovered !== target : false

  return (
    <BaseEdge
      path={path}
      markerEnd={kind === 'superseded' ? markerEnd : undefined}
      style={
        {
          ...(style ?? {}),
          ...edgeStyles[kind],
          opacity: dimmed ? 0.18 : 0.9,
          transition: 'opacity 160ms ease',
        } as any
      }
    />
  )
}

/* ── Node types / edge types (stable references) ── */

const nodeTypes = { memoryNode: MemoryGraphNodeComponent }
const edgeTypes = { memoryEdge: MemoryEdgeComponent }

/* ── Inner component (needs ReactFlowProvider above) ── */

function MemoryGraphInner({ memories }: { memories: AgentMemoryEntry[] }) {
  const chartMode = ui.useChartMode()
  const isLight = chartMode === 'light'
  const [hoveredId, setHoveredId] = useState<string | null>(null)
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const { nodes, edges } = useMemo(() => computeLayout(memories), [memories])

  const adjacency = useMemo(() => buildAdjacency(edges), [edges])

  const connectedIds = useMemo(() => {
    if (!hoveredId) return null
    const set = new Set<string>([hoveredId])
    const neighbors = adjacency.get(hoveredId)
    if (neighbors) for (const n of neighbors) set.add(n)
    return set
  }, [hoveredId, adjacency])

  const onHover = useCallback((id: string | null) => setHoveredId(id), [])
  const onSelect = useCallback((id: string | null) => {
    setSelectedId((prev) => (prev === id ? null : id))
  }, [])

  const onPaneClick = useCallback(() => setSelectedId(null), [])

  const ctxValue = useMemo<MemoryGraphContextValue>(
    () => ({
      hoveredId,
      selectedId,
      connectedIds,
      onHover,
      onSelect,
    }),
    [hoveredId, selectedId, connectedIds, onHover, onSelect],
  )

  const selectedMemory = useMemo(() => {
    if (!selectedId) return null
    return memories.find((m) => m.id === selectedId) ?? null
  }, [selectedId, memories])

  const stats = useMemo(() => getGraphStats(memories, edges), [memories, edges])

  const miniMapNodeColor = useCallback((node: MemoryGraphNode) => {
    return TYPE_STROKE[node.data.memory.memoryType] ?? '#525252'
  }, [])

  return (
    <MemoryGraphContext.Provider value={ctxValue}>
      <div
        className="relative overflow-hidden rounded-lg border border-brand-main-700/50 bg-brand-main-950"
        style={{ height: 560 }}
      >
        <ReactFlow<MemoryGraphNode, MemoryGraphEdge>
          nodes={nodes}
          edges={edges}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          nodesDraggable={false}
          nodesConnectable={false}
          edgesFocusable={false}
          fitView
          fitViewOptions={{
            padding: 0.2,
            includeHiddenNodes: false,
            maxZoom: 1,
          }}
          onPaneClick={onPaneClick}
          defaultEdgeOptions={{
            type: 'memoryEdge',
            style: { stroke: '#525252', strokeWidth: 1 },
            markerEnd: {
              type: MarkerType.ArrowClosed,
              width: 8,
              height: 8,
              color: '#525252',
            },
          }}
          proOptions={{ hideAttribution: true }}
          className="studio-flow bg-brand-main-950"
        >
          <Background
            variant={BackgroundVariant.Dots}
            gap={20}
            size={1}
            color={isLight ? '#d4d4d4' : '#323232'}
          />
          <Panel position="top-left" className="m-4">
            <div className="rounded-lg border border-brand-main-600 bg-brand-main-800/95 px-3 py-2 shadow-lg">
              <div className="mb-2 flex items-center justify-between gap-8 border-b border-brand-main-700 pb-2">
                <div className="text-xs font-medium text-white light:text-brand-main-50">
                  Memory graph
                </div>
                <div className="text-[10px] tabular-nums text-brand-main-400">
                  {stats.active}/{stats.total} active
                </div>
              </div>
              <div className="grid grid-cols-2 gap-x-3 gap-y-1 text-[10px] text-brand-main-400">
                {Object.entries(TYPE_STROKE).map(([type, color]) => (
                  <span key={type} className="flex items-center gap-1.5">
                    <span
                      className="size-2 rounded-full"
                      style={{ backgroundColor: color }}
                    />
                    {MEMORY_TYPE_LABELS[type] ?? type}
                  </span>
                ))}
              </div>
              <div className="mt-2 border-t border-brand-main-700 pt-2 text-[10px] text-brand-main-400">
                {stats.edges} links · {stats.superseded} inactive/superseded
              </div>
            </div>
          </Panel>
          <Controls
            showInteractive={false}
            className="!bottom-4 !left-4 !top-auto overflow-hidden !rounded-lg !border !border-brand-main-600 !bg-brand-main-800/90 !shadow-lg"
          />
          <MiniMap
            nodeColor={miniMapNodeColor}
            maskColor={isLight ? 'rgba(255, 255, 255, 0.6)' : 'rgba(0, 0, 0, 0.6)'}
            style={{
              background: isLight ? '#f4f4f5' : '#18181b',
              border: isLight ? '1px solid #d4d4d8' : '1px solid #525252',
            }}
            pannable
            zoomable
          />
        </ReactFlow>

        {/* Detail panel */}
        {selectedMemory && (
          <DetailPanel
            memory={selectedMemory}
            onClose={() => setSelectedId(null)}
          />
        )}
      </div>
    </MemoryGraphContext.Provider>
  )
}

/* ── Detail panel ── */

const MEMORY_TYPE_LABELS: Record<string, string> = {
  fact: 'Fact',
  instruction: 'Instruction',
  session_summary: 'Summary',
  document: 'Document',
}

const SCOPE_LABELS: Record<string, string> = {
  agent: 'Agent',
  user: 'User',
  global: 'Global',
}

function DetailPanel({
  memory,
  onClose,
}: {
  memory: AgentMemoryEntry
  onClose: () => void
}) {
  const typeColor = TYPE_STROKE[memory.memoryType] ?? '#525252'
  const typeBg = TYPE_BG[memory.memoryType] ?? 'rgba(82,82,82,0.12)'
  const scopeColor = SCOPE_RING[memory.scope] ?? '#6b7280'
  const confidencePercent = Math.round((memory.confidence ?? 1) * 100)

  return (
    <div
      className="absolute bottom-4 right-4 top-4 z-20 flex w-[320px] max-w-[calc(100%-2rem)] flex-col overflow-hidden rounded-lg border border-brand-main-600 bg-brand-main-800 shadow-lg pointer-events-auto"
      onClick={(e) => e.stopPropagation()}
    >
      {/* Header */}
      <div className="flex items-start justify-between gap-3 border-b border-brand-main-700/50 px-4 py-3">
        <div className="min-w-0 space-y-2">
          <div className="flex items-center gap-1.5 flex-wrap">
            <Badge
              variant="outline"
              className="text-[10px] px-1.5 py-0"
              style={{
                backgroundColor: typeBg,
                color: typeColor,
                borderColor: `${typeColor}44`,
              }}
            >
              {MEMORY_TYPE_LABELS[memory.memoryType] ?? memory.memoryType}
            </Badge>
            <Badge
              variant="outline"
              className="text-[10px] px-1.5 py-0"
              style={{ color: scopeColor, borderColor: `${scopeColor}44` }}
            >
              {SCOPE_LABELS[memory.scope] ?? memory.scope}
            </Badge>
            {!memory.isActive && (
              <span className="text-[10px] text-zinc-600 italic">inactive</span>
            )}
            {memory.supersededBy && (
              <span className="text-[10px] text-amber-500/70 light:text-amber-700/70 italic">
                superseded
              </span>
            )}
          </div>
          <div className="truncate text-[11px] text-zinc-500">
            {memory.factKey || memory.sourceSessionId || memory.id}
          </div>
        </div>
        <button
          onClick={onClose}
          className="rounded-md p-1 text-zinc-500 transition-colors hover:bg-brand-main-800 hover:text-zinc-200 light:hover:text-zinc-800"
          title="Close details"
        >
          <X className="w-4 h-4" />
        </button>
      </div>

      {/* Content */}
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        <p className="text-sm text-zinc-200 light:text-zinc-800 whitespace-pre-wrap break-words leading-relaxed">
          {memory.content}
        </p>
      </div>

      {/* Metadata */}
      <div className="space-y-2 border-t border-brand-main-700/50 px-4 py-3">
        {memory.factKey && <MetaRow label="Key" value={memory.factKey} mono />}
        <MetaRow
          label="Confidence"
          value={
            <div className="flex items-center gap-1.5">
              <div className="w-16 h-1 rounded-full bg-brand-main-700/50 overflow-hidden">
                <div
                  className={`h-full rounded-full ${
                    confidencePercent >= 80
                      ? 'bg-emerald-500/70'
                      : confidencePercent >= 50
                        ? 'bg-amber-500/70'
                        : 'bg-red-500/70'
                  }`}
                  style={{ width: `${confidencePercent}%` }}
                />
              </div>
              <span className="tabular-nums">{confidencePercent}%</span>
            </div>
          }
        />
        <MetaRow
          label="Source"
          value={
            memory.source === 'auto_extracted'
              ? 'auto-extracted'
              : memory.source
          }
        />
        {(memory.accessCount ?? 0) > 0 && (
          <MetaRow label="Accessed" value={`${memory.accessCount}x`} />
        )}
        {memory.sourceSessionId && (
          <MetaRow
            label="Session"
            value={memory.sourceSessionId.slice(0, 8) + '...'}
            mono
          />
        )}
        <MetaRow label="ID" value={memory.id.slice(0, 12) + '...'} mono />
      </div>
    </div>
  )
}

function MetaRow({
  label,
  value,
  mono,
}: {
  label: string
  value: ReactNode
  mono?: boolean
}) {
  return (
    <div className="flex items-center justify-between gap-3 text-[11px]">
      <span className="text-zinc-500">{label}</span>
      <span
        className={`min-w-0 truncate text-zinc-300 light:text-zinc-700 ${mono ? 'font-mono' : ''}`}
      >
        {value}
      </span>
    </div>
  )
}

function getGraphStats(memories: AgentMemoryEntry[], edges: MemoryGraphEdge[]) {
  return {
    total: memories.length,
    active: memories.filter((m) => m.isActive).length,
    superseded: memories.filter((m) => !!m.supersededBy || !m.isActive).length,
    edges: edges.length,
  }
}

/* ── Outer wrapper ── */

interface MemoryGraphProps {
  memories: AgentMemoryEntry[]
}

export function MemoryGraph({ memories }: MemoryGraphProps) {
  return (
    <ReactFlowProvider>
      <MemoryGraphInner memories={memories} />
    </ReactFlowProvider>
  )
}
