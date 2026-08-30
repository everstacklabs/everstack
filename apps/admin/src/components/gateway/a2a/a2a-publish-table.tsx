import { ui } from '@everstack/ui'
import { useAgents } from '@/hooks/deployments/use-agents'
import { useActiveDeploymentAgentIds } from '@/hooks/deployments/use-agent-deployments'
import { useA2aPublished, useSetA2aPublished } from '@/hooks/gateway/use-interop'
import { CopyField } from '@/components/gateway/interop/copy-field'
import { a2aAgentCardUrl } from '@/lib/interop/client-config'

const { Switch, Badge } = ui

/** Central A2A publish management: a row per agent with a publish toggle and,
 *  for published agents, the Agent Card URL clients connect to. */
export function A2aPublishTable() {
    const { data: agents } = useAgents()
    const { data: published } = useA2aPublished()
    const setPublished = useSetA2aPublished()
    const activeIds = useActiveDeploymentAgentIds()

    const rows = agents ?? []
    const publishedRows = rows.filter((a) => published?.[a.id])

    return (
        <div className="space-y-4 p-4">
            <p className="text-xs leading-5 text-brand-main-300">
                Publish agents over A2A so external A2A clients (Google ADK, …) can use them as remote sub-agents.
            </p>

            {rows.length === 0 ? (
                <p className="text-xs text-white/45 light:text-black/45">No agents yet.</p>
            ) : (
                <div className="space-y-2">
                    {rows.map((a) => {
                        const on = published?.[a.id] ?? false
                        const deployed = activeIds.has(a.id)
                        return (
                            <div
                                key={a.id}
                                className="flex items-center justify-between gap-4 rounded bg-brand-main-800/40 px-3 py-2 border border-brand-main-600/40"
                            >
                                <div className="flex min-w-0 flex-col">
                                    <span className="truncate text-sm text-brand-secondary-100">{a.name}</span>
                                    {a.description ? (
                                        <span className="truncate text-[11px] text-brand-main-300">{a.description}</span>
                                    ) : null}
                                    {!deployed ? (
                                        <span className="mt-0.5 text-[11px] text-amber-300/80 light:text-amber-700/80">
                                            {on
                                                ? 'Published but has no active deployment — A2A calls will fail until you deploy it.'
                                                : 'Needs an active deployment before it can be published over A2A.'}
                                        </span>
                                    ) : null}
                                </div>
                                <div className="flex shrink-0 items-center gap-2">
                                    {on ? <Badge variant="success">Published</Badge> : null}
                                    {!deployed ? <Badge variant="secondary">Not deployed</Badge> : null}
                                    <Switch
                                        checked={on}
                                        disabled={!deployed && !on}
                                        onCheckedChange={(c) => setPublished.mutate({ agentId: a.id, enabled: c })}
                                    />
                                </div>
                            </div>
                        )
                    })}
                </div>
            )}

            {publishedRows.length > 0 ? (
                <div className="space-y-3">
                    <div className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                        Agent Card URLs
                    </div>
                    {publishedRows.map((a) => (
                        <div key={a.id} className="space-y-1">
                            <span className="text-[11px] text-brand-main-300">{a.name}</span>
                            <CopyField value={a2aAgentCardUrl(a.id)} />
                        </div>
                    ))}
                </div>
            ) : null}
        </div>
    )
}
