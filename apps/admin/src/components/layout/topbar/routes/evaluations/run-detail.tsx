import { Link, useLocation } from '@tanstack/react-router'
import { Button } from '@everstack/ui/components'
import { ui } from '@everstack/ui'
import { type ActionGroup } from '@/components/layout/topbar/types'
import { useEvalRun, useCancelEvalRun, useRetryEvalRun } from '@/hooks/evaluations/use-evals'
import { ChevronDown } from 'lucide-react'
import { FeatureKey } from '@/config/features'
import { useFeatureGate } from '@/hooks/ee/use-feature-gate'

const {
    DropdownMenu,
    DropdownMenuTrigger,
    DropdownMenuContent,
    DropdownMenuItem,
} = ui

function RunBreadcrumb() {
    const { pathname } = useLocation()
    const segments = pathname.split('/').filter(Boolean)
    const runId = segments.length > 1 ? segments[segments.length - 1] : ''

    const { data: run, isLoading } = useEvalRun(runId)

    return (
        <div className="flex items-center gap-1.5">
            <Link to="/evaluations/runs" className="text-sm font-normal text-brand-main-300 hover:text-white/80 light:hover:text-black/80 transition-colors">
                Eval Runs
            </Link>
            {runId && (
                <>
                    <span className="text-brand-main-400 text-sm">/</span>
                    {isLoading ? (
                        <span className="inline-block h-4 w-24 rounded bg-white/10 light:bg-black/10 animate-pulse" />
                    ) : (
                        <span className="text-sm text-white light:text-brand-main-50 font-normal">
                            {(run as any)?.name ?? runId.substring(0, 12) + '...'}
                        </span>
                    )}
                </>
            )}
        </div>
    )
}

function CancelRunButton() {
    const gate = useFeatureGate(FeatureKey.EVALUATIONS)
    const { pathname } = useLocation()
    const runId = pathname.split('/').filter(Boolean).pop() ?? ''
    const { data: run } = useEvalRun(runId)
    const cancelMutation = useCancelEvalRun()

    if (gate.isBlocked) return null

    const status = (run as any)?.status?.toLowerCase()
    const canCancel = status === 'running' || status === 'pending'

    if (!canCancel) return null

    const handleCancel = async () => {
        if (!confirm('Are you sure you want to cancel this evaluation run?')) return
        await cancelMutation.mutateAsync(runId)
    }

    return (
        <Button
            variant="destructive"
            className=""
            onClick={handleCancel}
            disabled={cancelMutation.isPending}
        >
            {cancelMutation.isPending ? 'Cancelling...' : 'Stop'}
        </Button>
    )
}

function RetryRunDropdown() {
    const gate = useFeatureGate(FeatureKey.EVALUATIONS)
    const { pathname } = useLocation()
    const runId = pathname.split('/').filter(Boolean).pop() ?? ''
    const retryMutation = useRetryEvalRun()

    if (gate.isBlocked) return null

    const handleRetry = async (retryAll: boolean) => {
        await retryMutation.mutateAsync({ id: runId, retryAll })
    }

    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button variant="outline" disabled={retryMutation.isPending} className="border-brand-main-600 bg-brand-main-800 text-brand-main-100 hover:bg-brand-main-700 hover:text-white light:hover:text-brand-main-50">
                    {retryMutation.isPending ? 'Retrying...' : 'Retry'}
                    <ChevronDown className="ml-1 h-3.5 w-3.5" />
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="bg-brand-main-700 border-brand-main-600 text-brand-main-100">
                <DropdownMenuItem
                    onClick={() => handleRetry(false)}
                    className="text-brand-main-50 cursor-pointer hover:bg-brand-secondary-500/15 focus:bg-brand-secondary-500/15 focus:text-white light:focus:text-brand-main-50"
                >
                    Retry Failed Items
                </DropdownMenuItem>
                <DropdownMenuItem
                    onClick={() => handleRetry(true)}
                    className="text-brand-main-50 cursor-pointer hover:bg-brand-secondary-500/15 focus:bg-brand-secondary-500/15 focus:text-white light:focus:text-brand-main-50"
                >
                    Retry All Items
                </DropdownMenuItem>
            </DropdownMenuContent>
        </DropdownMenu>
    )
}

export const EvaluationsRunsDetailActions: ActionGroup[] = [
    {
        title: <RunBreadcrumb />,
    },
    {
        actions: [
            {
                type: 'custom',
                key: 'cancel-run',
                label: 'Cancel Run',
                component: CancelRunButton,
            },
            {
                type: 'custom',
                key: 'retry-run',
                label: 'Retry Run',
                component: RetryRunDropdown,
            },
        ],
    },
]
