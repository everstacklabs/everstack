import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import { OverviewTab, InstancesTab, NetworkTab, SnapshotsTab, VolumesTab, SandboxProvider } from '@/components/deployments/sandbox'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

// Inventory-first (Daytona-style). The root holds the sandbox LIST plus
// the account/fleet-level surfaces (overview tiles, gateway network,
// the snapshot + volume catalogs). Everything PER-SANDBOX — terminal,
// files, processes, logs, metrics, ports, events, crons, webhooks,
// settings — lives on the sandbox detail page
// (/deployments/sandboxes/$sandboxId), reached by clicking a row.
const sandboxSearchSchema = z.object({
    tab: z.enum(['instances', 'overview', 'network', 'snapshots', 'volumes']).optional().default('instances'),
})

export const Route = createFileRoute('/deployments/sandboxes/')({
    component: SandboxPage,
    validateSearch: sandboxSearchSchema,
})

const TAB_TRIGGER_CLASS =
    'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function SandboxPage() {
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()

    return (
        <div className="flex flex-col h-full w-full">
            <SandboxProvider>
                <Tabs
                    value={tab}
                    onValueChange={(value) => navigate({ search: (prev) => ({ ...prev, tab: value as typeof tab }) })}
                    className="flex-1 flex flex-col overflow-hidden"
                >
                    <div className="px-3 pt-2">
                        <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="instances">Instances</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="overview">Overview</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="network">Network</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="snapshots">Snapshots</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="volumes">Volumes</TabsTrigger>
                        </TabsList>
                    </div>

                    <div className="flex-1 overflow-hidden">
                        <TabsContent value="instances" className="h-full overflow-hidden flex flex-col">
                            <InstancesTab />
                        </TabsContent>
                        <TabsContent value="overview" className="h-full overflow-hidden flex flex-col">
                            <OverviewTab />
                        </TabsContent>
                        <TabsContent value="network" className="h-full overflow-hidden flex flex-col">
                            <NetworkTab />
                        </TabsContent>
                        <TabsContent value="snapshots" className="h-full overflow-hidden flex flex-col">
                            <SnapshotsTab />
                        </TabsContent>
                        <TabsContent value="volumes" className="h-full overflow-hidden flex flex-col">
                            <VolumesTab />
                        </TabsContent>
                    </div>
                </Tabs>
            </SandboxProvider>
        </div>
    )
}
