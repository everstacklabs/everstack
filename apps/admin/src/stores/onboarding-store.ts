import { create } from 'zustand'

export type OnboardingPath = 'agent' | 'gateway' | 'production'

// The durable fields persisted server-side (see server/onboarding.ts). The
// store is the single in-memory source of truth; useOnboardingSync hydrates it
// from the server on mount and persists changes back. There is intentionally
// no localStorage persistence: clearing the browser cache must not reset
// onboarding, which is the whole reason this moved server-side.
export interface OnboardingHydrateState {
    dismissed: boolean
    celebrationShown: boolean
    selectedPath: string
    sandboxSkipped: boolean
}

interface OnboardingStore {
    dismissed: boolean
    minimized: boolean
    celebrationShown: boolean
    selectedPath: OnboardingPath | null
    sandboxSkipped: boolean
    // True once server state has been loaded. Consumers gate rendering of the
    // launch center on this so a fresh device shows the correct state instead
    // of flashing the wrong screen before the server responds.
    hydrated: boolean

    dismiss: () => void
    toggleMinimized: () => void
    markCelebrationShown: () => void
    selectPath: (path: OnboardingPath) => void
    clearPath: () => void
    skipSandbox: () => void
    hydrate: (state: OnboardingHydrateState) => void
}

function toPath(value: string): OnboardingPath | null {
    return value === 'agent' || value === 'gateway' || value === 'production' ? value : null
}

export const useOnboardingStore = create<OnboardingStore>()((set) => ({
    dismissed: false,
    minimized: false,
    celebrationShown: false,
    selectedPath: null,
    sandboxSkipped: false,
    hydrated: false,

    dismiss: () => set({ dismissed: true }),
    toggleMinimized: () => set((s) => ({ minimized: !s.minimized })),
    markCelebrationShown: () => set({ celebrationShown: true }),
    selectPath: (path) => set({ selectedPath: path, dismissed: false, minimized: false }),
    clearPath: () => set({ selectedPath: null }),
    skipSandbox: () => set({ sandboxSkipped: true }),
    // Overwrite local state with the server's. Marks the store hydrated. This
    // must never trigger a write-back to the server — useOnboardingSync gates
    // its persist subscription so the hydrate itself is not echoed back.
    hydrate: (state) =>
        set({
            dismissed: state.dismissed,
            celebrationShown: state.celebrationShown,
            selectedPath: toPath(state.selectedPath),
            sandboxSkipped: state.sandboxSkipped,
            hydrated: true,
        }),
}))
