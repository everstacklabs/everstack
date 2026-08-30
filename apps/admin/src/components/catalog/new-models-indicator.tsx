import { ui } from '@everstack/ui'
import { useNewModels } from '@/hooks/vault/use-catalog'

const { Badge } = ui

interface NewModelsIndicatorProps {
    providerName?: string
}

export function NewModelsIndicator({ providerName }: NewModelsIndicatorProps) {
    const { data: newModels } = useNewModels(providerName)

    // Check if this provider has new models
    const hasNewModels = newModels && newModels.models && newModels.models.some(
        model => !providerName || model.provider === providerName
    )

    if (!hasNewModels) {
        return null
    }

    return (
        <Badge
            size="sm"
            variant="default"
            className="bg-brand-secondary-700/50 border-brand-secondary-700 text-xs hover:bg-green-600 text-white light:text-brand-main-50"
        >
            NEW
        </Badge>
    )
}

