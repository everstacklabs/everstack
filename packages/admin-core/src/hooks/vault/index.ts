// Vault hooks
// Will be populated with extracted hooks from apps/admin

export function useApiKeys() {
  return { data: [], isLoading: true, error: null }
}

export function useProviders() {
  return { data: [], isLoading: true, error: null }
}

export function useProviderApiKeys() {
  return { data: [], isLoading: true, error: null }
}

export function useCatalog() {
  return { data: null, isLoading: true, error: null }
}

export function useModelDiscovery() {
  return { discover: () => {}, isDiscovering: false }
}



