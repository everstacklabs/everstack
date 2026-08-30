import {
  Fragment,
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
} from 'react'
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  applyNodeChanges,
  type Connection,
  type Edge,
  type Node,
  type NodeChange,
  type NodeProps,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import {
  CircleCheck,
  GitBranch,
  ListTree,
  MoveRight,
  Crosshair,
  Trash2,
  Wand2,
  LayoutGrid,
  Plus,
} from 'lucide-react'
import { Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { MustacheTextarea } from './mustache-textarea'
import {
  EvaluationField,
  evaluationInputClass,
  evaluationSelectContentClass,
  evaluationSelectTriggerClass,
} from './evaluation-form'

const { Input, Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

// ─── Draft model ─────────────────────────────────────────────────────────────
// Mirrors internal/services/eval_runner/dag_scorer.go: a dag_definition is
// { root, nodes: { id: { id, type, prompt?, edges: [{label, target}], score?,
// sub_scorer_prompt? } } }. Only a verdict leaf assigns the score — routing is
// what makes the metric reproducible.

export type DagNodeType = 'task' | 'binary_judgement' | 'non_binary_judgement' | 'verdict'

export type DagEdgeDraft = { label: string; target: string }

export type DagNodeDraft = {
  id: string
  type: DagNodeType
  prompt: string
  edges: DagEdgeDraft[]
  /** verdict leaves: fixed score (string for input friendliness) … */
  verdictMode: 'fixed' | 'sub'
  score: string
  /** … or a nested numeric sub-scorer prompt whose result becomes the score. */
  subScorerPrompt: string
  /** Canvas position — persisted alongside the definition; ignored by the runner. */
  position?: { x: number; y: number }
}

export type DagDraft = { root: string; nodes: DagNodeDraft[] }

const DAG_NODE_TYPES: DagNodeType[] = [
  'task',
  'binary_judgement',
  'non_binary_judgement',
  'verdict',
]

export const DAG_NODE_TYPE_OPTIONS: { value: DagNodeType; label: string }[] = [
  { value: 'task', label: 'Task' },
  { value: 'binary_judgement', label: 'Binary judgement' },
  { value: 'non_binary_judgement', label: 'Non-binary judgement' },
  { value: 'verdict', label: 'Verdict' },
]

export function starterDagDraft(): DagDraft {
  return {
    root: 'consistent',
    nodes: [
      {
        id: 'consistent',
        type: 'binary_judgement',
        prompt:
          'Is the output factually consistent with the expected output?\n\nOutput: {{output}}\nExpected: {{expected_output}}',
        edges: [
          { label: 'yes', target: 'pass' },
          { label: 'no', target: 'fail' },
        ],
        verdictMode: 'fixed',
        score: '',
        subScorerPrompt: '',
        position: { x: 160, y: 0 },
      },
      {
        id: 'pass',
        type: 'verdict',
        prompt: '',
        edges: [],
        verdictMode: 'fixed',
        score: '1',
        subScorerPrompt: '',
        position: { x: 0, y: 190 },
      },
      {
        id: 'fail',
        type: 'verdict',
        prompt: '',
        edges: [],
        verdictMode: 'fixed',
        score: '0',
        subScorerPrompt: '',
        position: { x: 330, y: 190 },
      },
    ],
  }
}

// ─── Serialize / parse ───────────────────────────────────────────────────────

export function serializeDagDraft(draft: DagDraft): string {
  const nodes: Record<string, unknown> = {}
  for (const n of draft.nodes) {
    const out: Record<string, unknown> = { id: n.id, type: n.type }
    if (n.type === 'verdict') {
      if (n.verdictMode === 'sub') out.sub_scorer_prompt = n.subScorerPrompt
      else out.score = Number(n.score)
    } else {
      out.prompt = n.prompt
      out.edges = n.edges.map((e) => ({ label: e.label, target: e.target }))
    }
    if (n.position) {
      out.position = { x: Math.round(n.position.x), y: Math.round(n.position.y) }
    }
    nodes[n.id] = out
  }
  return JSON.stringify({ root: draft.root, nodes })
}

export function encodeDagDefinition(draft: DagDraft): Uint8Array {
  return new TextEncoder().encode(serializeDagDraft(draft))
}

/**
 * Sentinel that clears a persisted dag_definition when a scorer is switched
 * away from DAG mode: the runner's hasDagDefinition treats "null" as absent
 * (empty bytes would be dropped by the update command's nil check).
 */
export function clearedDagDefinition(): Uint8Array {
  return new TextEncoder().encode('null')
}

type RawDagEdge = { label?: string; target?: string; node_id?: string }
type RawDagNode = {
  id?: string
  type?: string
  prompt?: string
  edges?: RawDagEdge[]
  children?: RawDagEdge[]
  score?: number | string | null
  sub_scorer_prompt?: string
  position?: { x?: number; y?: number }
}

/** Tolerant parse of a persisted dag_definition (accepts edges|children, target|node_id). */
export function decodeDagDefinition(bytes: Uint8Array | undefined | null): DagDraft | null {
  if (!bytes || bytes.length === 0) return null
  let text: string
  try {
    text = new TextDecoder().decode(bytes).trim()
  } catch {
    return null
  }
  if (!text || text === 'null') return null
  let raw: { root?: string; nodes?: Record<string, RawDagNode> }
  try {
    raw = JSON.parse(text) as { root?: string; nodes?: Record<string, RawDagNode> }
  } catch {
    return null
  }
  if (!raw || typeof raw !== 'object' || !raw.nodes || typeof raw.nodes !== 'object') {
    return null
  }
  const nodes: DagNodeDraft[] = Object.entries(raw.nodes).map(([key, n]) => {
    const type = DAG_NODE_TYPES.includes((n.type ?? '') as DagNodeType)
      ? (n.type as DagNodeType)
      : 'task'
    const rawEdges = [...(n.edges ?? []), ...(n.children ?? [])]
    const hasScore = n.score !== undefined && n.score !== null
    return {
      id: (n.id ?? '').trim() || key,
      type,
      prompt: n.prompt ?? '',
      edges: rawEdges.map((e) => ({
        label: e.label ?? '',
        target: e.target?.trim() ? e.target : (e.node_id ?? ''),
      })),
      verdictMode: !hasScore && (n.sub_scorer_prompt ?? '').trim() !== '' ? 'sub' : 'fixed',
      score: hasScore ? String(n.score) : '',
      subScorerPrompt: n.sub_scorer_prompt ?? '',
      position:
        n.position && typeof n.position.x === 'number' && typeof n.position.y === 'number'
          ? { x: n.position.x, y: n.position.y }
          : undefined,
    }
  })
  if (nodes.length === 0) return null
  return { root: raw.root ?? '', nodes }
}

// ─── Validation (mirrors validateDagDefinition in dag_scorer.go) ─────────────

export function validateDagDraft(draft: DagDraft): string[] {
  const errs: string[] = []
  const ids = new Set<string>()
  for (const n of draft.nodes) {
    if (!n.id.trim()) errs.push('Every node needs an id.')
    else if (ids.has(n.id)) errs.push(`Duplicate node id "${n.id}".`)
    ids.add(n.id)
  }
  if (draft.nodes.length === 0) errs.push('Add at least one node.')
  if (!draft.root.trim()) errs.push('Set a root node (exactly one entry point).')
  else if (draft.nodes.length > 0 && !ids.has(draft.root)) {
    errs.push(`Root "${draft.root}" does not exist.`)
  }

  const byId = new Map(draft.nodes.map((n) => [n.id, n]))
  for (const n of draft.nodes) {
    if (n.type === 'verdict') {
      if (n.edges.length > 0) {
        errs.push(`Verdict "${n.id}" must be a leaf — remove its outgoing edges.`)
      }
      if (n.verdictMode === 'sub') {
        if (!n.subScorerPrompt.trim()) {
          errs.push(`Verdict "${n.id}" needs a sub-scorer prompt (or switch to a fixed score).`)
        }
      } else {
        const score = Number(n.score)
        if (n.score.trim() === '' || Number.isNaN(score)) {
          errs.push(`Verdict "${n.id}" needs a fixed score between 0 and 1.`)
        } else if (score < 0 || score > 1) {
          errs.push(`Verdict "${n.id}" score must be between 0 and 1.`)
        }
      }
      continue
    }
    if (!n.prompt.trim()) errs.push(`Node "${n.id}" needs a prompt.`)
    if (n.edges.length === 0) {
      errs.push(`Node "${n.id}" needs at least one outgoing edge.`)
    }
    for (const e of n.edges) {
      if (!e.label.trim()) errs.push(`Node "${n.id}" has an edge without a label.`)
      if (!e.target.trim()) {
        errs.push(`Node "${n.id}" edge "${e.label || '?'}" has no target.`)
      } else if (!byId.has(e.target)) {
        errs.push(`Node "${n.id}" edge "${e.label || '?'}" targets missing node "${e.target}".`)
      }
    }
  }

  // Cycle detection (DFS, three-color).
  const state = new Map<string, 1 | 2>()
  let cycleAt: string | null = null
  const visit = (id: string): void => {
    if (cycleAt) return
    const s = state.get(id)
    if (s === 1) {
      cycleAt = id
      return
    }
    if (s === 2) return
    state.set(id, 1)
    for (const e of byId.get(id)?.edges ?? []) {
      if (byId.has(e.target)) visit(e.target)
    }
    state.set(id, 2)
  }
  for (const n of draft.nodes) visit(n.id)
  if (cycleAt) errs.push(`The graph contains a cycle through "${cycleAt}" — a DAG must not loop.`)

  // Reachable verdict from root.
  if (!cycleAt && draft.root && byId.has(draft.root)) {
    const seen = new Set<string>()
    const reach = (id: string): boolean => {
      if (seen.has(id)) return false
      seen.add(id)
      const node = byId.get(id)
      if (!node) return false
      if (node.type === 'verdict') return true
      return node.edges.some((e) => reach(e.target))
    }
    if (!reach(draft.root)) {
      errs.push('No verdict is reachable from the root — every path must end in a verdict leaf.')
    }
  }
  return errs
}

// ─── Traversal-path parsing ──────────────────────────────────────────────────
// The runner records the path in the score reason, formatted by
// formatDagPathSegment: `node["answer" => label -> target]` segments joined
// with " -> " (verdicts appear as a bare id). Fallback routes append
// `, fallback:default` / `, fallback:first` inside the bracket.

export type DagPathSegment = {
  nodeId: string
  answer?: string
  label?: string
  target?: string
  fallback?: string
}

export type DagPath = { segments: DagPathSegment[] }

/** Split on " -> " only outside [...] brackets. */
function splitTopLevel(s: string): string[] {
  const parts: string[] = []
  let depth = 0
  let cur = ''
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (ch === '[') depth++
    else if (ch === ']') depth = Math.max(0, depth - 1)
    if (depth === 0 && s.startsWith(' -> ', i)) {
      parts.push(cur)
      cur = ''
      i += 3
      continue
    }
    cur += ch
  }
  parts.push(cur)
  return parts.map((p) => p.trim()).filter(Boolean)
}

function parsePathSegment(seg: string): DagPathSegment {
  const br = seg.indexOf('[')
  if (br === -1) return { nodeId: seg }
  const nodeId = seg.slice(0, br)
  let inner = seg.slice(br + 1, seg.lastIndexOf(']'))
  let fallback: string | undefined
  const fbIdx = inner.lastIndexOf(', fallback:')
  if (fbIdx !== -1) {
    fallback = inner.slice(fbIdx + 2)
    inner = inner.slice(0, fbIdx)
  }
  const arrowIdx = inner.lastIndexOf(' -> ')
  const target = arrowIdx !== -1 ? inner.slice(arrowIdx + 4).trim() : undefined
  const head = arrowIdx !== -1 ? inner.slice(0, arrowIdx) : inner
  const fatIdx = head.indexOf(' => ')
  const answer =
    fatIdx !== -1 ? head.slice(0, fatIdx).trim().replace(/^"|"$/g, '') : undefined
  const label = fatIdx !== -1 ? head.slice(fatIdx + 4).trim() : undefined
  return { nodeId, answer, label, target, fallback }
}

/**
 * Parse a runner reason string into the traversed path. Returns null when the
 * reason doesn't reference any known node (i.e. it isn't a DAG path).
 */
export function parseDagPath(reason: string, knownIds: Set<string>): DagPath | null {
  const segments = splitTopLevel(reason.trim()).map(parsePathSegment)
  const known = segments.filter((s) => knownIds.has(s.nodeId))
  if (known.length === 0) return null
  return { segments }
}

export function DagPathBreadcrumb({ path }: { path: DagPath }) {
  return (
    <div className="flex flex-wrap items-center gap-1">
      {path.segments.map((s, i) => (
        <Fragment key={`${s.nodeId}-${i}`}>
          {i > 0 && <MoveRight className="h-3 w-3 shrink-0 text-white/30 light:text-black/30" />}
          <span className="rounded border border-brand-secondary-500/40 bg-brand-secondary-500/10 px-1.5 py-0.5 font-mono text-[10.5px] text-brand-secondary-300 light:text-brand-secondary-700">
            {s.nodeId}
            {s.answer != null && (
              <span className="text-brand-secondary-300/60 light:text-brand-secondary-700/60">
                {' '}
                · {s.answer}
              </span>
            )}
          </span>
        </Fragment>
      ))}
    </div>
  )
}

// ─── Auto-layout (layered BFS from root; strays parked below) ────────────────

const LAYOUT_X_GAP = 250
const LAYOUT_Y_GAP = 170

export function autoLayoutPositions(draft: DagDraft): Map<string, { x: number; y: number }> {
  const byId = new Map(draft.nodes.map((n) => [n.id, n]))
  const depths = new Map<string, number>()
  const queue: Array<[string, number]> = []
  if (byId.has(draft.root)) queue.push([draft.root, 0])
  while (queue.length > 0) {
    const [id, d] = queue.shift()!
    if (depths.has(id)) continue
    depths.set(id, d)
    for (const e of byId.get(id)?.edges ?? []) {
      if (byId.has(e.target) && !depths.has(e.target)) queue.push([e.target, d + 1])
    }
  }
  let maxDepth = 0
  for (const d of depths.values()) maxDepth = Math.max(maxDepth, d)
  const levelCounts = new Map<number, number>()
  const pos = new Map<string, { x: number; y: number }>()
  for (const n of draft.nodes) {
    const d = depths.get(n.id) ?? maxDepth + 1
    const i = levelCounts.get(d) ?? 0
    levelCounts.set(d, i + 1)
    pos.set(n.id, { x: i * LAYOUT_X_GAP, y: d * LAYOUT_Y_GAP })
  }
  return pos
}

// ─── Canvas node ─────────────────────────────────────────────────────────────

type DagCanvasContextValue = {
  byId: Map<string, DagNodeDraft>
  root: string
  selectedId: string | null
  /** node id -> position in the traversed path (for staggered highlight). */
  pathIndex: Map<string, number>
  reducedMotion: boolean
}

const DagCanvasContext = createContext<DagCanvasContextValue | null>(null)

const NODE_META: Record<
  DagNodeType,
  { label: string; icon: typeof GitBranch; chip: string; border: string }
> = {
  task: {
    label: 'Task',
    icon: Wand2,
    chip: 'text-sky-300 light:text-sky-700',
    border: 'border-sky-500/40',
  },
  binary_judgement: {
    label: 'Binary',
    icon: GitBranch,
    chip: 'text-violet-300 light:text-violet-700',
    border: 'border-violet-500/40',
  },
  non_binary_judgement: {
    label: 'Non-binary',
    icon: ListTree,
    chip: 'text-fuchsia-300 light:text-fuchsia-700',
    border: 'border-fuchsia-500/40',
  },
  verdict: {
    label: 'Verdict',
    icon: CircleCheck,
    chip: 'text-emerald-300 light:text-emerald-700',
    border: 'border-emerald-500/40',
  },
}

function DagCanvasNode({ data }: NodeProps) {
  const ctx = useContext(DagCanvasContext)
  const nodeId = (data as { nodeId?: string }).nodeId ?? ''
  const node = ctx?.byId.get(nodeId)
  if (!ctx || !node) return null
  const meta = NODE_META[node.type]
  const Icon = meta.icon
  const isRoot = ctx.root === node.id
  const isSelected = ctx.selectedId === node.id
  const pathIdx = ctx.pathIndex.get(node.id)
  const onPath = pathIdx != null

  const preview =
    node.type === 'verdict'
      ? node.verdictMode === 'sub'
        ? 'sub-scorer'
        : `score ${node.score.trim() === '' ? '—' : Number(node.score).toFixed(2)}`
      : node.prompt.replace(/\s+/g, ' ').trim() || 'No prompt yet'

  return (
    <div
      className={[
        'w-[200px] rounded-lg border bg-brand-main-950 px-2.5 py-2 text-left light:bg-white',
        'transition-[box-shadow,border-color,background-color] duration-200 ease-out-strong motion-reduce:transition-none',
        onPath
          ? 'border-brand-secondary-500 bg-brand-secondary-500/10 shadow-lg shadow-brand-secondary-500/20'
          : meta.border,
        isSelected ? 'ring-2 ring-brand-secondary-500/70' : '',
      ].join(' ')}
      style={
        onPath && !ctx.reducedMotion
          ? { transitionDelay: `${(pathIdx ?? 0) * 70}ms` }
          : undefined
      }
    >
      <Handle
        type="target"
        position={Position.Top}
        className="!h-1.5 !w-1.5 !border-0 !bg-white/30 light:!bg-black/25"
      />
      <div className="flex items-center gap-1.5">
        <Icon className={`h-3 w-3 shrink-0 ${meta.chip}`} />
        <span className={`text-[10px] font-semibold uppercase tracking-wide ${meta.chip}`}>
          {meta.label}
        </span>
        {isRoot && (
          <span className="ml-auto rounded bg-brand-secondary-500/15 px-1 py-px text-[9px] font-bold uppercase tracking-wider text-brand-secondary-300 light:text-brand-secondary-700">
            root
          </span>
        )}
      </div>
      <div className="mt-1 truncate font-mono text-[11px] font-medium text-white light:text-brand-main-50">
        {node.id}
      </div>
      <div className="mt-0.5 line-clamp-2 text-[10px] leading-snug text-white/45 light:text-black/45">
        {preview}
      </div>
      {node.type !== 'verdict' && (
        <Handle
          type="source"
          position={Position.Bottom}
          className="!h-1.5 !w-1.5 !border-0 !bg-white/30 light:!bg-black/25"
        />
      )}
    </div>
  )
}

const dagNodeTypes = { dagNode: DagCanvasNode }

function usePrefersReducedMotion(): boolean {
  const [reduced, setReduced] = useState(
    () =>
      typeof window !== 'undefined' &&
      window.matchMedia('(prefers-reduced-motion: reduce)').matches,
  )
  useEffect(() => {
    const mq = window.matchMedia('(prefers-reduced-motion: reduce)')
    const onChange = () => setReduced(mq.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return reduced
}

// ─── Editor ──────────────────────────────────────────────────────────────────

const pressableClass =
  'transition-[color,background-color,border-color,transform] duration-150 ease-out-strong active:scale-[0.98] motion-reduce:transition-none motion-reduce:active:scale-100'

function nextNodeId(draft: DagDraft, type: DagNodeType): string {
  const prefix =
    type === 'verdict' ? 'verdict' : type === 'task' ? 'task' : 'check'
  const ids = new Set(draft.nodes.map((n) => n.id))
  for (let i = 1; ; i++) {
    const id = `${prefix}_${i}`
    if (!ids.has(id)) return id
  }
}

function makeRfNode(n: DagNodeDraft, fallback: { x: number; y: number }): Node {
  return {
    id: n.id,
    type: 'dagNode',
    position: n.position ?? fallback,
    data: { nodeId: n.id },
    draggable: true,
  }
}

export function DagScorerEditor({
  value,
  onChange,
  path,
}: {
  value: DagDraft
  onChange: (next: DagDraft) => void
  /** Traversed path from the latest test run — highlighted on the canvas. */
  path: DagPath | null
}) {
  const reducedMotion = usePrefersReducedMotion()
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const byId = useMemo(() => new Map(value.nodes.map((n) => [n.id, n])), [value.nodes])

  const pathIndex = useMemo(() => {
    const map = new Map<string, number>()
    path?.segments.forEach((s, i) => {
      if (byId.has(s.nodeId) && !map.has(s.nodeId)) map.set(s.nodeId, i)
    })
    return map
  }, [path, byId])

  const pathEdgeKeys = useMemo(() => {
    const set = new Set<string>()
    if (!path) return set
    for (let i = 0; i < path.segments.length - 1; i++) {
      set.add(`${path.segments[i].nodeId}=>${path.segments[i + 1].nodeId}`)
    }
    return set
  }, [path])

  // Canvas node state: positions live here while dragging; structure syncs
  // from the draft (add/remove); drag-stop persists back into the draft.
  const [rfNodes, setRfNodes] = useState<Node[]>(() => {
    const layout = autoLayoutPositions(value)
    return value.nodes.map((n) => makeRfNode(n, layout.get(n.id) ?? { x: 0, y: 0 }))
  })

  const structuralKey = value.nodes.map((n) => n.id).join('|')
  useEffect(() => {
    setRfNodes((cur) => {
      const curById = new Map(cur.map((n) => [n.id, n]))
      const layout = autoLayoutPositions(value)
      let changed = cur.length !== value.nodes.length
      const next = value.nodes.map((n) => {
        const existing = curById.get(n.id)
        if (existing) return existing
        changed = true
        return makeRfNode(n, layout.get(n.id) ?? { x: 40, y: 40 })
      })
      return changed ? next : cur
    })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [structuralKey])

  const rfEdges: Edge[] = useMemo(
    () =>
      value.nodes.flatMap((n) =>
        n.edges
          .filter((e) => e.target && byId.has(e.target))
          .map((e, i) => {
            const onPath = pathEdgeKeys.has(`${n.id}=>${e.target}`)
            return {
              id: `${n.id}:${i}:${e.target}`,
              source: n.id,
              target: e.target,
              label: e.label || '?',
              animated: onPath && !reducedMotion,
              style: onPath
                ? { stroke: 'var(--color-brand-secondary-500)', strokeWidth: 2 }
                : { stroke: 'var(--color-border)', strokeWidth: 1.5 },
              labelStyle: {
                fill: onPath
                  ? 'var(--color-brand-secondary-500)'
                  : 'var(--color-foreground)',
                fontSize: 10,
                fontFamily: 'var(--font-mono, monospace)',
              },
              labelBgStyle: { fill: 'var(--color-background)', fillOpacity: 0.85 },
              markerEnd: {
                type: MarkerType.ArrowClosed,
                color: onPath
                  ? 'var(--color-brand-secondary-500)'
                  : 'var(--color-border)',
              },
            } satisfies Edge
          }),
      ),
    [value.nodes, byId, pathEdgeKeys, reducedMotion],
  )

  const onNodesChange = useCallback(
    (changes: NodeChange[]) => setRfNodes((nds) => applyNodeChanges(changes, nds)),
    [],
  )

  const persistPosition = useCallback(
    (_: unknown, node: Node) => {
      onChange({
        ...value,
        nodes: value.nodes.map((n) =>
          n.id === node.id
            ? { ...n, position: { x: node.position.x, y: node.position.y } }
            : n,
        ),
      })
    },
    [value, onChange],
  )

  const onConnect = useCallback(
    (conn: Connection) => {
      if (!conn.source || !conn.target || conn.source === conn.target) return
      const source = byId.get(conn.source)
      if (!source || source.type === 'verdict') return
      let label = ''
      if (source.type === 'binary_judgement') {
        const used = new Set(source.edges.map((e) => e.label))
        label = !used.has('yes') ? 'yes' : !used.has('no') ? 'no' : ''
      }
      onChange({
        ...value,
        nodes: value.nodes.map((n) =>
          n.id === conn.source
            ? { ...n, edges: [...n.edges, { label, target: conn.target! }] }
            : n,
        ),
      })
      setSelectedId(conn.source)
    },
    [value, onChange, byId],
  )

  const addNode = (type: DagNodeType) => {
    const id = nextNodeId(value, type)
    const maxY = Math.max(0, ...value.nodes.map((n) => n.position?.y ?? 0))
    const node: DagNodeDraft = {
      id,
      type,
      prompt: '',
      edges: [],
      verdictMode: 'fixed',
      score: type === 'verdict' ? '1' : '',
      subScorerPrompt: '',
      position: { x: 40, y: maxY + LAYOUT_Y_GAP },
    }
    onChange({
      ...value,
      root: value.root || (type !== 'verdict' ? id : value.root),
      nodes: [...value.nodes, node],
    })
    setSelectedId(id)
  }

  const applyAutoLayout = () => {
    const layout = autoLayoutPositions(value)
    onChange({
      ...value,
      nodes: value.nodes.map((n) => ({ ...n, position: layout.get(n.id) ?? n.position })),
    })
    setRfNodes((cur) =>
      cur.map((n) => ({ ...n, position: layout.get(n.id) ?? n.position })),
    )
  }

  const updateNode = (id: string, patch: Partial<DagNodeDraft>) =>
    onChange({
      ...value,
      nodes: value.nodes.map((n) => (n.id === id ? { ...n, ...patch } : n)),
    })

  const renameNode = (oldId: string, rawNewId: string) => {
    const newId = rawNewId.trim().replace(/[^\w-]+/g, '_')
    if (!newId || newId === oldId || byId.has(newId)) return
    onChange({
      root: value.root === oldId ? newId : value.root,
      nodes: value.nodes.map((n) => ({
        ...n,
        id: n.id === oldId ? newId : n.id,
        edges: n.edges.map((e) => (e.target === oldId ? { ...e, target: newId } : e)),
      })),
    })
    setRfNodes((cur) =>
      cur.map((n) => (n.id === oldId ? { ...n, id: newId, data: { nodeId: newId } } : n)),
    )
    setSelectedId(newId)
  }

  const deleteNode = (id: string) => {
    onChange({
      root: value.root === id ? '' : value.root,
      nodes: value.nodes
        .filter((n) => n.id !== id)
        .map((n) => ({ ...n, edges: n.edges.filter((e) => e.target !== id) })),
    })
    setSelectedId(null)
  }

  const selected = selectedId ? (byId.get(selectedId) ?? null) : null

  const ctxValue = useMemo<DagCanvasContextValue>(
    () => ({ byId, root: value.root, selectedId, pathIndex, reducedMotion }),
    [byId, value.root, selectedId, pathIndex, reducedMotion],
  )

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-1.5">
        {DAG_NODE_TYPE_OPTIONS.map((opt) => {
          const Icon = NODE_META[opt.value].icon
          return (
            <button
              key={opt.value}
              type="button"
              onClick={() => addNode(opt.value)}
              className={`flex items-center gap-1.5 rounded-lg border border-brand-main-700 bg-brand-main-900 px-2.5 py-1.5 text-xs font-medium text-white/70 hover:border-brand-secondary-500/60 hover:text-white light:border-brand-main-200 light:bg-white light:text-black/60 light:hover:text-black ${pressableClass}`}
            >
              <Plus className="h-3 w-3 text-white/40 light:text-black/40" />
              <Icon className={`h-3 w-3 ${NODE_META[opt.value].chip}`} />
              {opt.label}
            </button>
          )
        })}
        <button
          type="button"
          onClick={applyAutoLayout}
          className={`ml-auto flex items-center gap-1.5 rounded-lg border border-brand-main-700 bg-brand-main-900 px-2.5 py-1.5 text-xs font-medium text-white/70 hover:text-white light:border-brand-main-200 light:bg-white light:text-black/60 light:hover:text-black ${pressableClass}`}
        >
          <LayoutGrid className="h-3 w-3" />
          Auto-layout
        </button>
      </div>

      <div className="flex gap-3 max-xl:flex-col">
        <DagCanvasContext.Provider value={ctxValue}>
          <div className="relative h-[460px] min-w-0 flex-1 overflow-hidden rounded-lg border border-brand-main-700 bg-brand-main-950 light:border-brand-main-200 light:bg-white">
            <ReactFlow
              nodes={rfNodes}
              edges={rfEdges}
              nodeTypes={dagNodeTypes}
              onNodesChange={onNodesChange}
              onNodeDragStop={persistPosition}
              onConnect={onConnect}
              onNodeClick={(_, node) => setSelectedId(node.id)}
              onPaneClick={() => setSelectedId(null)}
              nodesDraggable
              nodesConnectable
              edgesFocusable={false}
              deleteKeyCode={null}
              fitView
              fitViewOptions={{ padding: 0.25, maxZoom: 1 }}
              minZoom={0.2}
              proOptions={{ hideAttribution: true }}
            >
              <Background
                variant={BackgroundVariant.Dots}
                gap={16}
                size={1}
                color="var(--color-border)"
              />
              <Controls
                showInteractive={false}
                className="!border-brand-main-600 !bg-brand-main-800 light:!border-brand-main-200 light:!bg-white"
              />
            </ReactFlow>
            {path && (
              <div className="absolute inset-x-2 bottom-2 z-10 rounded-lg border border-brand-main-700 bg-brand-main-950/90 px-2.5 py-2 backdrop-blur-sm light:border-brand-main-200 light:bg-white/90">
                <span className="mb-1 block text-[9.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
                  Path taken
                </span>
                <DagPathBreadcrumb path={path} />
              </div>
            )}
          </div>
        </DagCanvasContext.Provider>

        <div className="w-[300px] shrink-0 max-xl:w-full">
          {selected ? (
            <DagNodeInspector
              key={selected.id}
              node={selected}
              draft={value}
              isRoot={value.root === selected.id}
              onPatch={(patch) => updateNode(selected.id, patch)}
              onRename={(newId) => renameNode(selected.id, newId)}
              onSetRoot={() => onChange({ ...value, root: selected.id })}
              onDelete={() => deleteNode(selected.id)}
            />
          ) : (
            <div className="flex h-full min-h-[200px] items-center justify-center rounded-lg border border-dashed border-brand-main-700 p-4 text-center text-xs leading-relaxed text-white/40 light:border-brand-main-300 light:text-black/45">
              Select a node to edit its prompt and routes.
              <br />
              Drag from a node&apos;s bottom handle to draw an edge.
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

// ─── Node inspector ──────────────────────────────────────────────────────────

function DagNodeInspector({
  node,
  draft,
  isRoot,
  onPatch,
  onRename,
  onSetRoot,
  onDelete,
}: {
  node: DagNodeDraft
  draft: DagDraft
  isRoot: boolean
  onPatch: (patch: Partial<DagNodeDraft>) => void
  onRename: (newId: string) => void
  onSetRoot: () => void
  onDelete: () => void
}) {
  const [idDraft, setIdDraft] = useState(node.id)
  const otherNodes = draft.nodes.filter((n) => n.id !== node.id)
  const promptVars = [
    'input',
    'output',
    'expected_output',
    'context',
    'metadata',
    'last',
    ...draft.nodes.filter((n) => n.id !== node.id && n.type !== 'verdict').map((n) => n.id),
  ]

  const setEdge = (i: number, patch: Partial<DagEdgeDraft>) =>
    onPatch({ edges: node.edges.map((e, idx) => (idx === i ? { ...e, ...patch } : e)) })
  const removeEdge = (i: number) =>
    onPatch({ edges: node.edges.filter((_, idx) => idx !== i) })
  const addEdge = () =>
    onPatch({ edges: [...node.edges, { label: '', target: otherNodes[0]?.id ?? '' }] })

  const meta = NODE_META[node.type]
  const Icon = meta.icon

  return (
    <div className="flex flex-col gap-3 rounded-lg border border-brand-main-700 bg-brand-main-950 p-3 light:border-brand-main-200 light:bg-white">
      <div className="flex items-center justify-between gap-2">
        <span className={`flex items-center gap-1.5 text-[10px] font-semibold uppercase tracking-wide ${meta.chip}`}>
          <Icon className="h-3 w-3" />
          {meta.label} node
        </span>
        <div className="flex items-center gap-1">
          {!isRoot && (
            <button
              type="button"
              onClick={onSetRoot}
              title="Set as root"
              className={`flex items-center gap-1 rounded border border-brand-main-700 px-1.5 py-1 text-[10px] font-medium text-white/60 hover:text-white light:border-brand-main-200 light:text-black/55 light:hover:text-black ${pressableClass}`}
            >
              <Crosshair className="h-3 w-3" />
              Set root
            </button>
          )}
          <button
            type="button"
            onClick={onDelete}
            title="Delete node"
            className={`rounded border border-brand-main-700 p-1 text-white/40 hover:border-red-500/50 hover:text-red-400 light:border-brand-main-200 light:text-black/40 ${pressableClass}`}
          >
            <Trash2 className="h-3 w-3" />
          </button>
        </div>
      </div>

      <div className="grid grid-cols-2 gap-2">
        <EvaluationField label="Id" htmlFor="dag-node-id">
          <Input
            id="dag-node-id"
            value={idDraft}
            onChange={(e) => setIdDraft(e.target.value)}
            onBlur={() => {
              if (idDraft !== node.id) onRename(idDraft)
              setIdDraft((cur) => cur.trim().replace(/[^\w-]+/g, '_') || node.id)
            }}
            className={`${evaluationInputClass} h-8 font-mono text-xs`}
          />
        </EvaluationField>
        <EvaluationField label="Type">
          <Select
            value={node.type}
            onValueChange={(v) => {
              const type = v as DagNodeType
              onPatch(type === 'verdict' ? { type, edges: [] } : { type })
            }}
          >
            <SelectTrigger className={`${evaluationSelectTriggerClass} h-8 text-xs`}>
              <SelectValue />
            </SelectTrigger>
            <SelectContent className={evaluationSelectContentClass}>
              {DAG_NODE_TYPE_OPTIONS.map((o) => (
                <SelectItem key={o.value} value={o.value}>
                  {o.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </EvaluationField>
      </div>

      {node.type !== 'verdict' && (
        <>
          <EvaluationField label="Prompt">
            <MustacheTextarea
              value={node.prompt}
              onChange={(prompt) => onPatch({ prompt })}
              placeholder={
                node.type === 'binary_judgement'
                  ? 'Does {{output}} answer {{input}}? Answer yes or no.'
                  : 'Classify {{output}} as one of the route labels.'
              }
              rows={5}
              vars={promptVars}
            />
          </EvaluationField>

          <div>
            <span className="mb-1.5 block text-[10.5px] font-semibold uppercase tracking-wide text-white/40 light:text-black/45">
              Routes (label → target)
            </span>
            <div className="space-y-1.5">
              {node.edges.map((e, i) => (
                <div key={i} className="flex items-center gap-1.5">
                  <Input
                    value={e.label}
                    onChange={(ev) => setEdge(i, { label: ev.target.value })}
                    placeholder={node.type === 'binary_judgement' ? 'yes' : 'label'}
                    className={`${evaluationInputClass} h-8 w-24 font-mono text-xs`}
                  />
                  <Select value={e.target || undefined} onValueChange={(v) => setEdge(i, { target: v })}>
                    <SelectTrigger className={`${evaluationSelectTriggerClass} h-8 flex-1 text-xs`}>
                      <SelectValue placeholder="target" />
                    </SelectTrigger>
                    <SelectContent className={evaluationSelectContentClass}>
                      {otherNodes.map((n) => (
                        <SelectItem key={n.id} value={n.id}>
                          {n.id}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                  <button
                    type="button"
                    onClick={() => removeEdge(i)}
                    className="shrink-0 text-white/35 hover:text-red-400 light:text-black/35"
                    aria-label="Remove route"
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </div>
              ))}
              <Button type="button" variant="outline" size="sm" onClick={addEdge} disabled={otherNodes.length === 0}>
                <Plus className="h-3 w-3" />
                Add route
              </Button>
            </div>
            <p className="mt-1.5 text-[10px] leading-relaxed text-white/30 light:text-black/35">
              The judge&apos;s answer routes to the matching label. A route labeled{' '}
              <code className="text-white/45 light:text-black/50">default</code> catches
              unmatched answers.
            </p>
          </div>
        </>
      )}

      {node.type === 'verdict' && (
        <>
          <div className="flex gap-1 rounded-lg border border-brand-main-700 bg-brand-main-900 p-0.5 light:border-brand-main-200 light:bg-white">
            {(
              [
                { value: 'fixed', label: 'Fixed score' },
                { value: 'sub', label: 'Sub-scorer' },
              ] as const
            ).map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => onPatch({ verdictMode: opt.value })}
                className={`flex-1 rounded px-2 py-1 text-[11px] font-medium ${pressableClass} ${
                  node.verdictMode === opt.value
                    ? 'bg-brand-main-700 text-white light:bg-brand-main-100 light:text-brand-main-950'
                    : 'text-white/45 hover:text-white light:text-black/45 light:hover:text-black'
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
          {node.verdictMode === 'fixed' ? (
            <EvaluationField label="Score (0–1)" htmlFor="dag-verdict-score">
              <div className="flex items-center gap-3">
                <input
                  id="dag-verdict-score"
                  type="range"
                  min="0"
                  max="1"
                  step="0.05"
                  value={node.score || '0'}
                  onChange={(e) => onPatch({ score: e.target.value })}
                  className="flex-1 accent-brand-secondary-500"
                />
                <Input
                  type="number"
                  min="0"
                  max="1"
                  step="0.05"
                  value={node.score}
                  onChange={(e) => onPatch({ score: e.target.value })}
                  className={`${evaluationInputClass} h-8 w-20 text-right tabular-nums`}
                />
              </div>
            </EvaluationField>
          ) : (
            <EvaluationField label="Sub-scorer prompt">
              <MustacheTextarea
                value={node.subScorerPrompt}
                onChange={(subScorerPrompt) => onPatch({ subScorerPrompt })}
                placeholder="Rate how well {{output}} matches {{expected_output}} from 0 to 1."
                rows={5}
                vars={promptVars}
              />
            </EvaluationField>
          )}
        </>
      )}
    </div>
  )
}
