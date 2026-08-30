import { create } from 'zustand'
import { persist } from 'zustand/middleware'

export type TimeRangePreset = '15m' | '6h' | '12h' | '24h' | '3d' | '7d' | '14d' | '30d' | '90d' | 'custom'

export interface CustomTimeRange {
    start: Date
    end: Date
}

interface LogsStore {
    isLiveMode: boolean
    timeRange: TimeRangePreset
    customRange: CustomTimeRange | null

    setLiveMode: (isLive: boolean) => void
    setTimeRange: (range: TimeRangePreset) => void
    setCustomRange: (range: CustomTimeRange | null) => void
    goLive: () => void
}

export const useLogsStore = create<LogsStore>()(
    persist(
        (set) => ({
            isLiveMode: true, // default to live mode
            timeRange: '15m',
            customRange: null,

            setLiveMode: (isLive) => set({ isLiveMode: isLive }),
            setTimeRange: (range) => set({ timeRange: range }),
            setCustomRange: (range) => set({ customRange: range }),
            goLive: () => set({ isLiveMode: true, timeRange: '15m', customRange: null }),
        }),
        {
            name: 'logs-view-settings', // localStorage key
        }
    )
)
