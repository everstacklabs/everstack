import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { OverviewTab, CollectionsTab, QueryTab } from '@/components/deployments/memory'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

const memorySearchSchema = z.object({
    tab: z.enum(['overview', 'collections', 'query']).optional().default('overview'),
})

export const Route = createFileRoute('/deployments/memory/')({
    component: MemoryIndexPage,
    validateSearch: memorySearchSchema,
})

function MemoryIndexPage() {
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()

    return (
        <div className="flex flex-col h-full w-full">
            <Tabs
                value={tab}
                onValueChange={(value) => navigate({ search: (prev) => ({ ...prev, tab: value as typeof tab }) })}
                className="flex-1 flex flex-col overflow-hidden"
            >
                <div className="px-3 pt-2">
                    <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="overview">Overview</TabsTrigger>
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="collections">Collections</TabsTrigger>
                        <TabsTrigger className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1" value="query">Query</TabsTrigger>
                    </TabsList>
                </div>

                <div className="flex-1 overflow-hidden">
                    <TabsContent value="overview" className="h-full overflow-hidden flex flex-col">
                        <OverviewTab />
                    </TabsContent>
                    <TabsContent value="collections" className="h-full overflow-hidden flex flex-col">
                        <CollectionsTab />
                    </TabsContent>
                    <TabsContent value="query" className="h-full overflow-hidden flex flex-col">
                        <QueryTab />
                    </TabsContent>
                </div>
            </Tabs>
        </div>
    )
}
