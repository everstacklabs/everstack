'use client'

import { createContext, useContext, ReactNode, useMemo } from 'react'

/**
 * API configuration for connecting to backend services
 */
export interface ApiConfig {
  /**
   * Base URL for API requests
   * - Community Edition: window.location.origin (same server)
   * - Cloud Edition: dynamic per workspace
   */
  baseUrl: string
  
  /**
   * Additional headers to include with every request
   * - Cloud Edition: may include X-Org-ID, X-Workspace-ID, etc.
   */
  headers?: Record<string, string>
  
  /**
   * Callback when a 401 Unauthorized response is received
   * - Community Edition: typically no-op
   * - Cloud Edition: redirect to login
   */
  onUnauthorized?: () => void
  
  /**
   * Callback when a 403 Forbidden response is received
   */
  onForbidden?: () => void
}

const defaultConfig: ApiConfig = {
  baseUrl: typeof window !== 'undefined' ? window.location.origin : '',
  headers: {},
}

const ApiContext = createContext<ApiConfig>(defaultConfig)

export interface ApiProviderProps {
  children: ReactNode
  /**
   * Base URL for API requests
   */
  baseUrl?: string
  /**
   * Additional headers to include with every request
   */
  headers?: Record<string, string>
  /**
   * Callback when a 401 Unauthorized response is received
   */
  onUnauthorized?: () => void
  /**
   * Callback when a 403 Forbidden response is received
   */
  onForbidden?: () => void
}

/**
 * Provider for API configuration
 * 
 * @example
 * // Community Edition (self-hosted)
 * <ApiProvider baseUrl={window.location.origin}>
 *   <App />
 * </ApiProvider>
 * 
 * @example
 * // Cloud Edition (per-workspace)
 * <ApiProvider 
 *   baseUrl={workspace.gatewayUrl}
 *   headers={{ 'X-Workspace-ID': workspace.id }}
 *   onUnauthorized={() => router.navigate({ to: '/login' })}
 * >
 *   <App />
 * </ApiProvider>
 */
export function ApiProvider({
  children,
  baseUrl,
  headers,
  onUnauthorized,
  onForbidden,
}: ApiProviderProps) {
  const config = useMemo<ApiConfig>(() => ({
    baseUrl: baseUrl ?? (typeof window !== 'undefined' ? window.location.origin : ''),
    headers: headers ?? {},
    onUnauthorized,
    onForbidden,
  }), [baseUrl, headers, onUnauthorized, onForbidden])

  return (
    <ApiContext.Provider value={config}>
      {children}
    </ApiContext.Provider>
  )
}

/**
 * Hook to access the current API configuration
 */
export function useApiConfig(): ApiConfig {
  return useContext(ApiContext)
}

/**
 * Hook to get the API base URL
 */
export function useApiBaseUrl(): string {
  const { baseUrl } = useApiConfig()
  return baseUrl
}



