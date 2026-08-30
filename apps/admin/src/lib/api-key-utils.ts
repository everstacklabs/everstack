import { ApiKeyType } from '@everstack/proto/everstack/api_key/v1/api_key_pb'

/**
 * Utility functions for API key type conversions and transformations
 */

/**
 * Convert ApiKeyType enum to display string
 */
export function getApiKeyTypeLabel(type: ApiKeyType): string {
    switch (type) {
        case ApiKeyType.USER:
            return 'User Account'
        case ApiKeyType.ORG:
            return 'Service Account'
        default:
            return 'Unknown'
    }
}

/**
 * Convert ApiKeyType enum to string value for form inputs
 */
export function apiKeyTypeToString(type: ApiKeyType | 'all'): string {
    if (type === 'all') {
        return 'all'
    }
    return type.toString()
}

/**
 * Convert string value back to ApiKeyType enum or 'all'
 */
export function stringToApiKeyType(value: string): ApiKeyType | 'all' {
    if (value === 'all') {
        return 'all'
    }
    const numericValue = parseInt(value, 10)
    return numericValue as ApiKeyType
}

/**
 * Get all available API key type options for dropdowns
 */
export function getApiKeyTypeOptions() {
    return [
        { value: 'all', label: 'All Types' },
        { value: ApiKeyType.USER.toString(), label: getApiKeyTypeLabel(ApiKeyType.USER) },
        { value: ApiKeyType.ORG.toString(), label: getApiKeyTypeLabel(ApiKeyType.ORG) },
    ]
}
