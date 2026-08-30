import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { A2aPublishTable } from '@/components/gateway/a2a/a2a-publish-table'
import { RemoteAgents } from '@/components/gateway/a2a/remote-agents'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

const a2aSearchSchema = z.object({
    tab: z.enum(['publish', 'remotes']).optional().default('publish'),
})

export const Route = createFileRoute('/gateway/a2a')({
    component: A2aPage,
    validateSearch: a2aSearchSchema,
})

function A2aPage() {
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()

    return (
        <div className="flex h-full w-full flex-col">
            <Tabs
                value={tab}
                onValueChange={(value) => navigate({ search: { tab: value as 'publish' | 'remotes' } })}
                className="flex flex-1 flex-col overflow-hidden"
            >
                <div className="px-3 pt-2">
                    <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        <TabsTrigger
                            className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                            value="publish"
                        >
                            Publish agents
                        </TabsTrigger>
                        <TabsTrigger
                            className="relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1"
                            value="remotes"
                        >
                            Remote agents
                        </TabsTrigger>
                    </TabsList>
                </div>
                <div className="flex-1 overflow-auto">
                    <TabsContent value="publish">
                        <A2aPublishTable />
                    </TabsContent>
                    <TabsContent value="remotes">
                        <RemoteAgents />
                    </TabsContent>
                </div>
            </Tabs>
        </div>
    )
}
