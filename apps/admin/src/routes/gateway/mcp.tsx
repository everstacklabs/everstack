import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { McpServersList } from '@/components/gateway/mcp/mcp-servers-list'
import { McpToolCatalog } from '@/components/gateway/mcp/mcp-tool-catalog'
import { McpPublishPanel } from '@/components/gateway/mcp/mcp-publish-panel'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

const mcpSearchSchema = z.object({
    tab: z.enum(['servers', 'tools', 'publish']).optional().default('servers'),
    search: z.string().optional(),
})

type McpTab = 'servers' | 'tools' | 'publish'

export const Route = createFileRoute('/gateway/mcp')({
    component: McpGatewayPage,
    validateSearch: mcpSearchSchema,
})

function McpGatewayPage() {
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()

    return (
        <div className="flex flex-col h-full w-full">
            <Tabs value={tab} onValueChange={(value) => navigate({ search: { tab: value as McpTab } })} className="flex-1 flex flex-col overflow-hidden">
                <div className="px-3 pt-2">
                    <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="servers">Servers</TabsTrigger>
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="tools">Tools</TabsTrigger>
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="publish">Publish</TabsTrigger>
                    </TabsList>
                </div>

                <div className="flex-1 overflow-hidden">
                    <TabsContent value="servers" className="h-full overflow-hidden flex flex-col">
                        <McpServersList />
                    </TabsContent>
                    <TabsContent value="tools" className="h-full overflow-hidden flex flex-col">
                        <McpToolCatalog />
                    </TabsContent>
                    <TabsContent value="publish" className="h-full overflow-auto flex flex-col">
                        <McpPublishPanel />
                    </TabsContent>
                </div>
            </Tabs>
        </div>
    )
}
