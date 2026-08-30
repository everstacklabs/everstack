import { useLicenseStatus, useRefreshLicense, useLicenseInfo } from '@/hooks/license/use-license-status'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { cn } from '@everstack/utils/functions/cn'
import { UsageMeter } from './usage-meter'

dayjs.extend(relativeTime)

const { Card, CardHeader, CardTitle, CardDescription, CardContent, Button } = ui

/**
 * Format a number with appropriate suffix (K, M, B)
 */
function formatNumber(num: number): string {
    if (num >= 1_000_000_000) return `${(num / 1_000_000_000).toFixed(1)}B`
    if (num >= 1_000_000) return `${(num / 1_000_000).toFixed(1)}M`
    if (num >= 1_000) return `${(num / 1_000).toFixed(1)}K`
    return num.toLocaleString()
}

/**
 * Format USD currency
 */
function formatCurrency(amount: number): string {
    return new Intl.NumberFormat('en-US', {
        style: 'currency',
        currency: 'USD',
        minimumFractionDigits: 2,
        maximumFractionDigits: 4,
    }).format(amount)
}

export function UsagePage() {
    const { data, isLoading, isError, error } = useLicenseStatus({
        enablePolling: true,
        pollingInterval: 30000,
    })
    const refreshMutation = useRefreshLicense()
    const { isLocked, isSpendBlocked, isExpiringSoon, isTrialExpiringSoon } = useLicenseInfo()

    const handleRefresh = () => {
        refreshMutation.mutate()
    }

    if (isLoading) {
        return (
            <div className="flex h-[50vh] items-center justify-center">
                <div className="flex flex-col items-center gap-4">
                    <Icon icon="lucide:loader-2" className="h-8 w-8 animate-spin text-white/20 light:text-black/20" />
                    <p className="text-sm text-white/40 light:text-black/40">Loading license information...</p>
                </div>
            </div>
        )
    }

    if (isError) {
        return (
            <div className="flex h-[50vh] items-center justify-center p-4">
                <Card className="w-full max-w-md border-red-500/20 bg-red-500/5">
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2 text-red-400 light:text-red-600">
                            <Icon icon="lucide:alert-circle" className="h-5 w-5" />
                            Error Loading License
                        </CardTitle>
                        <CardDescription className="text-red-200/60 light:text-red-700/60">
                            {error?.message || 'Failed to load license information'}
                        </CardDescription>
                    </CardHeader>
                    <CardContent>
                        <Button onClick={handleRefresh} variant="outline" className="w-full border-red-500/20 hover:bg-red-500/10 hover:text-red-400 light:hover:text-red-600">
                            <Icon icon="lucide:refresh-cw" className="mr-2 h-4 w-4" />
                            Try Again
                        </Button>
                    </CardContent>
                </Card>
            </div>
        )
    }

    if (!data) return null

    const { license, usage, gateway } = data

    return (
        <div className="p-4 w-full max-w-[1600px] mx-auto">
            {/* Alerts Section */}
            <div className="space-y-4">
                {isLocked && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-4 flex items-start gap-4">
                        <Icon icon="lucide:lock" className="h-5 w-5 text-red-400 light:text-red-600 mt-0.5" />
                        <div>
                            <h3 className="font-medium text-red-400 light:text-red-600">Gateway Locked</h3>
                            <p className="text-sm text-red-300/80 light:text-red-600/80 mt-1">{gateway.lock_reason}</p>
                        </div>
                    </div>
                )}

                {isSpendBlocked && !isLocked && (
                    <div className="rounded-lg border border-amber-500/20 bg-amber-500/10 p-4 flex items-start gap-4">
                        <Icon icon="lucide:alert-triangle" className="h-5 w-5 text-amber-400 light:text-amber-700 mt-0.5" />
                        <div>
                            <h3 className="font-medium text-amber-400 light:text-amber-700">Usage Limit Reached</h3>
                            <p className="text-sm text-amber-300/80 light:text-amber-700/80 mt-1">
                                {gateway.spend_blocked_reason || 'AI gateway requests are paused. Upgrade your plan or wait for limits to reset.'}
                            </p>
                        </div>
                    </div>
                )}

                {(isExpiringSoon || isTrialExpiringSoon) && !isLocked && !isSpendBlocked && (
                    <div className="rounded-lg border border-yellow-500/20 bg-yellow-500/10 p-4 flex items-start gap-4">
                        <Icon icon="lucide:alert-triangle" className="h-5 w-5 text-yellow-400 light:text-yellow-700 mt-0.5" />
                        <div>
                            <h3 className="font-medium text-yellow-400 light:text-yellow-700">
                                {license.is_paid ? 'License Expiring Soon' : 'Free Plan Ending Soon'}
                            </h3>
                            <p className="text-sm text-yellow-300/80 light:text-yellow-700/80 mt-1">
                                {license.is_paid && license.expires_at
                                    ? `Your license expires ${dayjs(license.expires_at).fromNow()}. Please renew to avoid service interruption.`
                                    : license.trial_expires
                                        ? `Your free plan ends ${dayjs(license.trial_expires).fromNow()}. Upgrade to continue using all features.`
                                        : 'Please upgrade to avoid service interruption.'}
                            </p>
                        </div>
                    </div>
                )}
            </div>

            {/* Current Status & Usage Grid */}
            <div className="w-full">
                {/* Current Plan Status */}
                {/* Usage Statistics */}
                <Card className="w-full border-white/10 light:border-black/10 bg-brand-main-950/50">
                    <CardHeader>
                        <div className="flex items-center justify-between">
                            <div>
                                <CardTitle>Usage Statistics</CardTitle>
                                <CardDescription className="mt-1">
                                    Real-time metrics against your plan limits
                                </CardDescription>
                            </div>
                            <Button variant="ghost" size="sm" onClick={handleRefresh} className="h-8 w-8 p-0">
                                <Icon icon="lucide:refresh-cw" className={cn("h-4 w-4", refreshMutation.isPending && "animate-spin")} />
                            </Button>
                        </div>
                    </CardHeader>
                    <CardContent className="grid gap-8 md:grid-cols-3">
                        {license.usage_limits?.map((limit) => {
                            let currentValue = 0
                            let label = ''
                            let description = ''

                            switch (limit.type) {
                                case 'RPM':
                                    currentValue = usage.requests_in_min
                                    label = 'Requests / Minute'
                                    description = 'Current load'
                                    break
                                case 'RPS':
                                    currentValue = usage.requests_in_sec
                                    label = 'Requests / Second'
                                    description = 'Current load'
                                    break
                                case 'RPH':
                                    currentValue = usage.requests_in_hour
                                    label = 'Requests / Hour'
                                    description = 'Current load'
                                    break
                                case 'TOKENS':
                                    currentValue = usage.total_tokens
                                    label = 'Total Tokens'
                                    description = `Since ${dayjs(usage.last_reset).format('MMM D')}`
                                    break
                                case 'REQUESTS':
                                    currentValue = usage.total_requests
                                    label = 'Total Requests'
                                    description = `Since ${dayjs(usage.last_reset).format('MMM D')}`
                                    break
                            }

                            return (
                                <UsageMeter
                                    key={limit.type}
                                    label={label}
                                    value={currentValue}
                                    max={limit.limit}
                                    description={description}
                                />
                            )
                        })}
                    </CardContent>
                </Card>

                {/* Token Usage Card */}
                <Card className="w-full border-white/10 light:border-black/10 bg-brand-main-950/50 mt-6">
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Icon icon="lucide:coins" className="h-5 w-5 text-brand-main-400" />
                            Token Usage
                        </CardTitle>
                        <CardDescription>
                            Total tokens processed this billing period
                        </CardDescription>
                    </CardHeader>
                    <CardContent>
                        <div className="grid gap-6 md:grid-cols-3">
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Input Tokens</div>
                                <div className="text-2xl font-mono font-semibold text-white/90 light:text-black/90">
                                    {formatNumber(usage.total_input_tokens)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Output Tokens</div>
                                <div className="text-2xl font-mono font-semibold text-white/90 light:text-black/90">
                                    {formatNumber(usage.total_output_tokens)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Total Tokens</div>
                                <div className="text-2xl font-mono font-semibold text-brand-main-400">
                                    {formatNumber(usage.total_tokens)}
                                </div>
                            </div>
                        </div>
                    </CardContent>
                </Card>

                {/* Cost & Savings Card */}
                <Card className="w-full border-white/10 light:border-black/10 bg-brand-main-950/50 mt-6">
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Icon icon="lucide:dollar-sign" className="h-5 w-5 text-emerald-400 light:text-emerald-600" />
                            Cost & Savings
                        </CardTitle>
                        <CardDescription>
                            Estimated costs and cache savings this billing period
                        </CardDescription>
                    </CardHeader>
                    <CardContent>
                        <div className="grid gap-6 md:grid-cols-3">
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Estimated Cost</div>
                                <div className="text-2xl font-mono font-semibold text-white/90 light:text-black/90">
                                    {formatCurrency(usage.estimated_cost_usd)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Cache Savings</div>
                                <div className="text-2xl font-mono font-semibold text-emerald-400 light:text-emerald-600">
                                    {formatCurrency(usage.cache_savings_usd)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Net Cost</div>
                                <div className="text-2xl font-mono font-semibold text-white/90 light:text-black/90">
                                    {formatCurrency(Math.max(0, usage.estimated_cost_usd - usage.cache_savings_usd))}
                                </div>
                            </div>
                        </div>
                    </CardContent>
                </Card>

                {/* Cache Performance Card */}
                <Card className="w-full border-white/10 light:border-black/10 bg-brand-main-950/50 mt-6">
                    <CardHeader>
                        <CardTitle className="flex items-center gap-2">
                            <Icon icon="lucide:database" className="h-5 w-5 text-cyan-400 light:text-cyan-700" />
                            Cache Performance
                        </CardTitle>
                        <CardDescription>
                            Semantic and exact match cache statistics
                        </CardDescription>
                    </CardHeader>
                    <CardContent>
                        <div className="grid gap-6 md:grid-cols-3">
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Cache Hits</div>
                                <div className="text-2xl font-mono font-semibold text-emerald-400 light:text-emerald-600">
                                    {formatNumber(usage.cache_hits)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Cache Misses</div>
                                <div className="text-2xl font-mono font-semibold text-white/90 light:text-black/90">
                                    {formatNumber(usage.cache_misses)}
                                </div>
                            </div>
                            <div className="space-y-1">
                                <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Hit Rate</div>
                                <div className="text-2xl font-mono font-semibold text-cyan-400 light:text-cyan-700">
                                    {usage.cache_hits + usage.cache_misses > 0
                                        ? `${((usage.cache_hits / (usage.cache_hits + usage.cache_misses)) * 100).toFixed(1)}%`
                                        : '—'
                                    }
                                </div>
                            </div>
                        </div>
                        {/* Cache hit rate progress bar */}
                        {(usage.cache_hits + usage.cache_misses) > 0 && (
                            <div className="mt-6">
                                <div className="h-2 w-full overflow-hidden rounded-full bg-white/5 light:bg-black/5">
                                    <div
                                        className="h-full bg-gradient-to-r from-cyan-500 to-emerald-500 transition-all duration-500"
                                        style={{
                                            width: `${(usage.cache_hits / (usage.cache_hits + usage.cache_misses)) * 100}%`
                                        }}
                                    />
                                </div>
                                <div className="flex justify-between mt-2 text-xs text-white/40 light:text-black/40">
                                    <span>0%</span>
                                    <span>Cache Hit Rate</span>
                                    <span>100%</span>
                                </div>
                            </div>
                        )}
                    </CardContent>
                </Card>
            </div>
        </div>
    )
}
