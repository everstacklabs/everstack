// Traces store
// Will be populated with extracted store from apps/admin

import { create } from 'zustand'

export interface TracesState {
  search: string
  setSearch: (search: string) => void
  
  selectedTraceId: string | null
  setSelectedTraceId: (id: string | null) => void
  
  filters: Record<string, unknown>
  setFilter: (key: string, value: unknown) => void
  clearFilters: () => void
}

export const useTracesStore = create<TracesState>((set) => ({
  search: '',
  setSearch: (search) => set({ search }),
  
  selectedTraceId: null,
  setSelectedTraceId: (id) => set({ selectedTraceId: id }),
  
  filters: {},
  setFilter: (key, value) => set((state) => ({
    filters: { ...state.filters, [key]: value }
  })),
  clearFilters: () => set({ filters: {} }),
}))



