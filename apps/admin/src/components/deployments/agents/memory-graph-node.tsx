import { memo, useContext } from 'react'
import { Handle, Position } from '@xyflow/react'
import type { NodeProps } from '@xyflow/react'
import { Brain, FileText, Lightbulb, NotebookText } from 'lucide-react'
import type { MemoryGraphNode } from './memory-graph-layout'
import { MemoryGraphContext } from './memory-graph'

const MEMORY_TYPE_LABELS: Record<string, string> = {
  fact: 'Fact',
  instruction: 'Instruction',
  session_summary: 'Summary',
  document: 'Document',
}

function MemoryGraphNodeComponent({ id, data }: NodeProps<MemoryGraphNode>) {
  const ctx = useContext(MemoryGraphContext)
  const { memory, nodeWidth, nodeHeight, typeColor, typeBg } = data

  const isHoveredNode = ctx?.hoveredId === id
  const isSelected = ctx?.selectedId === id
  const isConnected = ctx?.connectedIds?.has(id) ?? false
  const someNodeHovered = !!ctx?.hoveredId
  const dimmed = someNodeHovered && !isHoveredNode && !isConnected

  const label = MEMORY_TYPE_LABELS[memory.memoryType] ?? memory.memoryType
  const confidencePercent = Math.round((memory.confidence ?? 1) * 100)
  const Icon = getTypeIcon(memory.memoryType)

  return (
    <div
      className="relative hover:cursor-pointer"
      style={{
        width: nodeWidth,
        opacity: !memory.isActive ? 0.45 : dimmed ? 0.28 : 1,
      }}
      onMouseEnter={() => ctx?.onHover(id)}
      onMouseLeave={() => ctx?.onHover(null)}
      onClick={(e) => {
        e.stopPropagation()
        ctx?.onSelect(id)
      }}
    >
      <Handle
        type="target"
        position={Position.Left}
        className="!h-3 !w-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
        style={{ opacity: 0 }}
      />

      <div
        className={`rounded-lg border bg-brand-main-800 px-4 py-3 shadow-lg transition-all ${
          isSelected
            ? 'border-transparent ring-3 ring-brand-secondary-500'
            : 'border-brand-main-600 hover:border-brand-main-500'
        }`}
        style={{ minHeight: nodeHeight }}
      >
        <div className="flex items-center gap-2">
          <div
            className="flex h-7 w-7 shrink-0 items-center justify-center rounded"
            style={{ backgroundColor: typeBg, color: typeColor }}
          >
            <Icon className="h-4 w-4" />
          </div>
          <div className="min-w-0 flex-1">
            <div className="truncate text-sm font-medium text-white light:text-brand-main-50">
              {memory.factKey || label}
            </div>
            <div className="text-xs text-brand-main-400">{label}</div>
          </div>
          <div
            className={`h-2 w-2 rounded-full ${
              memory.isActive ? 'bg-emerald-500' : 'bg-brand-main-600'
            }`}
            title={memory.isActive ? 'Active memory' : 'Inactive memory'}
          />
        </div>

        <div className="mt-2 truncate border-t border-brand-main-700 pt-2 text-xs text-brand-main-300">
          {getSummary(memory.content)}
        </div>

        <div className="mt-2 flex items-center gap-2 border-t border-brand-main-700 pt-2 text-[10px] text-brand-main-400">
          <span className="truncate">{formatSource(memory.source)}</span>
          <span className="h-1 w-1 rounded-full bg-brand-main-600" />
          <span>{memory.scope}</span>
          <span className="ml-auto tabular-nums">{confidencePercent}%</span>
        </div>
      </div>

      <Handle
        type="source"
        position={Position.Right}
        className="!h-3 !w-3 !border-2 !border-brand-main-600 !bg-brand-main-800"
        style={{ opacity: 0 }}
      />
    </div>
  )
}

function getSummary(content: string) {
  const compact = content.replace(/\s+/g, ' ').trim()
  return compact.length > 64 ? `${compact.slice(0, 61)}...` : compact
}

function formatSource(source: string) {
  return source === 'auto_extracted' ? 'auto-extracted' : source || 'manual'
}

function getTypeIcon(memoryType: string) {
  switch (memoryType) {
    case 'instruction':
      return NotebookText
    case 'session_summary':
      return Lightbulb
    case 'document':
      return FileText
    default:
      return Brain
  }
}

export default memo(MemoryGraphNodeComponent)
