import { create } from 'zustand'

/**
 * Lightweight store for sidebar ↔ chat page communication.
 * The sidebar sets a pending action (switch session or new chat),
 * the chat page reads and clears it.
 */
interface PlatformChatState {
  pendingSessionId: string | null
  pendingNewChat: boolean
  requestSwitchSession: (sessionId: string) => void
  requestNewChat: () => void
  consumePending: () => { sessionId: string | null; newChat: boolean }
}

export const usePlatformChatStore = create<PlatformChatState>()((set, get) => ({
  pendingSessionId: null,
  pendingNewChat: false,
  requestSwitchSession: (sessionId) =>
    set({ pendingSessionId: sessionId, pendingNewChat: false }),
  requestNewChat: () =>
    set({ pendingSessionId: null, pendingNewChat: true }),
  consumePending: () => {
    const { pendingSessionId, pendingNewChat } = get()
    set({ pendingSessionId: null, pendingNewChat: false })
    return { sessionId: pendingSessionId, newChat: pendingNewChat }
  },
}))
