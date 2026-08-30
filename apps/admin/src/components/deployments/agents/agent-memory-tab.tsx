import { useState } from 'react'
import { Brain, Plus, Trash2, Eye, EyeOff, ChevronDown, Check, X, Pencil, LayoutList, Network } from 'lucide-react'
import { ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import {
    useAgentMemories,
    useCreateAgentMemory,
    useDeleteAgentMemory,
    useUpdateAgentMemory,
    useDeactivateAgentMemory,
} from '@/hooks/deployments/use-agent-memories'
import type { AgentMemoryEntry } from '@/server/agents'
import { MemoryGraph } from './memory-graph'

const {
    Button, Input, Label, Textarea, Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
    Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter,
    Badge, Collapsible, CollapsibleTrigger, CollapsibleContent,
    Tooltip, TooltipProvider,
} = ui

const MEMORY_TYPE_COLORS: Record<string, string> = {
    fact: 'bg-blue-500/20 text-blue-300 border-blue-500/30',
    instruction: 'bg-purple-500/20 text-purple-300 border-purple-500/30',
    session_summary: 'bg-green-500/20 text-green-300 border-green-500/30',
    document: 'bg-yellow-500/20 text-yellow-300 border-yellow-500/30',
}

const MEMORY_TYPE_LABELS: Record<string, string> = {
    fact: 'Fact',
    instruction: 'Instruction',
    session_summary: 'Summary',
    document: 'Document',
}

const SCOPE_COLORS: Record<string, string> = {
    agent: 'bg-blue-500/15 text-blue-400 border-blue-500/25',
    user: 'bg-emerald-500/15 text-emerald-400 border-emerald-500/25',
    global: 'bg-violet-500/15 text-violet-400 border-violet-500/25',
}

const SCOPE_LABELS: Record<string, string> = {
    agent: 'Agent',
    user: 'User',
    global: 'Global',
}

interface AgentMemoryTabProps {
    agentId: string
    memoryEnabled?: boolean
}

export function AgentMemoryTab({ agentId, memoryEnabled }: AgentMemoryTabProps) {
    const [viewMode, setViewMode] = useState<'list' | 'graph'>('list')
    const [typeFilter, setTypeFilter] = useState<string>('all')
    const [scopeFilter, setScopeFilter] = useState<string>('all')
    const [showInactive, setShowInactive] = useState(false)
    const [showCreateDialog, setShowCreateDialog] = useState(false)

    const { data: memories = [], isLoading } = useAgentMemories(agentId, {
        activeOnly: !showInactive,
        memoryType: typeFilter !== 'all' ? typeFilter : undefined,
        scope: scopeFilter !== 'all' ? scopeFilter : undefined,
        limit: 200,
    })
    const deleteMutation = useDeleteAgentMemory()
    const deactivateMutation = useDeactivateAgentMemory()

    const handleDelete = async (memoryId: string) => {
        try {
            await deleteMutation.mutateAsync({ memoryId })
            toast.success('Memory deleted')
        } catch {
            toast.error('Failed to delete memory')
        }
    }

    const handleDeactivate = async (memoryId: string) => {
        try {
            await deactivateMutation.mutateAsync({ memoryId })
            toast.success('Memory deactivated')
        } catch {
            toast.error('Failed to deactivate memory')
        }
    }

    const facts = memories.filter((m) => m.memoryType === 'fact')
    const instructions = memories.filter((m) => m.memoryType === 'instruction')
    const summaries = memories.filter((m) => m.memoryType === 'session_summary')
    const documents = memories.filter((m) => m.memoryType === 'document')

    // Stats
    const statParts: string[] = []
    if (facts.length > 0) statParts.push(`${facts.length} fact${facts.length !== 1 ? 's' : ''}`)
    if (instructions.length > 0) statParts.push(`${instructions.length} instruction${instructions.length !== 1 ? 's' : ''}`)
    if (summaries.length > 0) statParts.push(`${summaries.length} summar${summaries.length !== 1 ? 'ies' : 'y'}`)
    if (documents.length > 0) statParts.push(`${documents.length} document${documents.length !== 1 ? 's' : ''}`)

    if (isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-zinc-400">
                <Brain className="w-5 h-5 animate-pulse mr-2" />
                Loading memories...
            </div>
        )
    }

    if (memories.length === 0) {
        return (
            <div className="flex-1 flex flex-col">
                <EmptyState memoryEnabled={memoryEnabled} />
                <CreateMemoryDialog
                    agentId={agentId}
                    open={showCreateDialog}
                    onOpenChange={setShowCreateDialog}
                />
            </div>
        )
    }

    return (
        <TooltipProvider>
            <div className="space-y-4">
                {/* Header bar */}
                <div className="flex items-center justify-between gap-3 flex-wrap">
                    <div className="flex items-center gap-2 flex-wrap">
                        <Select value={typeFilter} onValueChange={setTypeFilter}>
                            <SelectTrigger className="w-[150px] h-8 text-xs bg-brand-main-900/60 border-brand-main-600 text-zinc-200">
                                <SelectValue placeholder="Filter by type" />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200">
                                <SelectItem value="all">All Types</SelectItem>
                                <SelectItem value="fact">Facts</SelectItem>
                                <SelectItem value="instruction">Instructions</SelectItem>
                                <SelectItem value="session_summary">Summaries</SelectItem>
                                <SelectItem value="document">Documents</SelectItem>
                            </SelectContent>
                        </Select>

                        <Select value={scopeFilter} onValueChange={setScopeFilter}>
                            <SelectTrigger className="w-[130px] h-8 text-xs bg-brand-main-900/60 border-brand-main-600 text-zinc-200">
                                <SelectValue placeholder="Scope" />
                            </SelectTrigger>
                            <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200">
                                <SelectItem value="all">All Scopes</SelectItem>
                                <SelectItem value="agent">Agent</SelectItem>
                                <SelectItem value="user">User</SelectItem>
                                <SelectItem value="global">Global</SelectItem>
                            </SelectContent>
                        </Select>

                        <button
                            onClick={() => setShowInactive(!showInactive)}
                            className={`flex items-center gap-1 px-2 py-1 rounded text-xs border transition-colors ${showInactive
                                ? 'bg-amber-500/15 text-amber-400 border-amber-500/30'
                                : 'text-zinc-500 border-brand-main-700/50 hover:text-zinc-300'
                                }`}
                        >
                            {showInactive ? <Eye className="w-3 h-3" /> : <EyeOff className="w-3 h-3" />}
                            {showInactive ? 'Showing all' : 'Active only'}
                        </button>

                        {statParts.length > 0 && (
                            <span className="text-[11px] text-zinc-500 ml-1">
                                {statParts.join(', ')}
                            </span>
                        )}
                    </div>

                    <div className="flex items-center gap-2">
                        {memoryEnabled !== undefined && (
                            <span className={`flex items-center gap-1 text-[10px] ${memoryEnabled ? 'text-emerald-400' : 'text-zinc-600'}`}>
                                <span className={`w-1.5 h-1.5 rounded-full ${memoryEnabled ? 'bg-emerald-400' : 'bg-zinc-600'}`} />
                                {memoryEnabled ? 'Auto-extract on' : 'Auto-extract off'}
                            </span>
                        )}
                        <div className="flex items-center rounded-md border border-brand-main-600 overflow-hidden">
                            <button
                                onClick={() => setViewMode('list')}
                                className={`flex items-center gap-1 px-2 py-1 text-xs transition-colors ${viewMode === 'list'
                                    ? 'bg-brand-main-700/60 text-zinc-200'
                                    : 'text-zinc-500 hover:text-zinc-300'
                                    }`}
                            >
                                <LayoutList className="w-3 h-3" />
                                List
                            </button>
                            <button
                                onClick={() => setViewMode('graph')}
                                className={`flex items-center gap-1 px-2 py-1 text-xs transition-colors border-l border-brand-main-600 ${viewMode === 'graph'
                                    ? 'bg-brand-main-700/60 text-zinc-200'
                                    : 'text-zinc-500 hover:text-zinc-300'
                                    }`}
                            >
                                <Network className="w-3 h-3" />
                                Graph
                            </button>
                        </div>
                        <Button
                            variant="outline"
                            size="sm"
                            onClick={() => setShowCreateDialog(true)}
                            className="gap-1 h-8 text-xs border-brand-main-600 text-zinc-400 hover:text-white light:hover:text-brand-main-50"
                        >
                            <Plus className="w-3 h-3" />
                            Add
                        </Button>
                    </div>
                </div>

                {viewMode === 'graph' ? (
                    <MemoryGraph memories={memories} />
                ) : (
                    <div className="space-y-3">
                        {(typeFilter === 'all' || typeFilter === 'fact') && facts.length > 0 && (
                            <MemorySection
                                title="Facts"
                                count={facts.length}
                                memories={sortByAccess(facts)}
                                onDelete={handleDelete}
                                onDeactivate={handleDeactivate}
                            />
                        )}
                        {(typeFilter === 'all' || typeFilter === 'instruction') && instructions.length > 0 && (
                            <MemorySection
                                title="Instructions"
                                count={instructions.length}
                                memories={sortByAccess(instructions)}
                                onDelete={handleDelete}
                                onDeactivate={handleDeactivate}
                            />
                        )}
                        {(typeFilter === 'all' || typeFilter === 'session_summary') && summaries.length > 0 && (
                            <MemorySection
                                title="Session Summaries"
                                count={summaries.length}
                                memories={sortByAccess(summaries)}
                                onDelete={handleDelete}
                                onDeactivate={handleDeactivate}
                            />
                        )}
                        {(typeFilter === 'all' || typeFilter === 'document') && documents.length > 0 && (
                            <MemorySection
                                title="Documents"
                                count={documents.length}
                                memories={sortByAccess(documents)}
                                onDelete={handleDelete}
                                onDeactivate={handleDeactivate}
                            />
                        )}
                    </div>
                )}

                <CreateMemoryDialog
                    agentId={agentId}
                    open={showCreateDialog}
                    onOpenChange={setShowCreateDialog}
                />
            </div>
        </TooltipProvider>
    )
}

function sortByAccess(memories: AgentMemoryEntry[]): AgentMemoryEntry[] {
    return [...memories].sort((a, b) => (b.accessCount ?? 0) - (a.accessCount ?? 0))
}

function EmptyState({ memoryEnabled }: { memoryEnabled?: boolean }) {
    return (
        <div className="flex-1 flex flex-col items-center justify-center py-12">
            <div className="relative mb-6">
                <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                    <Brain className="size-8 text-brand-secondary-400" />
                </div>
            </div>
            <h3 className="text-base font-medium text-white mb-2 light:text-brand-main-50">No memories yet</h3>
            <p className="text-sm text-white/50 max-w-sm text-center leading-relaxed light:text-black/50">
                Memories are automatically extracted from agent conversations.
                {memoryEnabled === false && (
                    <> Auto-extract is currently disabled — enable it in the agent's Memory config.</>
                )}
                {memoryEnabled === true && (
                    <> Run a session to start building your agent's memory.</>
                )}
            </p>
        </div>
    )
}

function MemorySection({
    title,
    count,
    memories,
    onDelete,
    onDeactivate,
}: {
    title: string
    count: number
    memories: AgentMemoryEntry[]
    onDelete: (id: string) => void
    onDeactivate: (id: string) => void
}) {
    const [open, setOpen] = useState(true)

    return (
        <Collapsible open={open} onOpenChange={setOpen}>
            <CollapsibleTrigger asChild>
                <button className="flex items-center gap-2 w-full text-left group py-1">
                    <ChevronDown className={`w-3 h-3 text-zinc-500 transition-transform ${open ? '' : '-rotate-90'}`} />
                    <span className="text-xs font-medium text-zinc-400 uppercase tracking-wider">{title}</span>
                    <span className="text-[10px] text-zinc-600 tabular-nums">({count})</span>
                </button>
            </CollapsibleTrigger>
            <CollapsibleContent>
                <div className="space-y-1 mt-1">
                    {memories.map((m) => (
                        <MemoryRow key={m.id} memory={m} onDelete={onDelete} onDeactivate={onDeactivate} />
                    ))}
                </div>
            </CollapsibleContent>
        </Collapsible>
    )
}

function MemoryRow({
    memory,
    onDelete,
    onDeactivate,
}: {
    memory: AgentMemoryEntry
    onDelete: (id: string) => void
    onDeactivate: (id: string) => void
}) {
    const [editing, setEditing] = useState(false)
    const [editContent, setEditContent] = useState(memory.content)
    const [editFactKey, setEditFactKey] = useState(memory.factKey ?? '')
    const updateMutation = useUpdateAgentMemory()

    const typeClass = MEMORY_TYPE_COLORS[memory.memoryType] ?? 'bg-zinc-500/20 text-zinc-300'
    const typeLabel = MEMORY_TYPE_LABELS[memory.memoryType] ?? memory.memoryType
    const scopeClass = SCOPE_COLORS[memory.scope] ?? ''
    const scopeLabel = SCOPE_LABELS[memory.scope] ?? memory.scope
    const isInactive = !memory.isActive
    const isSuperseded = !!memory.supersededBy

    const handleSave = async () => {
        try {
            await updateMutation.mutateAsync({
                memoryId: memory.id,
                content: editContent.trim(),
                factKey: editFactKey.trim() || undefined,
                confidence: memory.confidence,
            })
            toast.success('Memory updated')
            setEditing(false)
        } catch {
            toast.error('Failed to update memory')
        }
    }

    const handleCancel = () => {
        setEditContent(memory.content)
        setEditFactKey(memory.factKey ?? '')
        setEditing(false)
    }

    const confidencePercent = Math.round((memory.confidence ?? 1) * 100)

    return (
        <div
            className={`flex items-start gap-3 p-3 rounded-md border group transition-colors ${isInactive
                ? 'bg-brand-main-900/20 border-brand-main-800/30 opacity-60'
                : 'bg-brand-main-800/30 border-brand-main-700/40 hover:border-brand-main-600/60'
                }`}
        >
            <div className="flex-1 min-w-0">
                {/* Badges row */}
                <div className="flex items-center gap-1.5 mb-1.5 flex-wrap">
                    <Badge variant="outline" className={`text-[10px] px-1.5 py-0 ${typeClass}`}>
                        {typeLabel}
                    </Badge>
                    <Badge variant="outline" className={`text-[10px] px-1.5 py-0 ${scopeClass}`}>
                        {scopeLabel}
                    </Badge>
                    {memory.factKey && (
                        <span className="text-[10px] text-zinc-500 font-mono">{memory.factKey}</span>
                    )}
                    {isSuperseded && (
                        <span className="text-[10px] text-amber-500/70 italic">superseded</span>
                    )}
                    {isInactive && !isSuperseded && (
                        <span className="text-[10px] text-zinc-600 italic">inactive</span>
                    )}
                </div>

                {/* Content — editable */}
                {editing ? (
                    <div className="space-y-2">
                        {memory.memoryType === 'fact' && (
                            <Input
                                value={editFactKey}
                                onChange={(e) => setEditFactKey(e.target.value)}
                                placeholder="fact.key"
                                className="h-7 text-xs bg-brand-main-900 border-brand-main-600 font-mono"
                            />
                        )}
                        <Textarea
                            value={editContent}
                            onChange={(e) => setEditContent(e.target.value)}
                            className="text-sm bg-brand-main-900 border-brand-main-600 min-h-[60px]"
                        />
                        <div className="flex gap-1">
                            <Button
                                size="sm"
                                variant="outline"
                                onClick={handleSave}
                                disabled={updateMutation.isPending || !editContent.trim()}
                                className="h-6 text-[11px] px-2 gap-1"
                            >
                                <Check className="w-3 h-3" />
                                Save
                            </Button>
                            <Button
                                size="sm"
                                variant="ghost"
                                onClick={handleCancel}
                                className="h-6 text-[11px] px-2 gap-1 text-zinc-500"
                            >
                                <X className="w-3 h-3" />
                                Cancel
                            </Button>
                        </div>
                    </div>
                ) : (
                    <p
                        className={`text-sm text-zinc-300 whitespace-pre-wrap break-words cursor-pointer hover:text-zinc-200 transition-colors ${isSuperseded ? 'line-through text-zinc-500' : ''
                            }`}
                        onClick={() => {
                            if (!isInactive) {
                                setEditing(true)
                            }
                        }}
                    >
                        {memory.content}
                    </p>
                )}

                {/* Meta row */}
                {!editing && (
                    <div className="flex items-center gap-3 mt-1.5 flex-wrap">
                        <span className="text-[10px] text-zinc-600">
                            {memory.source === 'auto_extracted' ? 'auto-extracted' : memory.source}
                            {memory.sourceSessionId && (
                                <> from session</>
                            )}
                        </span>
                        {/* Confidence bar */}
                        <Tooltip content={`Confidence: ${memory.confidence?.toFixed(2)}`}>
                            <div className="flex items-center gap-1">
                                <div className="w-12 h-1 rounded-full bg-brand-main-700/50 overflow-hidden">
                                    <div
                                        className={`h-full rounded-full ${confidencePercent >= 80 ? 'bg-emerald-500/70' :
                                            confidencePercent >= 50 ? 'bg-amber-500/70' : 'bg-red-500/70'
                                            }`}
                                        style={{ width: `${confidencePercent}%` }}
                                    />
                                </div>
                                <span className="text-[10px] text-zinc-600 tabular-nums">{confidencePercent}%</span>
                            </div>
                        </Tooltip>
                        {(memory.accessCount ?? 0) > 0 && (
                            <span className="text-[10px] text-zinc-600 tabular-nums">
                                accessed {memory.accessCount}x
                            </span>
                        )}
                    </div>
                )}
            </div>

            {/* Action buttons */}
            {!editing && (
                <div className="flex items-center gap-0.5 opacity-0 group-hover:opacity-100 transition-opacity shrink-0">
                    <button
                        onClick={() => {
                            if (!isInactive) setEditing(true)
                        }}
                        className="text-zinc-500 hover:text-zinc-300 p-1 disabled:opacity-30"
                        disabled={isInactive}
                        title="Edit"
                    >
                        <Pencil className="w-3 h-3" />
                    </button>
                    {memory.isActive && (
                        <button
                            onClick={() => onDeactivate(memory.id)}
                            className="text-zinc-500 hover:text-amber-400 p-1"
                            title="Deactivate"
                        >
                            <EyeOff className="w-3 h-3" />
                        </button>
                    )}
                    <button
                        onClick={() => onDelete(memory.id)}
                        className="text-zinc-500 hover:text-red-400 p-1"
                        title="Delete"
                    >
                        <Trash2 className="w-3 h-3" />
                    </button>
                </div>
            )}
        </div>
    )
}

function CreateMemoryDialog({
    agentId,
    open,
    onOpenChange,
}: {
    agentId: string
    open: boolean
    onOpenChange: (open: boolean) => void
}) {
    const [memoryType, setMemoryType] = useState('fact')
    const [content, setContent] = useState('')
    const [factKey, setFactKey] = useState('')
    const [scope, setScope] = useState('agent')
    const createMutation = useCreateAgentMemory()

    const handleSubmit = async () => {
        if (!content.trim()) return
        try {
            await createMutation.mutateAsync({
                agentId,
                memoryType,
                content: content.trim(),
                factKey: factKey.trim() || undefined,
                confidence: 1.0,
                scope,
            })
            toast.success('Memory created')
            setContent('')
            setFactKey('')
            onOpenChange(false)
        } catch {
            toast.error('Failed to create memory')
        }
    }

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="bg-brand-main-900 border-brand-main-600">
                <DialogHeader>
                    <DialogTitle className="text-zinc-200">Add Memory</DialogTitle>
                </DialogHeader>
                <div className="space-y-4">
                    <div className="grid grid-cols-2 gap-3">
                        <div className="space-y-2">
                            <Label className="text-xs">Type</Label>
                            <Select value={memoryType} onValueChange={setMemoryType}>
                                <SelectTrigger className="bg-brand-main-900/60 border-brand-main-600 text-zinc-200">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200">
                                    <SelectItem value="fact">Fact</SelectItem>
                                    <SelectItem value="instruction">Instruction</SelectItem>
                                    <SelectItem value="document">Document</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                        <div className="space-y-2">
                            <Label className="text-xs">Scope</Label>
                            <Select value={scope} onValueChange={setScope}>
                                <SelectTrigger className="bg-brand-main-900/60 border-brand-main-600 text-zinc-200">
                                    <SelectValue />
                                </SelectTrigger>
                                <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200">
                                    <SelectItem value="agent">Agent</SelectItem>
                                    <SelectItem value="user">User</SelectItem>
                                    <SelectItem value="global">Global</SelectItem>
                                </SelectContent>
                            </Select>
                        </div>
                    </div>
                    {memoryType === 'fact' && (
                        <div className="space-y-2">
                            <Label className="text-xs">Fact Key</Label>
                            <Input
                                value={factKey}
                                onChange={(e) => setFactKey(e.target.value)}
                                placeholder="e.g. user.name, project.language"
                                className="bg-brand-main-900 border-brand-main-600 font-mono text-sm"
                            />
                        </div>
                    )}
                    <div className="space-y-2">
                        <Label className="text-xs">Content</Label>
                        <Textarea
                            value={content}
                            onChange={(e) => setContent(e.target.value)}
                            placeholder="Enter the memory content..."
                            className="bg-brand-main-900 border-brand-main-600 min-h-[100px]"
                        />
                    </div>
                </div>
                <DialogFooter>
                    <Button variant="outline" onClick={() => onOpenChange(false)} className="border-brand-main-600">
                        Cancel
                    </Button>
                    <Button onClick={handleSubmit} disabled={!content.trim() || createMutation.isPending}>
                        {createMutation.isPending ? 'Creating...' : 'Create'}
                    </Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    )
}
