import { create } from 'zustand'
import type { WorkflowPreviewData } from '@/components/deployments/agents/workflow-preview-panel'

interface SidePanelState {
    // Per-session workflow panel data
    workflowPanels: Record<string, WorkflowPreviewData>  // sessionId → data
    workflowPanelVisible: boolean
    workflowPanelWidth: number

    // Actions
    setWorkflowPanel: (sessionId: string, data: WorkflowPreviewData) => void
    clearWorkflowPanel: (sessionId: string) => void
    toggleWorkflowPanel: () => void
    showWorkflowPanel: () => void
    hideWorkflowPanel: () => void
    setWorkflowPanelWidth: (width: number) => void
}

export const useSidePanelStore = create<SidePanelState>()((set) => ({
    workflowPanels: {},
    workflowPanelVisible: false,
    workflowPanelWidth: 380,

    setWorkflowPanel: (sessionId, data) =>
        set((s) => ({
            workflowPanels: { ...s.workflowPanels, [sessionId]: data },
            workflowPanelVisible: true,
        })),
    clearWorkflowPanel: (sessionId) =>
        set((s) => {
            const { [sessionId]: _, ...rest } = s.workflowPanels
            return { workflowPanels: rest }
        }),
    toggleWorkflowPanel: () => set((s) => ({ workflowPanelVisible: !s.workflowPanelVisible })),
    showWorkflowPanel: () => set({ workflowPanelVisible: true }),
    hideWorkflowPanel: () => set({ workflowPanelVisible: false }),
    setWorkflowPanelWidth: (width) => set({ workflowPanelWidth: width }),
}))
