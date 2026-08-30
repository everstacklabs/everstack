import { type PropsWithChildren } from 'react'
import { cn } from '@/lib/utils'
import { useMediaQuery } from '@everstack/ui'
import { useState, useEffect } from 'react'
import { SideNavContext } from './main-nav'
import { AppSidebarNav } from './sidebar/app-sidebar-nav'
import { useContextPanelStore } from '@/stores/context-panel-store'
import { X } from 'lucide-react'
import { Button } from '@everstack/ui/components'

type ChatFirstLayoutProps = PropsWithChildren<{}>

export function ChatFirstLayout({ children }: ChatFirstLayoutProps) {
  const { isMobile } = useMediaQuery()
  const [isOpen, setIsOpen] = useState(false)
  const { isOpen: isPanelOpen, panelType, panelData, close: closePanel } = useContextPanelStore()

  useEffect(() => {
    document.body.style.overflow = isOpen && isMobile ? 'hidden' : 'auto'
  }, [isOpen, isMobile])

  return (
    <SideNavContext.Provider value={{ isOpen, setIsOpen }}>
      <div className="h-screen overflow-hidden scrollbar-hide md:grid md:grid-cols-[min-content_minmax(0,1fr)]">
        {/* Sidebar */}
        <div
          className={cn(
            'fixed left-0 top-0 z-50 h-dvh w-screen transition-[background,backdrop-filter] md:sticky md:z-auto md:w-full md:bg-transparent',
            isOpen
              ? 'bg-black/20 backdrop-blur-sm'
              : 'bg-transparent max-md:pointer-events-none',
          )}
          onClick={(e) => {
            if (e.target === e.currentTarget) {
              e.stopPropagation()
              setIsOpen(false)
            }
          }}
        >
          <div
            className={cn(
              'h-full w-min max-w-full z-50 md:z-0 bg-brand-main-950 transition-transform md:translate-x-0',
              !isOpen && '-translate-x-full',
            )}
          >
            <AppSidebarNav />
          </div>
        </div>

        {/* Main content area: chat + optional context panel */}
        <div className="flex-1 bg-brand-main-950 overflow-hidden flex flex-row">
          {/* Chat pane */}
          <div className="flex-1 min-w-0 border-l border-brand-main-600 bg-brand-main-700/50 flex flex-col">
            {children}
          </div>

          {/* Context panel */}
          <div
            className={cn(
              'border-l border-brand-main-600 bg-brand-main-800/80 transition-all duration-200 overflow-hidden',
              isPanelOpen ? 'w-[400px]' : 'w-0',
            )}
          >
            {isPanelOpen && (
              <div className="h-full flex flex-col w-[400px]">
                <div className="flex items-center justify-between px-4 py-3 border-b border-brand-main-600">
                  <h3 className="text-sm font-medium text-white light:text-brand-main-50 capitalize">
                    {panelType?.replace('-', ' ') ?? 'Details'}
                  </h3>
                  <Button
                    size="xs"
                    variant="ghost"
                    onClick={closePanel}
                    className="text-white/40 hover:text-white/70 light:text-black/40 light:hover:text-black/70"
                  >
                    <X className="size-4" />
                  </Button>
                </div>
                <div className="flex-1 overflow-y-auto p-4 scrollbar-macos">
                  <ContextPanelContent type={panelType} data={panelData} />
                </div>
              </div>
            )}
          </div>
        </div>
      </div>
    </SideNavContext.Provider>
  )
}

function ContextPanelContent({
  type,
  data,
}: {
  type: string | null
  data: Record<string, unknown>
}) {
  if (type === 'agent-detail') {
    const agentId = data.agentId as string
    return (
      <div className="space-y-3">
        <p className="text-xs text-white/50 light:text-black/50">Agent ID: {agentId}</p>
        <p className="text-sm text-white/70 light:text-black/70">
          Full agent detail view will be rendered here in a future iteration.
        </p>
        <a
          href={`/deployments/agents/${agentId}/overview`}
          className="text-xs text-brand-secondary-400 hover:underline"
        >
          Open full agent page →
        </a>
      </div>
    )
  }

  return (
    <p className="text-sm text-white/50 light:text-black/50">No content to display.</p>
  )
}
