import { useMemo } from 'react'
import { useFederatedTools, useMcpServers } from '@/hooks/gateway/use-mcp'
import type { McpTool } from '@/server/mcp'
import { ResponsiveTable, type ColumnConfig } from '@/ui/table'
import { Iconify } from '@everstack/ui/icons'
import { useSearch } from '@tanstack/react-router'

export function McpToolCatalog() {
    const { data: tools } = useFederatedTools()
    const { data: servers } = useMcpServers()

    const search = useSearch({ strict: false })

    const serverMap = useMemo(
        () => new Map((servers ?? []).map((s) => [s.id, s.name])),
        [servers]
    )

    const sourceTools = tools ?? []

    const filteredTools = useMemo(() => {
        let filtered = [...sourceTools]

        const searchTerm = (search as Record<string, unknown>)?.search as string | undefined
        if (searchTerm) {
            const term = searchTerm.toLowerCase()
            filtered = filtered.filter(t =>
                t.name.toLowerCase().includes(term) ||
                t.description.toLowerCase().includes(term) ||
                (serverMap.get(t.serverId) ?? '').toLowerCase().includes(term)
            )
        }

        return filtered
    }, [sourceTools, search, serverMap])

    const columns: ColumnConfig<McpTool>[] = [
        {
            id: 'name',
            header: 'Tool',
            width: 240,
            minWidth: 140,
            render: (tool: McpTool) => (
                <span className="truncate font-medium text-brand-secondary-100 text-xs font-mono">
                    {tool.name}
                </span>
            ),
        },
        {
            id: 'description',
            header: 'Description',
            width: 320,
            minWidth: 120,
            render: (tool: McpTool) => (
                <span className="truncate text-xs text-brand-main-100">
                    {tool.description || '-'}
                </span>
            ),
        },
        {
            id: 'server',
            header: 'Server',
            width: 180,
            minWidth: 100,
            render: (tool: McpTool) => (
                <span className="shrink-0 rounded px-1.5 py-0.5 text-[10px] font-medium bg-blue-500/15 text-blue-300 light:text-blue-600">
                    {serverMap.get(tool.serverId) ?? tool.serverId}
                </span>
            ),
        },
        {
            id: 'schema',
            header: 'Schema',
            width: 90,
            minWidth: 70,
            render: (tool: McpTool) => (
                <span className="text-xs text-brand-main-100">
                    {tool.inputSchema ? `${Object.keys(tool.inputSchema.properties ?? tool.inputSchema).length} params` : '-'}
                </span>
            ),
        },
    ]

    return (
        <div className="flex-1 min-h-0 w-full h-full overflow-hidden flex flex-col">
            <ResponsiveTable
                columns={columns}
                data={filteredTools}
                enableResizing={true}
                minTableWidth="100%"
                emptyMessage={sourceTools.length === 0 ? (
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
                            No tools discovered yet
                        </h3>
                        <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                            Tools are discovered automatically when MCP servers are registered and connected.
                        </p>
                    </div>
                ) : 'No tools match your search.'}
                rowKey={(tool) => `${tool.serverId}-${tool.name}`}
            />
        </div>
    )
}
