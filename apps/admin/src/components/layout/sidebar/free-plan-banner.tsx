import { Zap } from '@everstack/ui/icons'
import { Link } from '@tanstack/react-router'
import { useLicenseStatus } from '@/hooks/license/use-license-status'

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

export const FreePlanBanner = () => {
    const { data: licenseStatus, isLoading } = useLicenseStatus({ enablePolling: true, pollingInterval: 60000 })

    // Don't show banner if loading or if user is on a paid plan
    if (isLoading) return null
    if (!licenseStatus?.license?.active) return null
    if (licenseStatus?.license?.is_paid) return null

    // Get token usage from license status
    const tokensUsed = licenseStatus?.usage?.total_tokens ?? 0

    // Find token limit from usage_limits
    const tokenLimitEntry = licenseStatus?.license?.usage_limits?.find(l => l.type === 'TOKENS')
    const tokenLimit = tokenLimitEntry?.limit ?? 1000000
    const hasTokenLimit = tokenLimit > 0 && tokenLimit !== -1

    const tokenUsagePercent = hasTokenLimit ? Math.min((tokensUsed / tokenLimit) * 100, 100) : 0
    const isNearTokenLimit = tokenUsagePercent >= 80

    // Get RPM limit for display
    const rpmLimitEntry = licenseStatus?.license?.usage_limits?.find(l => l.type === 'RPM')
    const rpmLimit = rpmLimitEntry?.limit ?? 60

    return (
        <div className="mx-2 mb-2 rounded-sm bg-brand-secondary-700/10 border border-brand-secondary-600/20 p-3">
            <div className="flex items-center gap-2 mb-2">
                <div className="flex size-6 items-center justify-center rounded-md bg-brand-secondary-500/20 text-brand-secondary-500">
                    <Zap className="size-3.5" />
                </div>
                <span className="text-sm font-medium text-brand-secondary-400">
                    Free Plan
                </span>
            </div>

            {/* Token usage bar (if we have a limit) */}
            {hasTokenLimit && (
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
            )}

            {/* Rate limit info */}
            {rpmLimit > 0 && (
                <div className="text-xs text-brand-secondary-400/50 mb-2">
                    Rate limit: {rpmLimit} req/min
                </div>
            )}

            <p className="text-xs text-brand-secondary-400/70 mb-3">
                {isNearTokenLimit
                    ? 'Approaching token limit. Upgrade for more tokens.'
                    : 'Upgrade for higher limits and more features.'}
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
