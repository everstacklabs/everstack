import { useState } from 'react'
import { Link } from '@tanstack/react-router'
import { ui } from '@everstack/ui'
import { CopyField, CodeBox } from '@/components/gateway/interop/copy-field'
import { AdkStatusPanel } from '@/components/gateway/interop/adk-status-panel'
import { useMcpToolSettings, useSetMcpTool } from '@/hooks/gateway/use-interop'
import { MCP_CLIENTS, type McpClient, mcpEndpointUrl, buildMcpClientConfig } from '@/lib/interop/client-config'

const { Tabs, TabsList, TabsTrigger, TabsContent, Switch, Badge } = ui

const TAB_TRIGGER =
    'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

// The tools the MCP server can expose. Read-only descriptions; the toggle writes
// a per-tenant override.
const EXPOSED_TOOLS: { name: string; label: string; note?: string }[] = [
    { name: 'everstack_whoami', label: 'Identity probe' },
    { name: 'memory_query', label: 'Search tenant memory' },
    { name: 'memory_store', label: 'Store tenant memory' },
    { name: 'web_search', label: 'Web search' },
    { name: 'web_fetch', label: 'Fetch a URL' },
    { name: 'list_agents', label: 'List agents' },
    { name: 'get_agent', label: 'Get an agent' },
    { name: 'run_agent', label: 'Invoke a deployed agent' },
    { name: 'run_adk_agent', label: 'Run an ADK agent', note: 'Requires the ADK runtime (below).' },
]

export function McpPublishPanel() {
    const [client, setClient] = useState<McpClient>('claude')
    const url = mcpEndpointUrl()
    const { data: toolSettings } = useMcpToolSettings()
    const setTool = useSetMcpTool()
    const activeClient = MCP_CLIENTS.find((c) => c.id === client) ?? MCP_CLIENTS[0]

    return (
        <div className="flex flex-col gap-6 p-4">
            <div className="space-y-2">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Your MCP endpoint
                </div>
                <CopyField value={url} />
                <p className="text-xs leading-5 text-brand-main-300">
                    External clients authenticate with an Everstack API key as a Bearer token. Create or manage keys in{' '}
                    <Link to="/vault/api-keys" className="text-brand-secondary-300 hover:text-white light:hover:text-brand-main-50 underline">
                        Vault → API Keys
                    </Link>
                    .
                </p>
            </div>

            <div className="space-y-2">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Connect a client
                </div>
                <Tabs value={client} onValueChange={(v) => setClient(v as McpClient)}>
                    <TabsList className="w-fit bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
                        {MCP_CLIENTS.map((c) => (
                            <TabsTrigger key={c.id} value={c.id} className={TAB_TRIGGER}>
                                {c.label}
                            </TabsTrigger>
                        ))}
                    </TabsList>
                    {MCP_CLIENTS.map((c) => (
                        <TabsContent key={c.id} value={c.id} className="mt-3">
                            <CodeBox code={buildMcpClientConfig(c.id, url, '')} language={c.lang} />
                        </TabsContent>
                    ))}
                </Tabs>
                <p className="text-[11px] text-white/45 light:text-black/45">Showing {activeClient.label} configuration.</p>
            </div>

            <div className="space-y-2">
                <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                    Exposed tools
                </div>
                <div className="space-y-2">
                    {EXPOSED_TOOLS.map((t) => {
                        const enabled = toolSettings?.[t.name] ?? true
                        return (
                            <div
                                key={t.name}
                                className="flex items-center justify-between rounded bg-brand-main-800/40 px-3 py-2 border border-brand-main-600/40"
                            >
                                <div className="flex min-w-0 flex-col">
                                    <span className="font-mono text-xs text-brand-secondary-100">{t.name}</span>
                                    <span className="text-[11px] text-brand-main-300">
                                        {t.label}
                                        {t.note ? ` - ${t.note}` : ''}
                                    </span>
                                </div>
                                <div className="flex shrink-0 items-center gap-2">
                                    {!enabled ? <Badge variant="secondary">Disabled</Badge> : null}
                                    <Switch
                                        checked={enabled}
                                        onCheckedChange={(checked) => setTool.mutate({ tool: t.name, enabled: checked })}
                                    />
                                </div>
                            </div>
                        )
                    })}
                </div>
            </div>

            <AdkStatusPanel />
        </div>
    )
}
