import { useMemo, useState } from 'react'
import { useMcpServers, useDeleteMcpServer, useUpdateMcpServer, useDiscoverTools } from '@/hooks/gateway/use-mcp'
import type { McpServer } from '@/server/mcp'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Trash2, Pencil, RefreshCw, ui } from '@everstack/ui'
import { toast } from '@everstack/ui/components'
import { Iconify } from '@everstack/ui/icons'
import { useSearch } from '@tanstack/react-router'
import { EditMcpServerDialog } from './edit-mcp-server-dialog'

const {
    Dialog,
    DialogContent,
    DialogTitle,
    DialogDescription,
    Button,
    Switch,
} = ui

function transportLabel(type: string) {
    switch (type) {
        case 'MCP_TRANSPORT_TYPE_SSE': return 'SSE'
        case 'MCP_TRANSPORT_TYPE_STREAMABLE_HTTP': return 'Streamable HTTP'
        case 'MCP_TRANSPORT_TYPE_STDIO': return 'Stdio'
        default: return type
    }
}

function healthLabel(status: string) {
    switch (status) {
        case 'MCP_SERVER_HEALTH_STATUS_HEALTHY': return 'Healthy'
        case 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY': return 'Unhealthy'
        default: return 'Unknown'
    }
}

function healthColor(status: string) {
    switch (status) {
        case 'MCP_SERVER_HEALTH_STATUS_HEALTHY': return '#22c55e'
        case 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY': return '#ef4444'
        default: return '#64748b'
    }
}

export function McpServersList() {
    const [deleteConfirmId, setDeleteConfirmId] = useState<string | null>(null)
    const [deleteConfirmName, setDeleteConfirmName] = useState<string>('')
    const [editServer, setEditServer] = useState<McpServer | null>(null)
    const { data: servers } = useMcpServers()
    const deleteMutation = useDeleteMcpServer()
    const updateMutation = useUpdateMcpServer()
    const discoverMutation = useDiscoverTools()

    const search = useSearch({ strict: false })
    const sourceServers = servers ?? []

    const filteredServers = useMemo(() => {
        let filtered = [...sourceServers]

        const searchTerm = (search as Record<string, unknown>)?.search as string | undefined
        if (searchTerm) {
            const term = searchTerm.toLowerCase()
            filtered = filtered.filter(s =>
                s.name.toLowerCase().includes(term) ||
                s.url.toLowerCase().includes(term) ||
                transportLabel(s.transportType).toLowerCase().includes(term)
            )
        }

        return filtered
    }, [sourceServers, search])

    const handleDelete = async (id: string) => {
        try {
            await deleteMutation.mutateAsync(id)
            setDeleteConfirmId(null)
            setDeleteConfirmName('')
            toast.success('MCP server deleted successfully')
        } catch {
            toast.error('Failed to delete MCP server')
        }
    }

    const handleToggleEnabled = async (server: McpServer) => {
        try {
            await updateMutation.mutateAsync({
                id: server.id,
                enabled: !server.enabled,
            })
            toast.success(`Server ${server.enabled ? 'disabled' : 'enabled'} successfully`)
        } catch {
            toast.error('Failed to update server')
        }
    }

    const handleRefreshTools = async (server: McpServer) => {
        try {
            const tools = await discoverMutation.mutateAsync(server.id)
            toast.success(`Discovered ${tools.length} tools from "${server.name}"`)
        } catch (err) {
            toast.error(`Failed to discover tools: ${(err as Error).message}`)
        }
    }

    const columns: ColumnConfig<McpServer>[] = [
        {
            id: 'name',
            header: 'Name',
            width: 220,
            minWidth: 140,
            render: (server: McpServer) => (
                <div className="flex min-w-0 items-center gap-2">
                    <span
                        className="h-2.5 w-2.5 shrink-0 rounded-full border border-white/20 light:border-black/20"
                        style={{ backgroundColor: healthColor(server.healthStatus) }}
                    />
                    <span className="truncate font-medium text-brand-secondary-100 text-xs">
                        {server.name}
                    </span>
                </div>
            ),
        },
        {
            id: 'url',
            header: 'Endpoint',
            width: 260,
            minWidth: 120,
            render: (server: McpServer) => (
                <span className="truncate text-xs text-brand-main-100 font-mono">
                    {server.url || server.stdioConfig?.command || '-'}
                </span>
            ),
        },
        {
            id: 'transport',
            header: 'Transport',
            width: 140,
            minWidth: 100,
            render: (server: McpServer) => (
                <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-blue-500/15 text-blue-300 light:text-blue-600">
                    {transportLabel(server.transportType)}
                </span>
            ),
        },
        {
            id: 'tools',
            header: 'Tools',
            width: 80,
            minWidth: 60,
            render: (server: McpServer) => (
                <span className="px-1.5 py-0.5 rounded text-[10px] font-medium bg-purple-500/20 text-purple-300 light:text-purple-600">
                    {server.toolCount}
                </span>
            ),
        },
        {
            id: 'health',
            header: 'Health',
            width: 110,
            minWidth: 90,
            render: (server: McpServer) => {
                const label = healthLabel(server.healthStatus)
                const isHealthy = server.healthStatus === 'MCP_SERVER_HEALTH_STATUS_HEALTHY'
                const isUnhealthy = server.healthStatus === 'MCP_SERVER_HEALTH_STATUS_UNHEALTHY'
                return (
                    <span
                        className={`shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium ${
                            isHealthy
                                ? 'bg-emerald-500/15 text-emerald-300 light:text-emerald-600'
                                : isUnhealthy
                                    ? 'bg-red-500/15 text-red-300 light:text-red-600'
                                    : 'bg-brand-main-700/50 text-brand-main-200'
                        }`}
                    >
                        {label}
                    </span>
                )
            },
        },
        {
            id: 'enabled',
            header: 'Enabled',
            width: 80,
            minWidth: 80,
            render: (server: McpServer) => (
                <div data-row-actions>
                    <Switch
                        checked={server.enabled}
                        onCheckedChange={() => handleToggleEnabled(server)}
                        disabled={updateMutation.isPending}
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
            render: (server: McpServer) => (
                <div className="flex items-center gap-2 justify-center" data-row-actions>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                        onClick={() => handleRefreshTools(server)}
                        disabled={discoverMutation.isPending}
                        title="Refresh tools"
                    >
                        <RefreshCw size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-blue-500/20 hover:text-blue-400 light:hover:text-blue-600 transition-colors"
                        onClick={() => setEditServer(server)}
                        title="Edit server"
                    >
                        <Pencil size={14} />
                    </button>
                    <button
                        type="button"
                        className="p-1 rounded hover:bg-red-500/20 hover:text-red-400 light:hover:text-red-600 transition-colors"
                        onClick={() => {
                            setDeleteConfirmId(server.id)
                            setDeleteConfirmName(server.name)
                        }}
                        title="Delete server"
                    >
                        <Trash2 size={14} />
                    </button>
                </div>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={filteredServers}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={sourceServers.length === 0 ? (
                    <div className="flex flex-col items-center justify-center">
                        <div className="relative mb-6">
                            <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                            <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                                <Iconify.Icon
                                    icon="octicon:mcp-16"
                                    className="size-8 text-brand-secondary-400"
                                />
                            </div>
                        </div>
                        <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">
                            No MCP servers registered
                        </h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Register an MCP server to expose its tools through the gateway.
                        </p>
                    </div>
                ) : 'No servers match your search.'}
                rowKey={(server) => server.id}
            />

            <Dialog open={deleteConfirmId !== null} onOpenChange={(open) => !open && setDeleteConfirmId(null)}>
                <DialogContent className="w-[500px]">
                    <DialogTitle>Delete MCP Server</DialogTitle>
                    <DialogDescription className="text-brand-main-100">
                        Are you sure you want to delete <strong className="text-brand-main-100">{deleteConfirmName}</strong>? This will remove all discovered tools from this server.
                    </DialogDescription>
                    <div className="flex justify-end gap-3 mt-4">
                        <Button variant="outline" onClick={() => setDeleteConfirmId(null)} disabled={deleteMutation.isPending}>
                            Cancel
                        </Button>
                        <Button
                            variant="destructive"
                            className="bg-destructive/60 text-brand-main-100 hover:bg-destructive/90"
                            onClick={() => deleteConfirmId && handleDelete(deleteConfirmId)}
                            disabled={deleteMutation.isPending}
                        >
                            {deleteMutation.isPending ? 'Deleting...' : 'Delete'}
                        </Button>
                    </div>
                </DialogContent>
            </Dialog>

            <EditMcpServerDialog
                server={editServer}
                open={editServer !== null}
                onOpenChange={(open) => !open && setEditServer(null)}
            />
        </div>
    )
}
