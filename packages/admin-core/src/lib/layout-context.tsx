'use client'

import { createContext, useContext, ReactNode, useMemo } from 'react'

/**
 * Edition mode - determines which features are available
 */
export type EditionMode = 'community' | 'cloud'

/**
 * Navigation item configuration
 */
export interface NavItem {
  name: string
  href: string
  icon: string
  description?: string
  badge?: string
  active?: boolean
}

/**
 * Layout configuration for customizing the admin UI
 */
export interface LayoutConfig {
  /**
   * Edition mode - 'community' for self-hosted, 'cloud' for SaaS
   */
  mode: EditionMode
  
  /**
   * API base URL (convenience, also available via ApiProvider)
   */
  apiBaseUrl: string
  
  /**
   * Additional API headers
   */
  apiHeaders?: Record<string, string>
  
  // ===== Community Edition Features =====
  
  /**
   * Show the license activation guard (Community only)
   */
  showActivationGuard?: boolean
  
  /**
   * Show trial mode banner in sidebar (Community only)
   */
  showTrialBanner?: boolean
  
  /**
   * Show gateway locked banner (Community only)
   */
  showGatewayLockedBanner?: boolean
  
  // ===== Cloud Edition Features =====
  
  /**
   * Show organization switcher (Cloud only)
   */
  showOrgSwitcher?: boolean
  
  /**
   * Show workspace selector (Cloud only)
   */
  showWorkspaceSelector?: boolean
  
  /**
   * Show user menu (Cloud only)
   */
  showUserMenu?: boolean
  
  // ===== Layout Slots =====
  
  /**
   * Custom content for the header area
   */
  headerSlot?: ReactNode
  
  /**
   * Custom content for the top of the sidebar
   */
  sidebarTopSlot?: ReactNode
  
  /**
   * Custom content for the bottom of the sidebar
   */
  sidebarBottomSlot?: ReactNode
  
  // ===== Navigation Customization =====
  
  /**
   * Additional navigation items to add
   */
  additionalNavItems?: NavItem[]
  
  /**
   * Navigation item names to hide
   */
  hiddenNavItems?: string[]
  
  // ===== Callbacks =====
  
  /**
   * Called when user logs out (Cloud only)
   */
  onLogout?: () => void
}

const defaultConfig: LayoutConfig = {
  mode: 'community',
  apiBaseUrl: typeof window !== 'undefined' ? window.location.origin : '',
  showActivationGuard: true,
  showTrialBanner: true,
  showGatewayLockedBanner: true,
}

const LayoutContext = createContext<LayoutConfig>(defaultConfig)

export interface LayoutProviderProps {
  children: ReactNode
  config: Partial<LayoutConfig> & { mode: EditionMode }
}

/**
 * Provider for layout configuration
 * 
 * @example
 * // Community Edition
 * <LayoutProvider config={{
 *   mode: 'community',
 *   apiBaseUrl: window.location.origin,
 *   showTrialBanner: true,
 * }}>
 *   <MainLayout>
 *     <App />
 *   </MainLayout>
 * </LayoutProvider>
 * 
 * @example
 * // Cloud Edition
 * <LayoutProvider config={{
 *   mode: 'cloud',
 *   apiBaseUrl: workspace.gatewayUrl,
 *   showOrgSwitcher: true,
 *   showWorkspaceSelector: true,
 *   showUserMenu: true,
 *   headerSlot: <OrgSwitcher />,
 *   additionalNavItems: [
 *     { name: 'Team', href: '/settings/members', icon: 'users' },
 *   ],
 * }}>
 *   <MainLayout>
 *     <App />
 *   </MainLayout>
 * </LayoutProvider>
 */
export function LayoutProvider({ children, config }: LayoutProviderProps) {
  const mergedConfig = useMemo<LayoutConfig>(() => ({
    ...defaultConfig,
    ...config,
  }), [config])

  return (
    <LayoutContext.Provider value={mergedConfig}>
      {children}
    </LayoutContext.Provider>
  )
}

/**
 * Hook to access the current layout configuration
 */
export function useLayoutConfig(): LayoutConfig {
  const config = useContext(LayoutContext)
  if (!config) {
    throw new Error('useLayoutConfig must be used within a LayoutProvider')
  }
  return config
}

/**
 * Hook to check if running in community edition
 */
export function useIsCommunityEdition(): boolean {
  const { mode } = useLayoutConfig()
  return mode === 'community'
}

/**
 * Hook to check if running in cloud edition
 */
export function useIsCloudEdition(): boolean {
  const { mode } = useLayoutConfig()
  return mode === 'cloud'
}



