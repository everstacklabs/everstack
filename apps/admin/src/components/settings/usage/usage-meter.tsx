import { cn } from '@everstack/utils/functions/cn'
import { Icon } from '@iconify/react'

interface UsageMeterProps {
    label: string
    value: number
    max: number
    description?: string
    showThresholds?: boolean
}

export function UsageMeter({ label, value, max, description, showThresholds = true }: UsageMeterProps) {
    const isUnlimited = max === -1
    const percentage = isUnlimited ? 0 : Math.min((value / max) * 100, 100)

    // Determine color based on usage
    let colorClass = 'bg-brand-secondary-500'
    let ringColor = 'ring-brand-secondary-500/20'
    if (!isUnlimited) {
        if (percentage >= 90) {
            colorClass = 'bg-red-500'
            ringColor = 'ring-red-500/20'
        } else if (percentage >= 75) {
            colorClass = 'bg-amber-500'
            ringColor = 'ring-amber-500/20'
        }
    }

    // Format numbers for display
    const formatValue = (val: number) => {
        if (val >= 1_000_000) return `${(val / 1_000_000).toFixed(1)}M`
        if (val >= 1_000) return `${(val / 1_000).toFixed(1)}K`
        return val.toLocaleString()
    }

    return (
        <div className="space-y-3">
            <div className="flex items-center justify-between">
                <div className="space-y-1">
                    <div className="font-medium text-white light:text-brand-main-50">{label}</div>
                    {description && (
                        <div className="text-xs text-white/50 light:text-black/50">{description}</div>
                    )}
                </div>
                <div className="text-right">
                    <div className="flex items-baseline gap-1.5">
                        <span className="text-2xl font-semibold font-mono text-white light:text-brand-main-50">
                            {formatValue(value)}
                        </span>
                        <span className="text-sm text-white/50 light:text-black/50">
                            / {isUnlimited ? '∞' : formatValue(max)}
                        </span>
                    </div>
                    <div className="text-xs text-white/40 light:text-black/40 mt-0.5">
                        {isUnlimited ? 'Unlimited' : `${percentage.toFixed(1)}% used`}
                    </div>
                </div>
            </div>

            <div className="relative">
                {/* Progress bar background */}
                <div className="h-3 w-full rounded-full bg-white/5 light:bg-black/5 overflow-hidden ring-1 ring-white/10 light:ring-black/10">
                    {/* Filled portion */}
                    <div
                        className={cn(
                            "h-full transition-all duration-500 ease-out relative",
                            colorClass,
                            !isUnlimited && percentage >= 75 && 'shadow-lg',
                            ringColor
                        )}
                        style={{ width: `${isUnlimited ? 100 : percentage}%`, opacity: isUnlimited ? 0.3 : 1 }}
                    />
                </div>

                {/* Threshold markers */}
                {showThresholds && !isUnlimited && (
                    <>
                        {/* 75% warning threshold */}
                        <div
                            className="absolute top-0 bottom-0 w-0.5 bg-amber-400/40"
                            style={{ left: '75%' }}
                        >
                            <div className="absolute -top-1 left-1/2 -translate-x-1/2">
                                <Icon icon="lucide:alert-triangle" className="h-3 w-3 text-amber-400/60 light:text-amber-700/60" />
                            </div>
                        </div>

                        {/* 90% critical threshold */}
                        <div
                            className="absolute top-0 bottom-0 w-0.5 bg-red-400/40"
                            style={{ left: '90%' }}
                        >
                            <div className="absolute -top-1 left-1/2 -translate-x-1/2">
                                <Icon icon="lucide:alert-circle" className="h-3 w-3 text-red-400/60 light:text-red-600/60" />
                            </div>
                        </div>
                    </>
                )}
            </div>

            {/* Threshold legend (only show when approaching limits) */}
            {showThresholds && !isUnlimited && percentage >= 50 && (
                <div className="flex items-center gap-4 text-xs text-white/40 light:text-black/40">
                    <div className="flex items-center gap-1.5">
                        <div className="h-2 w-2 rounded-full bg-brand-secondary-500" />
                        <span>Normal</span>
                    </div>
                    {percentage >= 60 && (
                        <div className="flex items-center gap-1.5">
                            <div className="h-2 w-2 rounded-full bg-amber-500" />
                            <span>75% warning</span>
                        </div>
                    )}
                    {percentage >= 75 && (
                        <div className="flex items-center gap-1.5">
                            <div className="h-2 w-2 rounded-full bg-red-500" />
                            <span>90% critical</span>
                        </div>
                    )}
                </div>
            )}
        </div>
    )
}
