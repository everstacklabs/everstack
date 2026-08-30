import { useMemo } from 'react'
import {
  ReactFlow,
  Background,
  BackgroundVariant,
  Controls,
  MiniMap,
  Handle,
  Position,
  type Node,
  type Edge,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { AlertTriangle } from 'lucide-react'
import { Iconify } from '@everstack/ui'
import type { Span } from '@everstack/proto/everstack/traces/v1/traces_pb'
import { getSpanDisplayConfig } from '@/utils/span-title-name-map'
import { categoryColors, categoryIcons } from '@/utils/span-display-helpers'
import { formatDuration } from '@/utils/trace-formatters'
import { getAttr } from '@/utils/traces-common'
import { analyzeTrajectory, type StepInput, type StepKind } from '@/utils/trajectory'

/**
 * Agent trajectory graph (D4): renders a trace's spans as a node-link tree.
 * LLM calls, tool calls, and sub-agents as nodes, parent/child as edges, with
 * **loop detection** (a tool invoked repeatedly) highlighted. Incumbents stop at
 * a nested-span tree; a graph makes agent structure and loops legible at a glance.
 */

const X_GAP = 210
const Y_GAP = 96

type SpanNodeData = {
  title: string
  subtitle?: string
  category: string
  durationLabel: string
  isError: boolean
  looping: boolean
  selected: boolean
}

function SpanGraphNode({ data }: NodeProps) {
  const d = data as unknown as SpanNodeData
  const colors = categoryColors[d.category as keyof typeof categoryColors] ?? categoryColors.internal
  const iconName = categoryIcons[d.category as keyof typeof categoryIcons] ?? categoryIcons.internal
  return (
    <div
      className={[
        'rounded border px-2.5 py-1.5 w-[190px] text-left transition-shadow',
        colors.bg,
        d.isError ? 'border-rose-500/50' : d.looping ? 'border-amber-500/60' : colors.border,
        d.selected ? 'ring-2 ring-brand-secondary-500' : '',
      ].join(' ')}
    >
      <Handle type="target" position={Position.Top} className="!bg-white/30 !border-0 !w-1.5 !h-1.5" />
      <div className="flex items-center gap-1.5">
        <Iconify.Icon icon={iconName} className={`h-3 w-3 shrink-0 ${colors.text}`} />
        <span className={`text-[11px] font-medium truncate ${colors.text}`}>{d.title}</span>
        {d.looping && <AlertTriangle className="h-3 w-3 text-amber-400 shrink-0 ml-auto" />}
      </div>
      <div className="mt-0.5 flex items-center justify-between text-[9px] text-brand-main-50 light:text-black">
        <span className="truncate">{d.subtitle || ''}</span>
        <span className="shrink-0 ml-1">{d.durationLabel}</span>
      </div>
      <Handle type="source" position={Position.Bottom} className="!bg-white/30 !border-0 !w-1.5 !h-1.5" />
    </div>
  )
}

const nodeTypes = { span: SpanGraphNode }

/** Tidy top-down tree layout: x by leaf order, y by depth. Deterministic. */
function layout(spans: Span[]): { nodes: Node[]; edges: Edge[]; loopCount: number } {
  const byId = new Map(spans.map((s) => [s.spanId, s]))
  const children = new Map<string, Span[]>()
  const roots: Span[] = []
  for (const s of spans) {
    if (s.parentSpanId && byId.has(s.parentSpanId)) {
      ;(children.get(s.parentSpanId) ?? children.set(s.parentSpanId, []).get(s.parentSpanId)!).push(s)
    } else {
      roots.push(s)
    }
  }
  const sortByTime = (a: Span, b: Span) =>
    Number((a.timestamp?.seconds ?? 0n) as bigint) - Number((b.timestamp?.seconds ?? 0n) as bigint)
  roots.sort(sortByTime)
  for (const arr of children.values()) arr.sort(sortByTime)

  // Loop detection: tool/function spans whose display title repeats.
  const titleCounts = new Map<string, number>()
  for (const s of spans) {
    const cfg = getSpanDisplayConfig(s)
    if (cfg.category === 'function' || cfg.category === 'tool_loop') {
      titleCounts.set(cfg.title, (titleCounts.get(cfg.title) ?? 0) + 1)
    }
  }
  let loopCount = 0

  const pos = new Map<string, { x: number; y: number }>()
  let leaf = 0
  const assign = (s: Span, depth: number): number => {
    const kids = children.get(s.spanId) ?? []
    let x: number
    if (kids.length === 0) {
      x = leaf++ * X_GAP
    } else {
      const xs = kids.map((k) => assign(k, depth + 1))
      x = (xs[0] + xs[xs.length - 1]) / 2
    }
    pos.set(s.spanId, { x, y: depth * Y_GAP })
    return x
  }
  for (const r of roots) assign(r, 0)

  const nodes: Node[] = spans.map((s) => {
    const cfg = getSpanDisplayConfig(s)
    const looping = (cfg.category === 'function' || cfg.category === 'tool_loop') && (titleCounts.get(cfg.title) ?? 0) > 2
    if (looping) loopCount++
    const data: SpanNodeData = {
      title: cfg.title,
      subtitle: cfg.subtitle,
      category: cfg.category,
      durationLabel: formatDuration(s.duration),
      isError: (s.statusCode || '').toUpperCase().includes('ERROR'),
      looping,
      selected: false,
    }
    return {
      id: s.spanId,
      type: 'span',
      position: pos.get(s.spanId) ?? { x: 0, y: 0 },
      data: data as unknown as Record<string, unknown>,
      draggable: false,
    }
  })

  const edges: Edge[] = spans
    .filter((s) => s.parentSpanId && byId.has(s.parentSpanId))
    .map((s) => ({
      id: `${s.parentSpanId}->${s.spanId}`,
      source: s.parentSpanId,
      target: s.spanId,
      style: { stroke: 'rgba(255,255,255,0.18)' },
    }))

  return { nodes, edges, loopCount }
}

/** Map a span's display category to a trajectory step kind. */
function stepKind(category: string, obsType: string): StepKind {
  if (obsType === 'GENERATION' || category === 'provider') return 'generation'
  if (obsType === 'TOOL' || category === 'function' || category === 'tool_loop') return 'tool'
  return 'other'
}

/** Adapt spans into ordered trajectory steps for intrinsic-quality signals. */
function spansToSteps(spans: Span[]): StepInput[] {
  return spans.map((s) => {
    const cfg = getSpanDisplayConfig(s)
    const obsType = (getAttr(s, 'observation.type') || '').toUpperCase()
    const kind = stepKind(cfg.category, obsType)
    const name =
      getAttr(s, 'tool.name') ||
      getAttr(s, 'gen_ai.tool.name') ||
      getAttr(s, 'function.name') ||
      cfg.subtitle ||
      cfg.title
    const seconds = Number((s.timestamp?.seconds ?? 0n) as bigint)
    const nanos = Number((s.timestamp?.nanos ?? 0) as number)
    return {
      startNs: seconds * 1_000_000_000 + nanos,
      kind,
      name,
      args: getAttr(s, 'tool.arguments') || getAttr(s, 'function.arguments') || undefined,
      isError: (s.statusCode || '').toUpperCase().includes('ERROR'),
    }
  })
}

function TrajectorySignals({ spans }: { spans: Span[] }) {
  const signals = useMemo(() => analyzeTrajectory(spansToSteps(spans)).signals, [spans])
  if (signals.stepCount === 0) return null

  const stats: Array<{ label: string; value: string; warn?: boolean }> = [
    { label: 'steps', value: String(signals.stepCount) },
    { label: 'tool calls', value: String(signals.toolCallCount) },
    {
      label: 'tool diversity',
      value: `${Math.round(signals.toolDiversity * 100)}%`,
      warn: signals.toolCallCount > 2 && signals.toolDiversity < 0.5,
    },
    { label: 'redundant', value: String(signals.redundantSteps), warn: signals.redundantSteps > 0 },
    { label: 'errors', value: String(signals.errorSteps), warn: signals.errorSteps > 0 },
  ]

  return (
    <div className="absolute top-2 right-2 z-10 flex items-center gap-3 rounded border border-brand-main-600 bg-brand-main-900/85 px-2.5 py-1 backdrop-blur-sm">
      {stats.map((s) => (
        <div key={s.label} className="flex items-baseline gap-1">
          <span className={`text-[12px] ${s.warn ? 'text-amber-300' : 'text-brand-main-50 light:text-brand-main-50'}`}>
            {s.value}
          </span>
          <span className="text-[10px] uppercase tracking-wide text-brand-main-50 light:text-black">{s.label}</span>
        </div>
      ))}
    </div>
  )
}

export function AgentGraph({
  spans,
  selectedSpanId,
  onSelectSpan,
}: {
  spans: Span[]
  selectedSpanId?: string
  onSelectSpan?: (spanId: string) => void
}) {
  const { nodes, edges, loopCount } = useMemo(() => layout(spans), [spans])
  const styledNodes = useMemo(
    () =>
      nodes.map((n) =>
        n.id === selectedSpanId ? { ...n, data: { ...n.data, selected: true } } : n,
      ),
    [nodes, selectedSpanId],
  )

  if (spans.length === 0) {
    return <div className="flex-1 flex items-center justify-center text-brand-main-50 text-xs light:text-black">No spans to graph</div>
  }

  return (
    <div className="relative h-full w-full">
      {loopCount > 0 && (
        <div className="absolute top-2 left-2 z-10 flex items-center gap-1.5 rounded border border-amber-500/30 bg-amber-500/10 px-2.5 py-1 text-[11px] text-amber-300">
          <AlertTriangle className="h-3.5 w-3.5" />
          {loopCount} repeated tool call{loopCount === 1 ? '' : 's'}, possible loop
        </div>
      )}
      <TrajectorySignals spans={spans} />
      <ReactFlow
        nodes={styledNodes}
        edges={edges}
        nodeTypes={nodeTypes}
        fitView
        fitViewOptions={{ padding: 0.2, maxZoom: 1 }}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
        onNodeClick={(_, node) => onSelectSpan?.(node.id)}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} color="rgba(255,255,255,0.06)" />
        <Controls showInteractive={false} className="!bg-brand-main-800 !border-brand-main-600" />
        <MiniMap pannable zoomable className="!bg-brand-main-900" maskColor="rgba(0,0,0,0.6)" />
      </ReactFlow>
    </div>
  )
}
