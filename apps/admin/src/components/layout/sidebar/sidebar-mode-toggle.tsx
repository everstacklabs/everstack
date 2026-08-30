import { ui } from '@everstack/ui'

const { Tabs, TabsList, TabsTrigger } = ui

const TAB_TRIGGER_CLASS =
  'relative flex items-center gap-2 data-[state=active]:bg-brand-secondary-600/20 data-[state=active]:text-brand-secondary-300 data-[state=active]:border-brand-secondary-500/30 text-brand-secondary-100 hover:text-white light:hover:text-brand-main-50 transition-colors py-1'

export type SidebarMode = 'browse' | 'chat'

interface SidebarModeToggleProps {
  mode: SidebarMode
  onModeChange: (mode: SidebarMode) => void
}

export function SidebarModeToggle({ mode, onModeChange }: SidebarModeToggleProps) {
  return (
    <Tabs value={mode} onValueChange={(v) => onModeChange(v as SidebarMode)}>
      <TabsList className="w-full bg-brand-main-800/50 border border-brand-main-600 rounded p-1 h-auto gap-1">
        <TabsTrigger value="browse" className={TAB_TRIGGER_CLASS}>
          Browse
        </TabsTrigger>
        <TabsTrigger value="chat" className={TAB_TRIGGER_CLASS}>
          Chat
        </TabsTrigger>
      </TabsList>
    </Tabs>
  )
}
