import { createFileRoute } from '@tanstack/react-router'
import { z } from 'zod'
import { ui } from '@everstack/ui'
import {
    ShellTab,
    LogsTab,
    MetricsTab,
    EventsTab,
    PortsTab,
    CronsTab,
    WebhooksTab,
    FilesTab,
    ProcessesTab,
    SettingsTab,
    DetailOverview,
    SandboxProvider,
} from '@/components/deployments/sandbox'

const { Tabs, TabsContent, TabsList, TabsTrigger } = ui

// Sandbox detail page (Daytona-style): one sandbox, everything about
// it. The provider is PINNED to the route's sandbox id, so the reused
// tab components (terminal, logs, metrics, ports, crons, webhooks)
// operate on exactly this sandbox and their internal pickers render as
// static labels.

const detailSearchSchema = z.object({
    tab: z
        .enum(['overview', 'terminal', 'files', 'processes', 'logs', 'metrics', 'ports', 'events', 'crons', 'webhooks', 'settings'])
        .optional()
        .default('overview'),
})

export const Route = createFileRoute('/deployments/sandboxes/$sandboxId')({
    component: SandboxDetailPage,
    validateSearch: detailSearchSchema,
})

const TAB_TRIGGER_CLASS =
    'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

function SandboxDetailPage() {
    const { sandboxId } = Route.useParams()
    const { tab } = Route.useSearch()
    const navigate = Route.useNavigate()

    return (
        <div className="flex flex-col h-full w-full">
            <SandboxProvider pinned initialSandboxId={sandboxId}>
                {/* The breadcrumb + lifecycle actions live in the topbar
                    (topbar/routes/deployments/sandboxes.tsx), not here. */}
                <Tabs
                    value={tab}
                    onValueChange={(value) => navigate({ search: { tab: value as typeof tab } })}
                    className="flex-1 flex flex-col overflow-hidden"
                >
                    <div className="px-3 pt-2">
                        <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="overview">Overview</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="terminal">Terminal</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="files">Files</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="processes">Processes</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="logs">Logs</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="metrics">Metrics</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="ports">Ports</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="events">Events</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="crons">Crons</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="webhooks">Webhooks</TabsTrigger>
                            <TabsTrigger className={TAB_TRIGGER_CLASS} value="settings">Settings</TabsTrigger>
                        </TabsList>
                    </div>

                    <div className="flex-1 overflow-hidden">
                        <TabsContent value="overview" className="h-full overflow-hidden flex flex-col">
                            <DetailOverview />
                        </TabsContent>
                        {/* forceMount keeps the xterm Terminal instance and its
                            WebSocket alive across tab switches; see the index
                            page for the full rationale. */}
                        <TabsContent forceMount value="terminal" className="h-full overflow-hidden flex-col data-[state=active]:flex data-[state=inactive]:hidden">
                            <ShellTab />
                        </TabsContent>
                        <TabsContent value="files" className="h-full overflow-hidden flex flex-col">
                            <FilesTab />
                        </TabsContent>
                        <TabsContent value="processes" className="h-full overflow-hidden flex flex-col">
                            <ProcessesTab />
                        </TabsContent>
                        <TabsContent value="logs" className="h-full overflow-hidden flex flex-col">
                            <LogsTab />
                        </TabsContent>
                        <TabsContent value="metrics" className="h-full overflow-hidden flex flex-col">
                            <MetricsTab />
                        </TabsContent>
                        <TabsContent value="ports" className="h-full overflow-hidden flex flex-col">
                            <PortsTab />
                        </TabsContent>
                        <TabsContent value="events" className="h-full overflow-hidden flex flex-col">
                            <EventsTab />
                        </TabsContent>
                        <TabsContent value="crons" className="h-full overflow-hidden flex flex-col">
                            <CronsTab />
                        </TabsContent>
                        <TabsContent value="webhooks" className="h-full overflow-hidden flex flex-col">
                            <WebhooksTab />
                        </TabsContent>
                        <TabsContent value="settings" className="h-full overflow-hidden flex flex-col">
                            <SettingsTab />
                        </TabsContent>
                    </div>
                </Tabs>
            </SandboxProvider>
        </div>
    )
}
