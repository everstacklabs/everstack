import { useState } from 'react'
import { Iconify, ui } from '@everstack/ui'
import { useAgentDeployments } from '@/hooks/deployments/use-agent-deployments'
import { DeployAgentDialog } from './deploy-agent-dialog'
import { DeploymentDetailSheet } from './deployment-detail-sheet'
import { formatTimestamp } from '@everstack/utils/functions/index'

const { Button } = ui

interface AgentDeploymentsTabProps {
    agentId: string
    agentName: string
}

export function AgentDeploymentsTab({ agentId, agentName }: AgentDeploymentsTabProps) {
    const { data: deployments = [], isLoading } = useAgentDeployments(agentId)
    const [showDeploySheet, setShowDeploySheet] = useState(false)
    const [selectedDeploymentId, setSelectedDeploymentId] = useState<string | null>(null)

    return (
        <div className="flex-1 flex flex-col h-full">
            {isLoading ? (
                <div className="flex-1 flex items-center justify-center text-sm text-brand-main-200">Loading deployments...</div>
            ) : deployments.length === 0 ? (
                <div className="flex-1 flex flex-col items-center justify-center">
                    <div className="relative mb-6">
                        <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                        <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                            <Iconify.Icon icon="heroicons:rocket-launch" className="size-8 text-brand-secondary-400" />
                        </div>
                    </div>
                    <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">No API deployments</h3>
                    <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed mb-4">
                        Deploy this agent as an API endpoint to allow external clients to invoke it with their own auth and rate limits.
                    </p>
                    <Button onClick={() => setShowDeploySheet(true)}>
                        Create First Deployment
                    </Button>
                </div>
            ) : (
                <>
                    <div className="flex items-center justify-between pb-3">
                        <div className="text-sm text-brand-main-100">
                            {deployments.length} deployment{deployments.length !== 1 ? 's' : ''}
                        </div>
                        <Button onClick={() => setShowDeploySheet(true)}>
                            Deploy New Version
                        </Button>
                    </div>
                    <div className="space-y-2">
                        {deployments.map((dep) => (
                            <button
                                key={dep.id}
                                onClick={() => setSelectedDeploymentId(dep.id)}
                                className="w-full text-left flex items-center justify-between rounded-md bg-brand-main-800/50 px-4 py-3 border border-brand-main-700/30 hover:border-brand-main-600/50 transition-colors"
                            >
                                <div className="flex items-center gap-3">
                                    <span className="text-sm font-medium text-brand-secondary-100">{dep.name}</span>
                                    <span className="font-mono text-xs text-brand-main-200">v{dep.version}</span>
                                    <StatusBadge status={dep.status} />
                                </div>
                                <div className="flex items-center gap-4 text-xs text-brand-main-300">
                                    {dep.rateLimitRpm > 0 && (
                                        <span>{dep.rateLimitRpm} RPM</span>
                                    )}
                                    <span>{formatTimestamp(dep.createdAt)}</span>
                                </div>
                            </button>
                        ))}
                    </div>
                </>
            )}

            <DeployAgentDialog
                agentId={agentId}
                agentName={agentName}
                open={showDeploySheet}
                onOpenChange={setShowDeploySheet}
            />

            {selectedDeploymentId && (
                <DeploymentDetailSheet
                    deploymentId={selectedDeploymentId}
                    open={!!selectedDeploymentId}
                    onOpenChange={(open) => !open && setSelectedDeploymentId(null)}
                />
            )}
        </div>
    )
}

function StatusBadge({ status }: { status: string }) {
    const styles: Record<string, string> = {
        active: 'bg-green-500/20 text-green-300 light:text-green-600',
        paused: 'bg-amber-500/20 text-amber-300 light:text-amber-700',
        retired: 'bg-gray-500/20 text-gray-400 light:text-gray-600',
    }
    return (
        <span className={`px-1.5 py-0.5 rounded text-[10px] font-medium ${styles[status] ?? styles.retired}`}>
            {status}
        </span>
    )
}
