'use client'

import { type PropsWithChildren, type ReactNode } from 'react'
import { useLayoutConfig } from '../../lib/layout-context'

export interface MainLayoutProps {
  children: ReactNode
  /**
   * Custom sidebar component to render
   * If not provided, uses the default sidebar from layout config
   */
  sidebar?: ReactNode
}

/**
 * Main layout wrapper component
 * 
 * This component provides the basic layout structure with a sidebar and main content area.
 * It reads configuration from the LayoutProvider to customize behavior based on edition.
 */
export function MainLayout({ children, sidebar }: PropsWithChildren<MainLayoutProps>) {
  const config = useLayoutConfig()
  
  return (
    <div className="flex h-screen w-full bg-brand-main-950">
      {/* Sidebar */}
      {sidebar && (
        <aside className="flex-shrink-0">
          {sidebar}
        </aside>
      )}
      
      {/* Header slot from config */}
      {config.headerSlot && (
        <div className="flex-shrink-0">
          {config.headerSlot}
        </div>
      )}
      
      {/* Main content */}
      <main className="flex-1 min-h-0 flex flex-col overflow-y-auto scrollbar-macos">
        {children}
      </main>
    </div>
  )
}



