// Logs store
// Will be populated with extracted store from apps/admin

import { create } from 'zustand'

export interface LogsState {
  search: string
  setSearch: (search: string) => void
  
  filters: Record<string, unknown>
  setFilter: (key: string, value: unknown) => void
  clearFilters: () => void
}

export const useLogsStore = create<LogsState>((set) => ({
  search: '',
  setSearch: (search) => set({ search }),
  
  filters: {},
  setFilter: (key, value) => set((state) => ({
    filters: { ...state.filters, [key]: value }
  })),
  clearFilters: () => set({ filters: {} }),
}))



