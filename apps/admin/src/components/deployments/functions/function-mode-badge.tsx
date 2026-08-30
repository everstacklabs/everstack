import { ExecutionMode } from '@/server/functions'

interface FunctionModeBadgeProps {
    mode: ExecutionMode
    className?: string
}

export function FunctionModeBadge({ mode, className = '' }: FunctionModeBadgeProps) {
    const getModeLabel = (mode: ExecutionMode): string => {
        switch (mode) {
            case ExecutionMode.WEBHOOK:
                return 'Webhook'
            case ExecutionMode.PROXY:
                return 'Proxy'
            case ExecutionMode.ISOLATED:
                return 'Isolated'
            default:
                return 'Unknown'
        }
    }

    const getModeColor = (mode: ExecutionMode): string => {
        switch (mode) {
            case ExecutionMode.WEBHOOK:
                return 'bg-blue-500/20 text-blue-300 light:text-blue-600'
            case ExecutionMode.PROXY:
                return 'bg-purple-500/20 text-purple-300 light:text-purple-600'
            case ExecutionMode.ISOLATED:
                return 'bg-amber-500/20 text-amber-300 light:text-amber-700'
            default:
                return 'bg-gray-500/20 text-gray-300 light:text-gray-700'
        }
    }

    return (
        <span className={`px-2 py-0.5 rounded text-xs font-medium whitespace-nowrap ${getModeColor(mode)} ${className}`}>
            {getModeLabel(mode)}
        </span>
    )
}
