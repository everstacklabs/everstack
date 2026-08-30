import { useState, useEffect, useCallback } from 'react'
import { ui } from '@everstack/ui'
import { Icon } from '@iconify/react'
import { toast } from '@everstack/ui/components'
import { cn } from '@everstack/utils/functions/cn'
import {
    getSpendLimits as fetchSpendLimitsApi,
    setSpendLimit as setSpendLimitApi,
    deleteSpendLimit as deleteSpendLimitApi,
} from '@/server/license'
import {
    SpendLimitType,
    SpendLimitPeriod,
    SpendLimitAction,
} from '@everstack/proto/everstack/license/v1/license_pb'

const { Button, Input, Label, Switch, Select, SelectTrigger, SelectValue, SelectContent, SelectItem } = ui

// Types for spend limits (local representation)
interface SpendLimit {
    id: string
    organization_id: string
    instance_id?: string
    limit_type: 'estimated_cost' | 'actual_billing'
    limit_amount: number
    period: 'daily' | 'monthly'
    action_on_exceed: 'block' | 'warn' | 'notify'
    current_spend: number
    enabled: boolean
}

// Preset spending limit suggestions
const SPEND_PRESETS = [
    { label: '$50', value: 50 },
    { label: '$100', value: 100 },
    { label: '$500', value: 500 },
    { label: '$1,000', value: 1000 },
]

// Convert proto limit type to local type
function protoLimitTypeToLocal(t: SpendLimitType): 'estimated_cost' | 'actual_billing' {
    switch (t) {
        case SpendLimitType.ESTIMATED_COST:
            return 'estimated_cost'
        case SpendLimitType.ACTUAL_BILLING:
            return 'actual_billing'
        default:
            return 'estimated_cost'
    }
}

// Convert local limit type to proto type
function localLimitTypeToProto(t: 'estimated_cost' | 'actual_billing'): SpendLimitType {
    switch (t) {
        case 'estimated_cost':
            return SpendLimitType.ESTIMATED_COST
        case 'actual_billing':
            return SpendLimitType.ACTUAL_BILLING
        default:
            return SpendLimitType.ESTIMATED_COST
    }
}

// Convert proto period to local period
function protoPeriodToLocal(p: SpendLimitPeriod): 'daily' | 'monthly' {
    switch (p) {
        case SpendLimitPeriod.DAILY:
            return 'daily'
        case SpendLimitPeriod.MONTHLY:
            return 'monthly'
        default:
            return 'monthly'
    }
}

// Convert local period to proto period
function localPeriodToProto(p: 'daily' | 'monthly'): SpendLimitPeriod {
    switch (p) {
        case 'daily':
            return SpendLimitPeriod.DAILY
        case 'monthly':
            return SpendLimitPeriod.MONTHLY
        default:
            return SpendLimitPeriod.MONTHLY
    }
}

// Convert proto action to local action
function protoActionToLocal(a: SpendLimitAction): 'block' | 'warn' | 'notify' {
    switch (a) {
        case SpendLimitAction.BLOCK:
            return 'block'
        case SpendLimitAction.WARN:
            return 'warn'
        case SpendLimitAction.NOTIFY:
            return 'notify'
        default:
            return 'block'
    }
}

// Convert local action to proto action
function localActionToProto(a: 'block' | 'warn' | 'notify'): SpendLimitAction {
    switch (a) {
        case 'block':
            return SpendLimitAction.BLOCK
        case 'warn':
            return SpendLimitAction.WARN
        case 'notify':
            return SpendLimitAction.NOTIFY
        default:
            return SpendLimitAction.BLOCK
    }
}

interface SpendLimitsSectionProps {
    organizationId?: string
    instanceId?: string
}

export function SpendLimitsSection({ organizationId = '', instanceId = '' }: SpendLimitsSectionProps = {}) {
    const [spendLimits, setSpendLimits] = useState<SpendLimit[]>([])
    const [isLoading, setIsLoading] = useState(false)
    const [isSettingLimit, setIsSettingLimit] = useState(false)
    const [showCustom, setShowCustom] = useState(false)
    const [customAmount, setCustomAmount] = useState('')
    const [selectedPeriod, setSelectedPeriod] = useState<'daily' | 'monthly'>('monthly')
    const [selectedAction, setSelectedAction] = useState<'block' | 'warn' | 'notify'>('block')

    // Fetch spend limits on mount
    // Note: Don't pass instanceId here - we want ALL limits for the org (including disabled ones)
    // GetEffectiveLimits only returns enabled limits, which hides disabled ones from the admin UI
    const loadSpendLimits = useCallback(async () => {
        if (!organizationId) return
        setIsLoading(true)
        try {
            // Fetch without instanceId to get all limits (not just effective/enabled ones)
            const data = await fetchSpendLimitsApi(organizationId)
            // Convert proto spend limits to local format
            const limits: SpendLimit[] = (data.spendLimits || []).map((l) => ({
                id: l.id,
                organization_id: l.organizationId,
                instance_id: l.instanceId || undefined,
                limit_type: protoLimitTypeToLocal(l.limitType),
                limit_amount: l.limitAmount,
                period: protoPeriodToLocal(l.period),
                action_on_exceed: protoActionToLocal(l.actionOnExceed),
                current_spend: l.currentSpend,
                enabled: l.enabled,
            }))
            setSpendLimits(limits)
        } catch (err) {
            console.error('Failed to load spend limits:', err)
        } finally {
            setIsLoading(false)
        }
    }, [organizationId])

    useEffect(() => {
        loadSpendLimits()
    }, [loadSpendLimits])

    // Handle setting a new limit
    const handleSetLimit = async (amount: number) => {
        if (!organizationId || amount <= 0) return
        setIsSettingLimit(true)
        try {
            await setSpendLimitApi({
                organizationId,
                instanceId: instanceId || undefined,
                limitType: localLimitTypeToProto('estimated_cost'),
                limitAmount: amount,
                period: localPeriodToProto(selectedPeriod),
                actionOnExceed: localActionToProto(selectedAction),
                enabled: true,
            })
            toast.success(`Spend limit of $${amount} set successfully`)
            loadSpendLimits()
            setShowCustom(false)
            setCustomAmount('')
        } catch (err) {
            toast.error('Failed to set spend limit')
            console.error(err)
        } finally {
            setIsSettingLimit(false)
        }
    }

    // Handle toggling a limit
    const handleToggleLimit = async (limit: SpendLimit) => {
        if (!limit.organization_id) {
            toast.error('Cannot toggle limit: missing organization ID')
            return
        }
        try {
            await setSpendLimitApi({
                organizationId: limit.organization_id,
                instanceId: limit.instance_id,
                limitType: localLimitTypeToProto(limit.limit_type),
                limitAmount: limit.limit_amount,
                period: localPeriodToProto(limit.period),
                actionOnExceed: localActionToProto(limit.action_on_exceed),
                enabled: !limit.enabled,
            })
            toast.success(limit.enabled ? 'Spend limit disabled' : 'Spend limit enabled')
            loadSpendLimits()
        } catch (err) {
            toast.error('Failed to update spend limit')
            console.error('Toggle limit error:', err)
        }
    }

    // Handle deleting a limit
    const handleDeleteLimit = async (limitId: string) => {
        if (!limitId) {
            toast.error('Cannot delete limit: missing limit ID')
            return
        }
        try {
            await deleteSpendLimitApi(limitId)
            toast.success('Spend limit removed')
            loadSpendLimits()
        } catch (err: unknown) {
            const errorMessage = err instanceof Error ? err.message : 'Unknown error'
            toast.error(`Failed to remove spend limit: ${errorMessage}`)
        }
    }

    // Calculate progress percentage
    const getProgressPercentage = (current: number, limit: number) => {
        if (limit <= 0) return 0
        return Math.min((current / limit) * 100, 100)
    }

    // Get progress color based on percentage
    const getProgressColor = (percentage: number) => {
        if (percentage >= 90) return 'bg-red-500'
        if (percentage >= 75) return 'bg-yellow-500'
        return 'bg-white/50 light:bg-black/50'
    }

    const activeLimit = spendLimits.find(l => l.enabled && l.limit_type === 'estimated_cost')

    // Show placeholder if not activated (no organization ID)
    if (!organizationId) {
        return (
            <div className="flex flex-col items-center justify-center py-12">
                <div className="relative mb-6">
                    <div className="absolute inset-0 bg-brand-secondary-500/20 rounded-full blur-xl" />
                    <div className="relative rounded-xl border border-brand-main-600 bg-brand-main-800/80 p-4">
                        <Icon icon="stash:billing-info-duotone" className="size-8 text-brand-secondary-400" />
                    </div>
                </div>
                <h3 className="text-base font-medium text-white light:text-brand-main-50 mb-2">Spend limits not available</h3>
                <p className="text-sm text-white/50 light:text-black/50 max-w-sm text-center leading-relaxed">
                    Activate your gateway to enable spend limits.
                </p>
            </div>
        )
    }

    return (
        <div className="space-y-6">
            {/* {isLoading ? (
                <div className="text-center py-8 text-white/50 light:text-black/50">
                    <Icon icon="lucide:loader-2" className="h-8 w-8 mx-auto mb-4 animate-spin" />
                    <p className="text-sm">Loading...</p>
                </div>
            ) : ( */}
            <>
                {/* Current Active Limit */}
                {activeLimit && (
                    <div className="p-4 rounded-lg bg-white/5 light:bg-black/5 border border-white/10 light:border-black/10 space-y-3">
                        <div className="flex items-baseline justify-between">
                            <div className="text-xs text-white/50 light:text-black/50 uppercase tracking-wider">Current Spend</div>
                            <div className="text-xl font-mono font-bold text-white light:text-brand-main-50">
                                ${activeLimit.current_spend.toFixed(2)}
                            </div>
                        </div>
                        <div className="text-xs text-white/40 light:text-black/40">
                            of ${activeLimit.limit_amount.toFixed(2)} ({activeLimit.period})
                        </div>

                        {/* Progress bar */}
                        <div className="relative h-2">
                            <div className="absolute inset-0 rounded-full bg-white/5 light:bg-black/5 ring-1 ring-inset ring-white/10 light:ring-black/10" />
                            <div
                                className={cn("absolute inset-y-0 left-0 rounded-full transition-all duration-500", getProgressColor(
                                    getProgressPercentage(activeLimit.current_spend, activeLimit.limit_amount)
                                ))}
                                style={{
                                    width: `${getProgressPercentage(activeLimit.current_spend, activeLimit.limit_amount)}%`,
                                }}
                            />
                            {/* Limit marker */}
                            <div className="absolute inset-y-0 w-0.5 bg-white/60 light:bg-black/60" style={{ left:'100%', transform: 'translateX(-1px)' }}>
                                <div className="absolute -top-1 -left-1 w-2 h-2 rounded-full bg-white/60 light:bg-black/60" />
                            </div>
                        </div>

                        <div className="flex justify-between text-xs text-white/40 light:text-black/40">
                            <span>{getProgressPercentage(activeLimit.current_spend, activeLimit.limit_amount).toFixed(1)}% used</span>
                            <span>${(activeLimit.limit_amount - activeLimit.current_spend).toFixed(2)} left</span>
                        </div>
                    </div>
                )}

                {/* All Limits List */}
                {spendLimits.length > 0 && (
                    <div className="space-y-2">
                        <h4 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">All Limits</h4>
                        <div className="space-y-2">
                            {spendLimits.map((limit) => (
                                <div
                                    key={limit.id}
                                    className="p-3 rounded-lg bg-white/5 light:bg-black/5 border border-white/10 light:border-black/10 hover:border-white/20 light:hover:border-black/20 transition-colors"
                                >
                                    <div className="flex items-center justify-between mb-2">
                                        <div className="text-sm font-semibold text-white light:text-brand-main-50">
                                            ${limit.limit_amount.toFixed(0)}
                                            <span className="text-white/40 light:text-black/40 text-xs ml-1">/{limit.period}</span>
                                        </div>
                                        <div className="flex items-center gap-2">
                                            <Switch
                                                checked={limit.enabled}
                                                onCheckedChange={() => handleToggleLimit(limit)}
                                            />
                                            <Button
                                                variant="ghost"
                                                size="sm"
                                                className="text-white/40 light:text-black/40 hover:text-red-400 light:hover:text-red-600 h-6 w-6 p-0"
                                                onClick={(e) => {
                                                    e.stopPropagation()
                                                    handleDeleteLimit(limit.id)
                                                }}
                                            >
                                                {isLoading ? <Icon icon="lucide:loader-2" className="h-3 w-3 animate-spin" /> : <Icon icon="lucide:trash-2" className="h-3 w-3" />}
                                            </Button>
                                        </div>
                                    </div>
                                    <div className="text-xs text-white/40 light:text-black/40">
                                        {limit.action_on_exceed ==='block' ? 'Blocks' :
                                            limit.action_on_exceed === 'warn' ? 'Warns' : 'Notifies'} when exceeded
                                    </div>
                                </div>
                            ))}
                        </div>
                    </div>
                )}

                {/* Add New Limit */}
                <div className="space-y-3 pt-4 border-t border-white/10 light:border-black/10">
                    <h4 className="text-xs font-semibold text-white/50 light:text-black/50 uppercase tracking-wider">Add Limit</h4>

                    {/* Period Selection */}
                    <div className="space-y-2">
                        <Label className="text-xs text-white/50 light:text-black/50">Period</Label>
                        <Select value={selectedPeriod} onValueChange={(value:'monthly' | 'daily') => setSelectedPeriod(value)}>
                            <SelectTrigger className="w-full h-9 bg-white/5 light:bg-black/5 border-white/10 light:border-black/10">
                                <SelectValue placeholder="Select period" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="monthly">Monthly</SelectItem>
                                <SelectItem value="daily">Daily</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>

                    {/* Action Selection */}
                    <div className="space-y-2">
                        <Label className="text-xs text-white/50 light:text-black/50">Action</Label>
                        <Select value={selectedAction} onValueChange={(value:'block' | 'warn' | 'notify') => setSelectedAction(value)}>
                            <SelectTrigger className="w-full h-9 bg-white/5 light:bg-black/5 border-white/10 light:border-black/10">
                                <SelectValue placeholder="Select action" />
                            </SelectTrigger>
                            <SelectContent>
                                <SelectItem value="block">Block</SelectItem>
                                <SelectItem value="warn">Warn</SelectItem>
                            </SelectContent>
                        </Select>
                    </div>

                    {/* Amount Selection */}
                    <div className="space-y-2">
                        <Label className="text-xs text-white/50 light:text-black/50">Amount</Label>
                        <div className="grid grid-cols-2 gap-2">
                            {SPEND_PRESETS.map((preset) => (
                                <Button
                                    key={preset.value}
                                    variant="outline"
                                    size="sm"
                                    className="border-white/20 light:border-black/20 text-white light:text-brand-main-50 hover:bg-brand-secondary-500/20 text-xs h-8"
                                    onClick={() => handleSetLimit(preset.value)}
                                    disabled={isSettingLimit}
                                >
                                    {preset.label}
                                </Button>
                            ))}
                        </div>
                        <Button
                            variant="outline"
                            size="sm"
                            className="w-full border-white/20 light:border-black/20 text-white light:text-brand-main-50 hover:bg-white/10 light:hover:bg-black/10 text-xs h-8"
                            onClick={() => setShowCustom(!showCustom)}
                        >
                            <Icon icon="lucide:pencil" className="h-3 w-3 mr-1" />
                            Custom
                        </Button>
                    </div>

                    {/* Custom Amount Input */}
                    {showCustom && (
                        <div className="space-y-2">
                            <div className="relative">
                                <span className="absolute left-3 top-1/2 -translate-y-1/2 text-white/50 light:text-black/50">$</span>
                                <Input
                                    type="number"
                                    placeholder="Amount"
                                    value={customAmount}
                                    onChange={(e) => setCustomAmount(e.target.value)}
                                    className="pl-7 bg-white/5 light:bg-black/5 border-white/10 light:border-black/10 text-white light:text-brand-main-50 text-sm h-9"
                                />
                            </div>
                            <Button
                                className="w-full bg-brand-secondary-500 hover:bg-brand-secondary-600 text-white text-xs h-8"
                                onClick={() => handleSetLimit(parseFloat(customAmount))}
                                disabled={isSettingLimit || !customAmount || parseFloat(customAmount) <= 0}
                            >
                                {isSettingLimit ? (
                                    <>
                                        <Icon icon="lucide:loader-2" className="h-3 w-3 animate-spin mr-1" />
                                        Setting...
                                    </>
                                ) : (
                                    <>
                                        <Icon icon="lucide:check" className="h-3 w-3 mr-1" />
                                        Set Limit
                                    </>
                                )}
                            </Button>
                        </div>
                    )}
                </div>

                {/* Info */}
                <div className="p-3 rounded-lg bg-white/5 light:bg-black/5 border border-white/10 light:border-black/10">
                    <p className="text-xs text-white/50 light:text-black/50">
                        Limits track estimated API costs. The gateway will {selectedAction ==='block' ? 'block' : 'warn about'} requests when exceeded.
                    </p>
                </div>
            </>
            {/* )} */}
        </div>
    )
}

