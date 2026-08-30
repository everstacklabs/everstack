'use client'

import { type ReactNode, type ComponentType } from 'react'

/**
 * Navigation item common properties
 */
export interface NavItemCommon {
  name: string
  href: string
  exact?: boolean
  isActive?: (pathname: string, href: string) => boolean
  badge?: ReactNode
  arrow?: boolean
  locked?: boolean
}

export type NavSubItemType = NavItemCommon

export interface NavItemType extends NavItemCommon {
  icon: string | ReactNode
  items?: NavSubItemType[]
}

export interface NavGroupType {
  name: string
  icon: string | ReactNode
  href: string
  active: boolean
  onClick?: () => void
  popup?: ComponentType<{ referenceElement: HTMLElement | null }>
  badge?: ReactNode
  description?: string
  learnMoreHref?: string
  showTooltip?: boolean
}

export type SidebarNavGroups<T extends Record<string, unknown>> = (args: T) => NavGroupType[]

export type SidebarNavAreas<T extends Record<string, unknown>> = Record<
  string,
  (args: T) => {
    title?: string | ReactNode
    backHref?: string
    showNews?: boolean
    hideSwitcherIcons?: boolean
    direction?: 'left' | 'right'
    content: {
      name?: string
      items: NavItemType[]
    }[]
  }
>

export interface SidebarNavProps<T extends Record<string, unknown>> {
  groups?: SidebarNavGroups<T>
  groupsTop?: SidebarNavGroups<T>
  groupsBottom?: SidebarNavGroups<T>
  areas: SidebarNavAreas<T>
  currentArea: string | null
  data: T
  toolContent?: ReactNode
  newsContent?: ReactNode
  switcher?: ReactNode
  bottom?: ReactNode
  /**
   * Link component to use for navigation
   * This allows the sidebar to work with different routers (TanStack, Next.js, etc.)
   */
  LinkComponent?: ComponentType<{ to: string; className?: string; children: ReactNode; onClick?: (e: React.MouseEvent) => void }>
  /**
   * Current pathname for active state detection
   */
  pathname?: string
}

/**
 * Sidebar navigation component
 * 
 * This is a placeholder - the full implementation will be extracted from apps/admin.
 * The component is designed to work with any router by accepting a LinkComponent prop.
 */
export function SidebarNav<T extends Record<string, unknown>>(_props: SidebarNavProps<T>) {
  // Placeholder implementation
  // Full implementation will be extracted from apps/admin/src/components/layout/sidebar/sidebar-nav.tsx
  return (
    <nav className="h-full min-h-dvh w-64 bg-brand-main-900">
      <div className="p-4 text-white">
        Sidebar Nav (placeholder)
      </div>
    </nav>
  )
}



