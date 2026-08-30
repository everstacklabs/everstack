import { create } from 'zustand'
import { devtools } from 'zustand/middleware'

export type ContextPanelType = 'agent-detail' | null

interface ContextPanelState {
  isOpen: boolean
  panelType: ContextPanelType
  panelData: Record<string, unknown>

  open: (type: ContextPanelType, data: Record<string, unknown>) => void
  close: () => void
}

export const useContextPanelStore = create<ContextPanelState>()(
  devtools(
    (set) => ({
      isOpen: false,
      panelType: null,
      panelData: {},

      open: (panelType, panelData) =>
        set({ isOpen: true, panelType, panelData }),
      close: () =>
        set({ isOpen: false, panelType: null, panelData: {} }),
    }),
    { name: 'context-panel' },
  ),
)
