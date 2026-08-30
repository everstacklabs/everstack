// Common hooks
// Will be populated with extracted hooks from apps/admin

export { useApiConfig, useApiBaseUrl } from '../../lib/api-context'
export { useLayoutConfig, useIsCommunityEdition, useIsCloudEdition } from '../../lib/layout-context'

export function useDebounce<T>(value: T, delay: number): T {
  // Placeholder - will be replaced with actual implementation
  return value
}

export function useResizeObserver() {
  return { ref: null, width: 0, height: 0 }
}

export function useKeyboardKey(_key: string, _callback: () => void) {
  // Placeholder
}

export function useInstanceHasData() {
  return { hasData: false, isLoading: true }
}



