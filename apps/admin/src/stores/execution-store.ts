import { create } from 'zustand'
import { devtools } from 'zustand/middleware'
import { executeWorkflow } from '@/server/workflow-execution'
import { useStudioStore } from '@/stores/studio-store'
import { listWorkflowExecutions } from '@/server/workflow-executions'
import type { WorkflowExecution } from '@/server/workflow-executions'

export interface ExecutionEvent {
  type:
    | 'node.started'
    | 'node.completed'
    | 'node.error'
    | 'chunk'
    | 'done'
    | 'error'
    | 'agent.browser.started'
    | 'agent.browser.ready'
    | 'agent.browser.navigate'
    | 'agent.browser.action'
    | 'agent.browser.snapshot'
    | 'agent.browser.error'
    | 'agent.browser.closed'
    | `agent.${string}`
  nodeId: string
  nodeType: string
  nodeLabel: string
  error?: string
  durationMs?: number
  timestamp?: number
  chunkContent?: string
  data?: Record<string, string>
}

export interface ChatMessage {
  role: 'user' | 'assistant'
  content: string
  audio?: string // base64 audio data
  audioFormat?: string // e.g. 'wav', 'mp3'
  audioContentType?: string // e.g. 'audio/wav'
}

export type StudioTab = 'chat' | 'browser' | 'trace' | 'nodes' | 'runs'
type PanelPosition = 'right' | 'bottom'

interface ExecutionState {
  sessionWorkflowId: string | null // workflow this session belongs to
  messages: ChatMessage[]
  metadata: Record<string, string> // key-value metadata sent with each execution (e.g. auth headers)
  isExecuting: boolean
  events: ExecutionEvent[]
  activeNodeId: string | null
  completedNodeIds: string[]
  errorNodeId: string | null
  streamingContent: string
  executionError: string | null
  isTestPanelOpen: boolean
  abortController: AbortController | null
  panelPosition: PanelPosition
  activeTab: StudioTab

  // Execution history
  executions: WorkflowExecution[]
  executionsTotal: number
  selectedExecutionId: string | null
  loadingExecutions: boolean
  currentExecutionId: string | null // ID of the live execution (from stream)

  // Actions
  sendMessage: (content: string) => void
  setMetadata: (metadata: Record<string, string>) => void
  addEvent: (event: ExecutionEvent) => void
  openTestPanel: () => void
  closeTestPanel: () => void
  togglePanelPosition: () => void
  setActiveTab: (tab: StudioTab) => void
  cancelExecution: () => void
  reset: () => void
  clearMessages: () => void
  fetchExecutions: (
    workflowId: string,
    tenantId: string,
    opts?: { pageSize?: number; offset?: number; statusFilter?: string },
  ) => Promise<void>
  selectExecution: (executionId: string) => void
  clearSelectedExecution: () => void
}

const sessionDefaults = {
  messages: [] as ChatMessage[],
  metadata: {} as Record<string, string>,
  isExecuting: false,
  events: [] as ExecutionEvent[],
  activeNodeId: null as string | null,
  completedNodeIds: [] as string[],
  errorNodeId: null as string | null,
  streamingContent: '',
  executionError: null as string | null,
  abortController: null as AbortController | null,
  currentExecutionId: null as string | null,
}

const getInitialPanelPosition = (): PanelPosition => {
  try {
    const stored = localStorage.getItem('studio-panel-position')
    if (stored === 'right' || stored === 'bottom') return stored
  } catch {}
  return 'right'
}

export const useExecutionStore = create<ExecutionState>()(
  devtools(
    (set, get) => ({
      sessionWorkflowId: null,
      ...sessionDefaults,
      isTestPanelOpen: false,
      panelPosition: getInitialPanelPosition(),
      activeTab: 'chat',
      executions: [],
      executionsTotal: 0,
      selectedExecutionId: null,
      loadingExecutions: false,

      sendMessage: (content: string) => {
        const state = get()

        // If a previous execution is still running (e.g. stale replay),
        // force-cancel it so the user isn't permanently blocked.
        if (state.isExecuting) {
          if (state.abortController) {
            state.abortController.abort()
          }
        }

        const workflowId = useStudioStore.getState().workflowId
        if (!workflowId) return

        const userMessage: ChatMessage = { role: 'user', content }

        // Build full message history for the API
        const allMessages = [...state.messages, userMessage]

        set({
          messages: allMessages,
          isExecuting: true,
          events: [],
          activeNodeId: null,
          completedNodeIds: [],
          errorNodeId: null,
          streamingContent: '',
          executionError: null,
          currentExecutionId: null,
        })

        const tenantId = useStudioStore.getState().tenantId
        const metadata = state.metadata
        const abortController = executeWorkflow(
          workflowId,
          tenantId,
          allMessages.map((m) => ({ role: m.role, content: m.content })),
          (event) => {
            // Capture execution_id from first event's data
            if (event.data?.execution_id && !get().currentExecutionId) {
              set({ currentExecutionId: event.data.execution_id })
            }
            get().addEvent(event)
          },
          (error) => {
            // Skip if already handled by a 'done' or 'node.error' event
            if (!get().isExecuting) return
            const errorMsg =
              error instanceof Error ? error.message : String(error)
            console.error('Workflow execution error:', error)
            set({
              isExecuting: false,
              executionError: errorMsg,
              abortController: null,
            })
          },
          (accumulatedContent: string) => {
            // Skip if already handled by the 'done' event
            if (!get().isExecuting) return
            // Fallback: use the client-accumulated content
            if (accumulatedContent) {
              set((state) => ({
                messages: [
                  ...state.messages,
                  { role: 'assistant', content: accumulatedContent },
                ],
                isExecuting: false,
                activeNodeId: null,
                streamingContent: '',
                abortController: null,
              }))
            } else {
              set({
                isExecuting: false,
                activeNodeId: null,
                abortController: null,
              })
            }
          },
          metadata,
        )

        set({ abortController })
      },

      setMetadata: (metadata: Record<string, string>) => {
        set({ metadata })
      },

      addEvent: (event: ExecutionEvent) => {
        switch (event.type) {
          case 'node.started':
            set((state) => ({
              events: [...state.events, event],
              activeNodeId: event.nodeId,
            }))
            break

          case 'node.completed':
            set((state) => ({
              events: [...state.events, event],
              activeNodeId: null,
              completedNodeIds: [...state.completedNodeIds, event.nodeId],
            }))
            break

          case 'node.error': {
            // Abort the stream so the for-await loop exits
            // immediately and its stale onDone/onError never fires.
            const ctrl1 = get().abortController
            if (ctrl1) ctrl1.abort()
            set((state) => ({
              events: [...state.events, event],
              activeNodeId: null,
              errorNodeId: event.nodeId,
              isExecuting: false,
              executionError: event.error || 'A node encountered an error',
              abortController: null,
            }))
            break
          }

          case 'chunk':
            set((state) => ({
              events: [...state.events, event],
              streamingContent:
                state.streamingContent + (event.chunkContent || ''),
            }))
            break

          case 'agent.browser.started':
            set((state) => ({
              events: [...state.events, event],
              // Surface computer-use activity immediately. Users
              // can switch back to Chat while the run continues.
              activeTab:
                state.activeTab === 'chat' ? 'browser' : state.activeTab,
            }))
            break

          case 'done': {
            // Abort the stream so the for-await loop exits
            // immediately — prevents stale onDone from firing
            // during a later execution and resetting its state.
            const ctrl2 = get().abortController
            if (ctrl2) ctrl2.abort()
            set((state) => {
              const assistantContent = state.streamingContent
              let newMessages = state.messages

              if (assistantContent) {
                newMessages = [
                  ...newMessages,
                  { role: 'assistant' as const, content: assistantContent },
                ]
              } else {
                // Check for audio response from response node
                const responseEvent = state.events.find(
                  (e) =>
                    e.type === 'node.completed' &&
                    e.data?.type === 'audio' &&
                    e.data?.audio,
                )
                if (responseEvent?.data) {
                  newMessages = [
                    ...newMessages,
                    {
                      role: 'assistant' as const,
                      content: '',
                      audio: responseEvent.data.audio,
                      audioFormat: responseEvent.data.format,
                      audioContentType: responseEvent.data.content_type,
                    },
                  ]
                }
              }

              return {
                events: [...state.events, event],
                messages: newMessages,
                isExecuting: false,
                activeNodeId: null,
                streamingContent: '',
                abortController: null,
              }
            })
            break
          }

          case 'error': {
            const ctrl3 = get().abortController
            if (ctrl3) ctrl3.abort()
            set((state) => ({
              events: [...state.events, event],
              isExecuting: false,
              executionError: event.error || 'Unknown error',
              abortController: null,
            }))
            break
          }

          default:
            set((state) => ({ events: [...state.events, event] }))
        }
      },

      openTestPanel: () => {
        const state = get()
        const currentWorkflowId = useStudioStore.getState().workflowId
        // Flush session if the workflow changed
        if (currentWorkflowId !== state.sessionWorkflowId) {
          if (state.abortController) {
            state.abortController.abort()
          }
          set({
            ...sessionDefaults,
            sessionWorkflowId: currentWorkflowId,
            isTestPanelOpen: true,
            activeTab: 'chat',
            executions: [],
            executionsTotal: 0,
            selectedExecutionId: null,
            loadingExecutions: false,
          })
        } else {
          set({ isTestPanelOpen: true })
        }
      },
      closeTestPanel: () => {
        const state = get()
        if (state.abortController) {
          state.abortController.abort()
        }
        set({
          isTestPanelOpen: false,
          isExecuting: false,
          abortController: null,
        })
      },

      togglePanelPosition: () => {
        const current = get().panelPosition
        const next: PanelPosition = current === 'right' ? 'bottom' : 'right'
        try {
          localStorage.setItem('studio-panel-position', next)
        } catch {}
        set({ panelPosition: next })
      },

      setActiveTab: (tab: StudioTab) => set({ activeTab: tab }),

      cancelExecution: () => {
        const state = get()
        if (state.abortController) {
          state.abortController.abort()
        }
        set({
          isExecuting: false,
          activeNodeId: null,
          abortController: null,
          executionError: 'Execution cancelled',
        })
      },

      clearMessages: () => {
        const state = get()
        if (state.abortController) {
          state.abortController.abort()
        }
        set({ ...sessionDefaults, sessionWorkflowId: state.sessionWorkflowId })
      },

      reset: () =>
        set({
          ...sessionDefaults,
          sessionWorkflowId: null,
          isTestPanelOpen: false,
          activeTab: 'chat',
          executions: [],
          executionsTotal: 0,
          selectedExecutionId: null,
          loadingExecutions: false,
        }),

      fetchExecutions: async (workflowId, tenantId, opts) => {
        set({ loadingExecutions: true })
        try {
          const res = await listWorkflowExecutions(workflowId, tenantId, opts)
          // Convert BigInt fields (int64 in proto) to Number so the store
          // is JSON-serializable — zustand devtools uses JSON.stringify
          // which throws on BigInt values.
          const executions = res.executions.map((e) => ({
            ...e,
            startedAt: Number(e.startedAt),
            completedAt: Number(e.completedAt),
          })) as unknown as WorkflowExecution[]
          if (opts?.offset && opts.offset > 0) {
            // Append for "load more"
            set((state) => ({
              executions: [...state.executions, ...executions],
              executionsTotal: res.total,
              loadingExecutions: false,
            }))
          } else {
            set({
              executions,
              executionsTotal: res.total,
              loadingExecutions: false,
            })
          }
        } catch (err) {
          console.error('Failed to fetch executions:', err)
          set({ loadingExecutions: false })
        }
      },

      selectExecution: (executionId) => {
        set({ selectedExecutionId: executionId })
      },

      clearSelectedExecution: () => {
        set({ selectedExecutionId: null })
      },
    }),
    { name: 'execution-store' },
  ),
)

export type { WorkflowExecution }
