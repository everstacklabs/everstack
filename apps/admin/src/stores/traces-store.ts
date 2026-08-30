import { create } from 'zustand'
import { persist } from 'zustand/middleware'

interface TracesStore {
    showMetadata: boolean
    showDuration: boolean

    setShowMetadata: (show: boolean) => void
    setShowDuration: (show: boolean) => void
}

export const useTracesStore = create<TracesStore>()(
    persist(
        (set) => ({
            showMetadata: false,
            showDuration: true,

            setShowMetadata: (show) => set({ showMetadata: show }),
            setShowDuration: (show) => set({ showDuration: show }),
        }),
        {
            name: 'traces-view-settings', // localStorage key
        }
    )
)

