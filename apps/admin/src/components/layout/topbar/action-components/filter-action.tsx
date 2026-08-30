import { useCallback, useMemo } from 'react'
import { ui } from '@everstack/ui'
import { type FilterAction as FilterActionType } from '../types'
import { useApiKeyFilters, useApiKeyFilterActions } from '@/stores/filters/api-keys-filters'
import { apiKeyTypeToString, stringToApiKeyType } from '@/lib/api-key-utils'

const { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } = ui

interface FilterActionProps {
    action: FilterActionType
}

export function FilterAction({ action }: FilterActionProps) {
    const filters = useApiKeyFilters()
    const { setType, setStatus } = useApiKeyFilterActions()

    const handleValueChange = useCallback((value: string) => {
        switch (action.storeAction) {
            case 'setType':
                // Convert string back to ApiKeyType enum or 'all'
                setType(stringToApiKeyType(value))
                break
            case 'setStatus':
                setStatus(value as any)
                break
            default:
                console.warn(`Unknown filter action: ${action.storeAction}`)
        }
    }, [action.storeAction, setType, setStatus])

    const currentValue = useMemo(() => {
        switch (action.storeKey) {
            case 'type':
                // Convert ApiKeyType enum to string for display
                return apiKeyTypeToString(filters.type)
            case 'status':
                return filters.status
            default:
                return ''
        }
    }, [action.storeKey, filters.type, filters.status])

    if (action.filterType === 'select') {
        return (
            <Select key={action.key} value={currentValue} onValueChange={handleValueChange}>
                <SelectTrigger className={`bg-brand-main-800 border-brand-main-600 text-white light:text-brand-main-50 ${action.className || ''}`}>
                    <SelectValue placeholder={action.label} />
                </SelectTrigger>
                <SelectContent className="bg-brand-main-800 border-brand-main-600">
                    {action.options?.map((option) => (
                        <SelectItem
                            key={option.value}
                            value={option.value}
                            className="text-white light:text-brand-main-50 hover:bg-brand-main-700 focus:bg-brand-main-700"
                        >
                            {option.label}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        )
    }

    // For other filter types (date-range, multi-select), we can extend this component
    return (
        <div className="text-white/70 light:text-black/70 text-sm">
            {action.filterType} filter not implemented yet
        </div>
    )
}
