import { cn } from '@everstack/utils/functions/cn'

interface MetricBarProps {
    label: string
    value: number
    limit: number
    unit?: string
    description?: string
    formatter?: (value: number) => string
}

// Format numbers for display
const formatValue = (val: number) => {
    if (val >= 1_000_000_000) return `${(val / 1_000_000_000).toFixed(2)}B`
    if (val >= 1_000_000) return `${(val / 1_000_000).toFixed(2)}M`
    if (val >= 1_000) return `${(val / 1_000).toFixed(1)}K`
    return val.toLocaleString()
}

const formatCurrency = (val: number) => {
    return new Intl.NumberFormat('en-US', {
        style: 'currency',
        currency: 'USD',
        minimumFractionDigits: 2,
        maximumFractionDigits: 2,
    }).format(val)
}

export function MetricBar({ label, value, limit, unit = '', description, formatter }: MetricBarProps) {
    const isUnlimited = limit === -1
    const percentage = isUnlimited ? 0 : Math.min((value / limit) * 100, 100)

    // Determine color based on usage
    let barColor = 'bg-brand-secondary-500'

    if (!isUnlimited) {
        if (percentage >= 90) {
            barColor = 'bg-red-500'
        } else if (percentage >= 75) {
            barColor = 'bg-amber-500'
        }
    }

    const displayValue = formatter ? formatter(value) : unit === '$' ? formatCurrency(value) : formatValue(value)
    const displayLimit = formatter ? formatter(limit) : unit === '$' ? formatCurrency(limit) : isUnlimited ? '∞' : formatValue(limit)

    return (
        <div className="space-y-2">
            {/* Header */}
            <div className="flex items-baseline justify-between">
                <div className="flex-1">
                    <div className="text-sm font-medium text-white light:text-brand-main-50">{label}</div>
                    {description && (
                        <div className="text-xs text-white/40 light:text-black/40 mt-0.5">{description}</div>
                    )}
                </div>
                <div className="text-right">
                    <div className="text-lg font-semibold font-mono text-white light:text-brand-main-50">
                        {displayValue}
                    </div>
                    <div className="text-xs text-white/40 light:text-black/40">
                        of {displayLimit}
                    </div>
                </div>
            </div>

            {/* Progress Bar with Limit Marker */}
            <div className="relative h-2">
                {/* Background */}
                <div className="absolute inset-0 rounded-full bg-white/5 light:bg-black/5 ring-1 ring-inset ring-white/10 light:ring-black/10" />

                {/* Progress */}
                <div
                    className={cn(
                        "absolute inset-y-0 left-0 rounded-full transition-all duration-500",
                        barColor
                    )}
                    style={{ width: `${isUnlimited ? 100 : percentage}%`, opacity: isUnlimited ? 0.3 : 1 }}
                />

                {/* Limit Marker - vertical line at 100% */}
                {!isUnlimited && (
                    <div className="absolute -inset-y-0.5 w-0.5 h-3 bg-brand-main-300" style={{ left: '95%', transform: 'translateX(-1px)' }}>
                    </div>
                )}
            </div>

            {/* Stats */}
            <div className="flex items-center justify-between text-xs">
                <span className="text-white/40 light:text-black/40">
                    {isUnlimited ? 'Unlimited' : `${percentage.toFixed(1)}% used`}
                </span>
                {!isUnlimited && (
                    <span className="text-white/40 light:text-black/40">
                        {formatter ? formatter(limit - value) : unit === '$' ? formatCurrency(Math.max(0, limit - value)) : formatValue(Math.max(0, limit - value))} remaining
                    </span>
                )}
            </div>
        </div>
    )
}
