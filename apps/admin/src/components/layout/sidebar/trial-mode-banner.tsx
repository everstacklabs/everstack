import { Clock, Zap } from '@everstack/ui/icons'
import { Link } from '@tanstack/react-router'
import { useTrialStatus } from '../../../hooks/license/use-trial-status'

// Format large numbers for display (e.g., 1000000 -> "1M")
const formatNumber = (num: number): string => {
    if (num >= 1000000) {
        return `${(num / 1000000).toFixed(1).replace(/\.0$/, '')}M`
    }
    if (num >= 1000) {
        return `${(num / 1000).toFixed(1).replace(/\.0$/, '')}K`
    }
    return num.toString()
}

export const TrialModeBanner = () => {
    // Enable polling for trial banner since it displays usage metrics
    const { data: trialStatus, isLoading } = useTrialStatus({ enablePolling: true, pollingInterval: 60000 })

    // Don't show banner if loading, not in trial mode, or trial is expired
    if (isLoading) return null
    if (trialStatus?.mode !== 'trial') return null
    if (!trialStatus?.active) return null

    const isExpired = trialStatus.expired
    const daysRemaining = trialStatus.daysRemaining ?? 0

    // Token-based usage (primary metric now)
    // Fields are typed as bigint in proto, convert to number for display
    const tokensUsed = Number(trialStatus.tokensUsed ?? 0n)
    const tokenLimit = Number(trialStatus.tokenLimit ?? 1000000n)
    const tokenUsagePercent = tokenLimit > 0 ? Math.min((tokensUsed / tokenLimit) * 100, 100) : 0
    const isNearTokenLimit = tokenUsagePercent >= 80

    // RPM limit for display
    const rpmLimit = Number(trialStatus.rpmLimit ?? 60n)

    if (isExpired) {
        return (
            <div className="mx-2 mb-2 rounded-sm bg-amber-500/10 border border-amber-500/20 p-3">
                <div className="flex items-center gap-2 mb-2">
                    <div className="flex size-6 items-center justify-center rounded-md bg-amber-500/20 text-amber-500">
                        <Clock className="size-3.5" />
                    </div>
                    <span className="text-sm font-medium text-amber-500">
                        Free Plan Limit Reached
                    </span>
                </div>
                <p className="text-xs text-amber-400/90 light:text-amber-700/90 mb-3 leading-relaxed">
                    You've reached your free plan limits. Upgrade to continue using all features.
                </p>
                <Link
                    to="/settings/billing"
                    search={{ upgrade_success: false, plan: undefined }}
                    className="flex w-full items-center justify-center rounded bg-amber-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-600 transition-colors"
                >
                    Upgrade Plan
                </Link>
            </div>
        )
    }

    return (
        <div className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-brand-secondary-500/20 text-brand-secondary-500">
                    <Zap className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-brand-secondary-400">
                    Free Plan
                </span>
                <span className="ml-auto text-xs text-brand-secondary-400/70">
                    {daysRemaining} day{daysRemaining !== 1 ? 's' : ''} left
                </span>
            </div>

            {/* Token usage bar (primary metric) */}
            <div className="mb-2">
                <div className="flex justify-between text-xs text-brand-secondary-400/70 mb-1">
                    <span>Token usage</span>
                    <span className={isNearTokenLimit ? 'text-amber-400 light:text-amber-700' : ''}>
                        {formatNumber(tokensUsed)}/{formatNumber(tokenLimit)}
                    </span>
                </div>
                <div className="h-1.5 bg-brand-secondary-500/20 rounded-full overflow-hidden">
                    <div
                        className={`h-full rounded-full transition-all ${isNearTokenLimit ? 'bg-amber-500' : 'bg-brand-secondary-500'
                            }`}
                        style={{ width: `${tokenUsagePercent}%` }}
                    />
                </div>
            </div>

            {/* Rate limit info */}
            <div className="text-xs text-brand-secondary-400/50 mb-2">
                Rate limit: {rpmLimit} req/min
            </div>

            <p className="text-xs text-brand-secondary-400/70 mb-3">
                {isNearTokenLimit
                    ? 'Approaching token limit. Upgrade for more tokens.'
                    : 'Enjoying the free plan? Upgrade for more.'}
            </p>

            <Link
                to="/settings/billing"
                search={{ upgrade_success: false, plan: undefined }}
                className="flex w-full items-center justify-center rounded bg-brand-secondary-500 px-3 py-1.5 text-xs font-medium text-white hover:bg-brand-secondary-600 transition-colors"
            >
                Upgrade Plan
            </Link>
        </div>
    )
}
