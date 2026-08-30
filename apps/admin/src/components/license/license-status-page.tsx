import { useLicenseStatus, useRefreshLicense, useLicenseInfo } from '@/hooks/license/use-license-status'
import { useGatewayLicenseStatus } from '@/hooks/license/use-license-observer'
import { UpgradeLicenseDialog } from '../settings/usage/upgrade-license-dialog'
import { useState } from 'react'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import { cn } from '@everstack/utils/functions/cn'
import { PlanCard } from '../settings/billing/plan-card'
import { UsageMeter } from '../settings/usage/usage-meter'
import { useGatewayPlans } from '@/hooks/license/use-plans'
import type { BillingPeriod } from '@/config/plans'

dayjs.extend(relativeTime)

const { Card, CardHeader, CardTitle, CardDescription, CardContent, Button, Badge } = ui

export function LicenseStatusPage() {
    const { data, isLoading, isError, error } = useLicenseStatus({
        enablePolling: true,
        pollingInterval: 30000,
    })
    const { data: gatewayStatus } = useGatewayLicenseStatus()
    const refreshMutation = useRefreshLicense()
    const { isLocked, isExpiringSoon, isTrialExpiringSoon } = useLicenseInfo()
    const [selectedPlanId, setSelectedPlanId] = useState<string | null>(null)
    const [billingPeriod, setBillingPeriod] = useState<BillingPeriod>('monthly')
    const { data: plans } = useGatewayPlans()
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
                        <CardTitle className="flex items-center gap-2 text-red-400">
                            <Icon icon="lucide:alert-circle" className="h-5 w-5" />
                            Error Loading License
                        </CardTitle>
                        <CardDescription className="text-red-200/60">
                            {error?.message || 'Failed to load license information'}
                        </CardDescription>
                    </CardHeader>
                    <CardContent>
                        <Button onClick={handleRefresh} variant="outline" className="w-full border-red-500/20 hover:bg-red-500/10 hover:text-red-400">
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
    const currentPlanId = license.tier?.toLowerCase()

    return (
        <div className="space-y-8 p-8 max-w-[1600px] mx-auto">
            <UpgradeLicenseDialog
                open={!!selectedPlanId}
                onOpenChange={(open) => !open && setSelectedPlanId(null)}
                targetPlanId={selectedPlanId || ''}
                billingPeriod={billingPeriod}
            />

            {/* Header */}
            <div className="flex flex-col gap-2">
                <h1 className="text-3xl font-bold tracking-tight text-white light:text-brand-main-50">Subscription & Billing</h1>
                <p className="text-lg text-white/50 light:text-black/50">Manage your plan, usage limits, and billing details.</p>
            </div>

            {/* Alerts Section */}
            <div className="space-y-4">
                {isLocked && (
                    <div className="rounded-lg border border-red-500/20 bg-red-500/10 p-4 flex items-start gap-4">
                        <Icon icon="lucide:lock" className="h-5 w-5 text-red-400 mt-0.5" />
                        <div>
                            <h3 className="font-medium text-red-400">Gateway Locked</h3>
                            <p className="text-sm text-red-300/80 mt-1">{gateway.lock_reason}</p>
                        </div>
                    </div>
                )}

                {(isExpiringSoon || isTrialExpiringSoon) && !isLocked && (
                    <div className="rounded-lg border border-yellow-500/20 bg-yellow-500/10 p-4 flex items-start gap-4">
                        <Icon icon="lucide:alert-triangle" className="h-5 w-5 text-yellow-400 mt-0.5" />
                        <div>
                            <h3 className="font-medium text-yellow-400">
                                {license.is_paid ? 'License Expiring Soon' : 'Free Plan Ending Soon'}
                            </h3>
                            <p className="text-sm text-yellow-300/80 mt-1">
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
            <div className="grid gap-6 lg:grid-cols-3">
                {/* Current Plan Status */}
                <Card className="lg:col-span-1 border-white/10 bg-white/5 light:border-black/10 light:bg-black/5">
                    <CardHeader>
                        <CardTitle>Current Status</CardTitle>
                    </CardHeader>
                    <CardContent className="space-y-6">
                        <div className="flex items-center justify-between">
                            <span className="text-sm text-white/50 light:text-black/50">Plan</span>
                            <Badge variant="outline" className="capitalize text-base px-3 py-1 border-brand-main-500/50 text-brand-main-400 bg-brand-main-500/10">
                                {license.tier}
                            </Badge>
                        </div>

                        <div className="flex items-center justify-between">
                            <span className="text-sm text-white/50 light:text-black/50">Status</span>
                            <div className="flex items-center gap-2">
                                <div className={`h-2 w-2 rounded-full ${license.active ? 'bg-green-500' : 'bg-red-500'}`} />
                                <span className="capitalize font-medium text-white/90 light:text-black/90">{license.status}</span>
                            </div>
                        </div>

                        <div className="flex items-center justify-between">
                            <span className="text-sm text-white/50 light:text-black/50">
                                {license.is_paid ? 'Renews' : 'Free Plan Ends'}
                            </span>
                            <span className="font-medium text-white/90 light:text-black/90">
                                {license.is_paid
                                    ? (license.expires_at ? dayjs(license.expires_at).format('MMM D, YYYY') : 'Never')
                                    : (license.trial_expires ? dayjs(license.trial_expires).format('MMM D, YYYY') : 'Never')}
                            </span>
                        </div>

                        <div className="pt-4 border-t border-white/10 light:border-black/10">
                            <div className="flex items-center justify-between mb-2">
                                <span className="text-sm text-white/50 light:text-black/50">Gateway ID</span>
                                <Icon icon="lucide:copy" className="h-4 w-4 text-white/30 cursor-pointer hover:text-white/70 light:text-black/30 light:hover:text-black/70" />
                            </div>
                            <code className="block w-full p-2 rounded bg-black/20 text-xs text-white/60 font-mono break-all light:text-black/60">
                                {gatewayStatus?.instanceId || 'Unknown'}
                            </code>
                        </div>
                    </CardContent>
                </Card>

                {/* Usage Statistics */}
                <Card className="lg:col-span-2 border-white/10 bg-white/5 light:border-black/10 light:bg-black/5">
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
                    <CardContent className="grid gap-8 md:grid-cols-2">
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
            </div>

            {/* Available Plans */}
            <div className="space-y-6">
                <div className="flex flex-col gap-4">
                    <div className="flex items-center justify-between">
                        <h2 className="text-2xl font-bold text-white light:text-brand-main-50">Available Plans</h2>
                        <span className="text-sm text-white/50 light:text-black/50">Upgrade anytime to unlock more features</span>
                    </div>

                    {/* Billing Period Toggle */}
                    <div className="flex items-center justify-center gap-3 p-1 rounded-lg bg-brand-main-900 border border-white/10 w-fit light:border-black/10">
                        <button
                            onClick={() => setBillingPeriod('monthly')}
                            className={`px-6 py-2 rounded-md text-sm font-medium transition-all ${billingPeriod === 'monthly'
                                ? 'bg-brand-secondary-500 text-white shadow-sm'
                                : 'text-white/60 hover:text-white/80 light:text-black/60 light:hover:text-black/80'
                                }`}
                        >
                            Monthly
                        </button>
                        <button
                            onClick={() => setBillingPeriod('yearly')}
                            className={`px-6 py-2 rounded-md text-sm font-medium transition-all ${billingPeriod === 'yearly'
                                ? 'bg-brand-secondary-500 text-white shadow-sm'
                                : 'text-white/60 hover:text-white/80 light:text-black/60 light:hover:text-black/80'
                                }`}
                        >
                            <span>Yearly</span>
                            <span className="ml-2 text-xs text-green-400">Save 20%</span>
                        </button>
                    </div>
                </div>

                <div className="grid gap-6 md:grid-cols-2 xl:grid-cols-4">
                    {plans?.map((plan) => (
                        <PlanCard
                            key={plan.tier}
                            plan={plan}
                            currentPlanId={currentPlanId}
                            billingPeriod={billingPeriod}
                            upgradesDisabled={true}
                        />
                    ))}
                </div>
            </div>
        </div>
    )
}
