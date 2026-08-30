import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'

export interface ApiKeyFilters {
    // Filter by API key type (enum value or 'all' for no filter)
    type: ApiKeyType | 'all'

    // Filter by date range
    dateRange: {
        start?: Date
        end?: Date
    }

    // Filter by status (active, expired, etc.)
    status: 'all' | 'active' | 'expired'

    // Sort configuration
    sortBy: 'name' | 'type' | 'createdAt'
    sortOrder: 'asc' | 'desc'
}

interface ApiKeyFiltersState extends ApiKeyFilters {
    // Actions
    setType: (type: ApiKeyType | 'all') => void
    setDateRange: (dateRange: { start?: Date; end?: Date }) => void
    setStatus: (status: 'all' | 'active' | 'expired') => void
    setSorting: (sortBy: 'name' | 'type' | 'createdAt', sortOrder: 'asc' | 'desc') => void
    resetFilters: () => void
    setFilters: (filters: Partial<ApiKeyFilters>) => void
}

const defaultFilters: ApiKeyFilters = {
    type: 'all',
    dateRange: {},
    status: 'all',
    sortBy: 'createdAt',
    sortOrder: 'desc'
}

export const useApiKeyFilters = create<ApiKeyFiltersState>()(
    devtools(
        (set) => ({
            ...defaultFilters,

            setType: (type) => set({ type }, false, 'setType'),

            setDateRange: (dateRange) => set({ dateRange }, false, 'setDateRange'),

            setStatus: (status) => set({ status }, false, 'setStatus'),

            setSorting: (sortBy, sortOrder) => set({ sortBy, sortOrder }, false, 'setSorting'),

            resetFilters: () => set(defaultFilters, false, 'resetFilters'),

            setFilters: (filters) => set((state) => ({ ...state, ...filters }), false, 'setFilters'),
        }),
        {
            name: 'api-key-filters',
        }
    )
)

// Selector hooks for specific parts of the state (properly memoized)
export const useApiKeyTypeFilter = () => useApiKeyFilters((state) => state.type)
export const useApiKeyDateRangeFilter = () => useApiKeyFilters((state) => state.dateRange)
export const useApiKeyStatusFilter = () => useApiKeyFilters((state) => state.status)

// Individual selectors for sorting to prevent infinite loops
export const useApiKeySorting = () => {
    const sortBy = useApiKeyFilters((state) => state.sortBy)
    const sortOrder = useApiKeyFilters((state) => state.sortOrder)

    return { sortBy, sortOrder }
}

// Individual action selectors to prevent infinite loops
export const useApiKeyFilterActions = () => {
    const setType = useApiKeyFilters((state) => state.setType)
    const setDateRange = useApiKeyFilters((state) => state.setDateRange)
    const setStatus = useApiKeyFilters((state) => state.setStatus)
    const setSorting = useApiKeyFilters((state) => state.setSorting)
    const resetFilters = useApiKeyFilters((state) => state.resetFilters)
    const setFilters = useApiKeyFilters((state) => state.setFilters)

    return {
        setType,
        setDateRange,
        setStatus,
        setSorting,
        resetFilters,
        setFilters,
    }
}
