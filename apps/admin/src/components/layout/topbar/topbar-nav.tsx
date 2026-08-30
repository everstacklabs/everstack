import { cn } from '@/lib/utils'
import { useLocation } from '@tanstack/react-router'
import { useContext } from 'react'
import { SideNavContext } from '../main-nav'
import { Iconify } from '@everstack/ui'
import { TopbarActions } from './topbar-actions'

export function TopbarNav() {
    const sidebarContext = useContext(SideNavContext)
    const { pathname } = useLocation()

    // Hide topbar on root and studio canvas views (which have their own toolbar)
    const isStudioCanvas = /^\/deployments\/studio\/.+/.test(pathname)
    if (pathname === '/' || isStudioCanvas) return null;

    return (
        <header className={cn(
            'w-full border-b backdrop-blur-2xl border-brand-main-600 bg-brand-main-700/50 min-h-10 flex items-center justify-between',
            'sticky top-0 z-10 py-0.5',
        )}>
            <div className={cn("mx-auto pl-2 pr-2 py-1.5 w-full")}>
                <div className="flex justify-between items-center">
                    <div className="flex items-center">
                        <button
                            type="button"
                            aria-label="Toggle sidebar"
                            onClick={() => sidebarContext.setIsOpen(true)}
                            className="md:hidden flex items-center justify-center text-brand-main-50 rounded-sm hover:bg-brand-main-800 focus-visible:ring-2 p-1.5 focus-visible:ring-white/50 light:focus-visible:ring-black/30 light:focus-visible:ring-black/50"
                        >
                            {/* simple hamburger */}
                            <Iconify.Icon icon="fluent:window-20-regular" className="rotate-270 h-5 w-5 text-brand-main-50" />
                        </button>

                    </div>

                    {/* Action buttons for different routes */}
                    <TopbarActions />
                </div>
            </div>
        </header>
    )
}

