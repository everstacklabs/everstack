import { useMemo, useState } from 'react'
import { useAgents, useDeleteAgent, useUpdateAgent } from '@/hooks/deployments/use-agents'
import { useTerminateSandbox } from '@/hooks/deployments/use-sandbox'
import { AgentMode, AgentLifecycleMode, AgentLifecycleStatus, type AgentDefinition } from '@/server/agents'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Trash2, Pencil, ui } from '@everstack/ui'
import { Loader, toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { formatTimestamp } from '@everstack/utils/functions/index'
import { useSearch, useNavigate } from '@tanstack/react-router'
import { Bot, EyeOff, XCircle } from 'lucide-react'

const {
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    Button,
    Switch,
    Select,
    SelectTrigger,
    SelectValue,
    SelectContent,
    SelectItem,
} = ui

interface AgentListProps {
    onEdit?: (agent: AgentDefinition) => void
    lifecycleMode?: 'ephemeral' | 'persistent'
}

export function AgentList({ onEdit, lifecycleMode }: AgentListProps) {
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const [terminateTarget, setTerminateTarget] = useState<AgentDefinition | null>(null)
    const [includeHidden, setIncludeHidden] = useState(false)
    const [modeFilter, setModeFilter] = useState<'all' | 'primary' | 'subagent'>('all')
    const deleteAgentMutation = useDeleteAgent()
    const updateAgentMutation = useUpdateAgent()
    const terminateSandboxMutation = useTerminateSandbox()
    const listAgentsQuery = useAgents({
        includeHidden,
        mode:
            modeFilter === 'primary'
                ? AgentMode.PRIMARY
                : modeFilter === 'subagent'
                    ? AgentMode.SUBAGENT
                    : undefined,
        lifecycleMode,
    })
    const navigate = useNavigate()

    const search = useSearch({ strict: false })
    const sourceAgents = listAgentsQuery.data ?? []

    const filteredAgents = useMemo(() => {
        let filtered = [...sourceAgents]

        const searchTerm = (search as Record<string, unknown>)?.search as string | undefined
        if (searchTerm) {
            const term = searchTerm.toLowerCase()
            filtered = filtered.filter(a =>
                a.name.toLowerCase().includes(term) ||
                (a.description ?? '').toLowerCase().includes(term) ||
                a.model.toLowerCase().includes(term)
            )
        }

        filtered.sort((a, b) => {
            const aTime = a.createdAt?.seconds ? (typeof a.createdAt.seconds === 'bigint' ? Number(a.createdAt.seconds) : Number(a.createdAt.seconds)) : 0
            const bTime = b.createdAt?.seconds ? (typeof b.createdAt.seconds === 'bigint' ? Number(b.createdAt.seconds) : Number(b.createdAt.seconds)) : 0
            return bTime - aTime
        })

        return filtered
    }, [sourceAgents, search])

    if (listAgentsQuery.isLoading) {
        return (
            <div className="flex-1 flex items-center justify-center text-white/70 light:text-black/70">
                <Loader loaderText="Loading agents..." />
            </div>
        )
    }

    if (listAgentsQuery.error) {
        return (
            <div className="flex-1 flex items-center justify-center text-red-400 light:text-red-600">
                Error loading agents: {listAgentsQuery.error.message}
            </div>
        )
    }

    const handleDelete = async (id: string) => {
        try {
            await deleteAgentMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('Agent deleted successfully')
            listAgentsQuery.refetch()
        } catch {
            toast.error('Failed to delete agent')
        }
    }

    const handleToggleEnabled = async (agent: AgentDefinition) => {
        try {
            await updateAgentMutation.mutateAsync({
                id: agent.id,
                enabled: !agent.enabled,
            })
            toast.success(`Agent ${agent.enabled ? 'disabled' : 'enabled'} successfully`)
            listAgentsQuery.refetch()
        } catch {
            toast.error('Failed to update agent')
        }
    }

    const columns: ColumnConfig<AgentDefinition>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 260,
            minWidth: 140,
            render: (agent: AgentDefinition) => (
                <div className="flex min-w-0 overflow-hidden items-center gap-2">
                    <span
                        className="h-2.5 w-2.5 shrink-0 rounded-full border border-white/20 light:border-black/20"
                        style={{ backgroundColor: agent.color || '#64748b' }}
                    />
                    <span className="truncate font-medium text-brand-secondary-100 text-xs">
                        {agent.name}
                    </span>
                    <span
                        className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${agent.mode === AgentMode.SUBAGENT
                                ? 'bg-amber-500/15 text-amber-300 light:text-amber-700'
                                : 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600'
                            }`}
                    >
                        {agent.mode === AgentMode.SUBAGENT ? (
                            <span className="inline-flex items-center gap-1">
                                <Bot className="h-3 w-3" />
                                Subagent
                            </span>
                        ) : 'Primary'}
                    </span>
                    {agent.hidden && (
                        <span className="inline-flex shrink-0 items-center gap-1 rounded px-1.5 py-0.5 text-[10px] font-medium bg-brand-main-700/50 text-brand-main-200">
                            <EyeOff className="h-3 w-3" />
                            Hidden
                        </span>
                    )}
                </div>
            ),
        },
        {
            id: 'status',
            header: 'Status',
            width: 120,
            minWidth: 100,
            render: (agent: AgentDefinition) => (
                <div className="flex items-center gap-1.5">
                    {agent.lifecycleMode === AgentLifecycleMode.PERSISTENT ? (
                        agent.lifecycleStatus > 0 ? (
                            <LifecycleStatusPill status={agent.lifecycleStatus} />
                        ) : (
                            <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-blue-500/15 text-blue-300 light:text-blue-600">
                                Persistent
                            </span>
                        )
                    ) : (
                        <span className="text-xs text-brand-main-300">Ephemeral</span>
                    )}
                </div>
            ),
        },
        {
            id: 'description',
            header: 'Description',
            width: 240,
            minWidth: 120,
            render: (agent: AgentDefinition) => (
                <span className="truncate text-xs text-brand-main-100">{agent.description || '-'}</span>
            ),
        },
        {
            id: 'tools',
            header: 'Tools',
            width: 80,
            minWidth: 60,
            render: (agent: AgentDefinition) => (
                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-purple-500/20 text-purple-300 light:text-purple-600">
                    {agent.tools?.length ?? 0}
                </span>
            ),
        },
        {
            id: 'model',
            header: 'Model',
            width: 180,
            minWidth: 120,
            render: (agent: AgentDefinition) => (
                <span className="truncate text-xs text-brand-main-100 font-mono">{agent.model}</span>
            ),
        },
        {
            id: 'createdAt',
            header: 'Created',
            width: 160,
            minWidth: 140,
            render: (agent: AgentDefinition) => (
                <span className="truncate text-xs text-brand-main-100">{formatTimestamp(agent.createdAt)}</span>
            ),
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (agent: AgentDefinition) => (
                <div data-row-actions>
                    <Switch
                        checked={agent.enabled}
                        onCheckedChange={() => handleToggleEnabled(agent)}
                        disabled={updateAgentMutation.isPending}
                    />
                </div>
            ),
        },
        {
            id: 'actions',
            header: '',
            width: 110,
            minWidth: 110,
            maxWidth: 110,
            resizable: false,
            render: (agent: AgentDefinition) => {
                // Terminate is only meaningful for persistent agents that own a
                // live sandbox. For ephemeral agents (no sandbox) the action is
                // a no-op; for already-terminated agents there's nothing to stop.
                const canTerminate =
                    agent.lifecycleMode === AgentLifecycleMode.PERSISTENT &&
                    !!agent.sandboxId &&
                    agent.lifecycleStatus !== AgentLifecycleStatus.TERMINATED &&
                    agent.lifecycleStatus !== AgentLifecycleStatus.FAILED &&
                    agent.lifecycleStatus !== AgentLifecycleStatus.CREATED
                return (
                    <div className="flex items-center gap-2 justify-center" data-row-actions>
                        <button
                            type="button"
                            className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                            onClick={() => onEdit?.(agent)}
                            title="Edit agent"
                        >
                            <Pencil size={14} />
                        </button>
                        {canTerminate && (
                            <button
                                type="button"
                                className="p-1 rounded text-white/55 light:text-black/55 hover:bg-red-500/15 hover:text-red-400 light:hover:text-red-600 transition-colors"
                                onClick={() => setTerminateTarget(agent)}
                                title="Terminate agent (stops the sandbox)"
                            >
                                <XCircle size={14} />
                            </button>
                        )}
                        <button
                            type="button"
                            className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                            onClick={() => {
                                setDeleteConfirmId(agent.id)
                                setDeleteConfirmName(agent.name)
                            }}
                            title="Delete agent"
                        >
                            <Trash2 size={14} />
                        </button>
                    </div>
                )
            },
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <div className="shrink-0 flex items-center justify-between gap-3 px-3 py-2 border-b border-brand-main-800/40 bg-brand-main-900/20">
                <div className="flex items-center gap-2">
                    <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">Mode</span>
                    <Select value={modeFilter} onValueChange={(value) => setModeFilter(value as 'all' | 'primary' | 'subagent')}>
                        <SelectTrigger className="h-8 w-[150px] bg-brand-main-900/60 border-brand-main-700 text-xs text-zinc-200">
                            <SelectValue />
                        </SelectTrigger>
                        <SelectContent className="bg-brand-main-900 border-brand-main-600 text-zinc-200">
                            <SelectItem value="all">All agents</SelectItem>
                            <SelectItem value="primary">Primary only</SelectItem>
                            <SelectItem value="subagent">Subagents only</SelectItem>
                        </SelectContent>
                    </Select>
                </div>
                <div className="flex items-center gap-2">
                    <span className="text-xs text-white/60 light:text-black/60">Include hidden</span>
                    <Switch checked={includeHidden} onCheckedChange={setIncludeHidden} />
                </div>
            </div>
            <ResponsiveTable
                columns={columns}
                data={filteredAgents}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={sourceAgents.length === 0 ? (
                    <div className="flex flex-col items-center justify-center">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon
                                    icon="ri:apps-ai-line"
                                    className="size-8 text-brand-secondary-400"
                                />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white mb-2 light:text-brand-main-50">
                            No agents yet
                        </h3>
                        <p className="text-sm text-white/50 max-w-sm text-center leading-relaxed light:text-black/50">
                            Create your first agent to get started. Agents can be configured with tools, skills, and custom behavior.
                        </p>
                    </div>
                ) :'No agents match your search.'}
                onRowClick={(agent) => navigate({ to: '/deployments/agents/$agentId', params: { agentId: agent.id } })}
                rowKey={(agent) => agent.id}
            />

            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete Agent</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? This action cannot be undone and all sessions for this agent will be orphaned.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteAgentMutation.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteAgentMutation.isPending}
                        >
                            {deleteAgentMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <AlertDialog
                open={terminateTarget !== null}
                onOpenChange={(open) => {
                    if (!open && !terminateSandboxMutation.isPending) setTerminateTarget(null)
                }}
            >
                <AlertDialogContent className="bg-brand-main-900 border border-brand-main-700 text-brand-main-100 p-0 gap-0 sm:max-w-md shadow-2xl shadow-black/60">
                    <AlertDialogHeader className="p-5 pb-4 sm:text-left">
                        <div className="flex items-start gap-3">
                            <div className="shrink-0 size-9 rounded-md bg-red-500/10 border border-red-500/25 flex items-center justify-center">
                                <XCircle size={18} className="text-red-400" />
                            </div>
                            <div className="flex-1 min-w-0">
                                <AlertDialogTitle className="text-white text-[15px] font-semibold leading-tight light:text-brand-main-50">
                                    Terminate agent?
                                </AlertDialogTitle>
                                <AlertDialogDescription className="mt-1.5 text-sm text-white/55 leading-relaxed light:text-black/55">
                                    Stops{' '}
                                    <span className="font-mono text-xs px-1.5 py-0.5 rounded bg-brand-main-800 text-brand-secondary-300 border border-brand-main-700">
                                        {terminateTarget?.name}
                                    </span>{' '}
                                    and releases its sandbox compute back to the host.
                                </AlertDialogDescription>
                            </div>
                        </div>
                    </AlertDialogHeader>
                    <AlertDialogFooter className="px-5 py-3 border-t border-brand-main-700 bg-brand-main-950/40 sm:justify-end gap-2">
                        <AlertDialogCancel
                            disabled={terminateSandboxMutation.isPending}
                            className="mt-0 h-8 px-3.5 text-sm bg-transparent border border-brand-main-600 text-white/75 hover:bg-brand-main-800 hover:text-white light:text-black/75 light:hover:text-brand-main-50"
                        >
                            Cancel
                        </AlertDialogCancel>
                        <AlertDialogAction
                            disabled={terminateSandboxMutation.isPending || !terminateTarget?.sandboxId}
                            onClick={(e) => {
                                e.preventDefault()
                                if (!terminateTarget?.sandboxId) return
                                terminateSandboxMutation.mutate(terminateTarget.sandboxId, {
                                    onSuccess: () => {
                                        toast.success('Agent and sandbox terminated')
                                        setTerminateTarget(null)
                                        listAgentsQuery.refetch()
                                    },
                                    onError: (err) => toast.error(`Failed to terminate: ${err.message}`),
                                })
                            }}
                            className="h-8 px-3.5 text-sm bg-red-500/90 hover:bg-red-500 text-white border-0 inline-flex items-center gap-1.5 shadow-sm shadow-red-900/30"
                        >
                            <XCircle size={14} />
                            {terminateSandboxMutation.isPending ? 'Terminating' : 'Terminate'}
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </div>
    )
}

const LIFECYCLE_STATUS_PILL_META: Record<number, { label: string; colors: string; pulse?: boolean }> = {
    [AgentLifecycleStatus.CREATED]: { label: 'Created', colors: 'bg-gray-500/20 text-gray-400' },
    [AgentLifecycleStatus.PROVISIONING]: { label: 'Provisioning', colors: 'bg-blue-500/20 text-blue-300' },
    [AgentLifecycleStatus.RUNNING]: { label: 'Running', colors: 'bg-green-500/20 text-green-300', pulse: true },
    [AgentLifecycleStatus.IDLE]: { label: 'Idle', colors: 'bg-amber-500/20 text-amber-300' },
    [AgentLifecycleStatus.SLEEPING]: { label: 'Sleeping', colors: 'bg-gray-500/20 text-gray-400' },
    [AgentLifecycleStatus.WAKING]: { label: 'Waking', colors: 'bg-yellow-500/20 text-yellow-300' },
    [AgentLifecycleStatus.FAILED]: { label: 'Failed', colors: 'bg-red-500/20 text-red-300' },
    [AgentLifecycleStatus.TERMINATED]: { label: 'Terminated', colors: 'bg-gray-500/20 text-gray-400' },
}

function LifecycleStatusPill({ status }: { status: AgentLifecycleStatus }) {
    const meta = LIFECYCLE_STATUS_PILL_META[status] ?? LIFECYCLE_STATUS_PILL_META[AgentLifecycleStatus.CREATED]
    return (
        <span className={`inline-flex items-center gap-1 shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${meta.colors}`}>
            {meta.pulse && <span className="h-1.5 w-1.5 rounded-full bg-green-400 animate-pulse" />}
            {meta.label}
        </span>
    )
}
