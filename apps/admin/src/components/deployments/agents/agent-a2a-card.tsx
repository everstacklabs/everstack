import { ui } from '@everstack/ui'
import { CopyField } from '@/components/gateway/interop/copy-field'
import { useA2aPublished, useSetA2aPublished } from '@/hooks/gateway/use-interop'
import { a2aAgentCardUrl, a2aEndpointUrl } from '@/lib/interop/client-config'

const { Switch, Badge } = ui

/**
 * Per-agent A2A publish control. Lives on the agent's API tab, styled to match
 * the deployment sections it sits beside. Publishing makes the agent
 * discoverable + callable by external A2A clients (Google ADK, …) as a remote
 * sub-agent. Requires an active deployment.
 */
export function AgentA2aCard({ agentId, hasDeployment }: { agentId: string; hasDeployment: boolean }) {
    const { data: published } = useA2aPublished()
    const setPublished = useSetA2aPublished()
    const isPublished = published?.[agentId] ?? false

    return (
        <div className="rounded-md border border-brand-main-600/40 bg-brand-main-800/20 p-3 space-y-3">
            <div className="flex items-start justify-between">
                <div>
                    <div className="text-sm font-medium text-brand-main-50">A2A (Agent2Agent)</div>
                    <p className="text-xs leading-5 text-brand-main-300">
                        Let external A2A clients use this agent as a remote sub-agent.
                    </p>
                </div>
                <Badge variant={isPublished ? 'success' : 'secondary'}>{isPublished ? 'Published' : 'Private'}</Badge>
            </div>

            <div className="flex items-center justify-between">
                <span className="text-sm text-brand-main-100">Publish via A2A</span>
                <Switch
                    checked={isPublished}
                    disabled={!hasDeployment && !isPublished}
                    onCheckedChange={(checked) => setPublished.mutate({ agentId, enabled: checked })}
                />
            </div>

            {!hasDeployment ? (
                <p className="text-[11px] text-amber-300/80 light:text-amber-700/80">
                    {isPublished
                        ? 'Published but has no active deployment — A2A calls will fail until you deploy it.'
                        : 'This agent needs an active deployment before it can be published over A2A.'}
                </p>
            ) : null}

            {isPublished ? (
                <div className="space-y-2">
                    <div className="space-y-1">
                        <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                            Agent Card URL
                        </span>
                        <CopyField value={a2aAgentCardUrl(agentId)} />
                    </div>
                    <div className="space-y-1">
                        <span className="text-[11px] text-white/45 light:text-black/45 uppercase tracking-wider">
                            A2A endpoint
                        </span>
                        <CopyField value={a2aEndpointUrl(agentId)} />
                    </div>
                </div>
            ) : null}
        </div>
    )
}
