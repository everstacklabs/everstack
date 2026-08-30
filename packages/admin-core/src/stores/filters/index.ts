// Filter stores
// Will be populated with extracted stores from apps/admin

import { create } from 'zustand'

export interface ApiKeysFiltersState {
  search: string
  setSearch: (search: string) => void
  status: 'all' | 'active' | 'revoked'
  setStatus: (status: 'all' | 'active' | 'revoked') => void
}

export const useApiKeysFiltersStore = create<ApiKeysFiltersState>((set) => ({
  search: '',
  setSearch: (search) => set({ search }),
  status: 'all',
  setStatus: (status) => set({ status }),
}))



