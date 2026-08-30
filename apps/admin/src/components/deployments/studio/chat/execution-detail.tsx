import { useEffect, useState } from 'react'
import { Icon } from '@iconify/react'
import {
  useExecutionStore,
  type ExecutionEvent,
} from '@/stores/execution-store'
import { useStudioStore } from '@/stores/studio-store'
import { getWorkflowExecution } from '@/server/workflow-executions'
import { ExecutionTimeline } from './execution-timeline'
import { NodeExecutionDetails } from './node-execution-details'
import { BrowserRunPanel } from './browser-run-panel'
import type { WorkflowExecution } from '@/server/workflow-executions'
import { ui } from '@everstack/ui'

const { Button } = ui

function formatTimestamp(unixMs: bigint | number): string {
  const ms = typeof unixMs === 'bigint' ? Number(unixMs) : unixMs
  if (!ms) return '-'
  const date = new Date(ms)
  return date.toLocaleString(undefined, {
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

function formatDuration(ms: number): string {
  if (!ms) return '-'
  if (ms < 1000) return `${ms}ms`
  return `${(ms / 1000).toFixed(1)}s`
}

const statusConfig: Record<
  string,
  { label: string; color: string; bg: string }
> = {
  running: {
    label: 'Running',
    color: 'text-blue-400 light:text-blue-600',
    bg: 'bg-blue-500/20',
  },
  completed: {
    label: 'Completed',
    color: 'text-emerald-400 light:text-emerald-600',
    bg: 'bg-emerald-500/20',
  },
  failed: {
    label: 'Failed',
    color: 'text-red-400 light:text-red-600',
    bg: 'bg-red-500/20',
  },
}

export function ExecutionDetail() {
  const selectedExecutionId = useExecutionStore((s) => s.selectedExecutionId)
  const clearSelectedExecution = useExecutionStore(
    (s) => s.clearSelectedExecution,
  )
  const tenantId = useStudioStore((s) => s.tenantId)

  const [execution, setExecution] = useState<WorkflowExecution | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [staticEvents, setStaticEvents] = useState<ExecutionEvent[]>([])
  const [replaying, setReplaying] = useState(false)

  useEffect(() => {
    if (!selectedExecutionId || !tenantId) return

    setLoading(true)
    setError(null)

    getWorkflowExecution(selectedExecutionId, tenantId)
      .then((res) => {
        const exec = res.execution
        if (exec) {
          setExecution(exec)

          // Parse events JSON for timeline
          if (exec.eventsJson) {
            try {
              const raw = JSON.parse(exec.eventsJson) as Array<{
                type: string
                node_id: string
                node_type: string
                node_label: string
                data?: Record<string, string>
                chunk_content?: string
                error?: string
                timestamp: number
                duration_ms?: number
              }>
              const parsed: ExecutionEvent[] = raw.map((e) => ({
                type: e.type as ExecutionEvent['type'],
                nodeId: e.node_id,
                nodeType: e.node_type,
                nodeLabel: e.node_label,
                data: e.data,
                chunkContent: e.chunk_content,
                error: e.error,
                timestamp: e.timestamp,
                durationMs: e.duration_ms,
              }))
              setStaticEvents(parsed)
            } catch {
              setStaticEvents([])
            }
          }
        }
      })
      .catch((err) => {
        setError(err instanceof Error ? err.message : String(err))
      })
      .finally(() => {
        setLoading(false)
      })
  }, [selectedExecutionId, tenantId])

  const handleReplay = () => {
    if (!execution || replaying) return

    const workflowId = execution.workflowId
    if (!workflowId || !tenantId) return

    setReplaying(true)

    // Reconstruct input messages from the execution
    const messages = execution.inputMessages.map((m) => ({
      role: m.role,
      content: m.content,
    }))

    const metadata: Record<string, string> = {}
    for (const [k, v] of Object.entries(execution.requestMetadata)) {
      metadata[k] = v
    }

    // Cancel any existing execution first
    const prevCtrl = useExecutionStore.getState().abortController
    if (prevCtrl) prevCtrl.abort()

    // Clear selected execution and switch to chat tab
    useExecutionStore.getState().clearSelectedExecution()

    // Set metadata from the original execution so sendMessage picks it up
    useExecutionStore.setState({ metadata })

    // Use the store's sendMessage which handles the full lifecycle properly.
    // Send the last user message content — sendMessage will prepend it to
    // the existing messages array. We seed the messages with the original
    // conversation history minus the last user message.
    const userMessages = messages.filter((m) => m.role === 'user')
    const lastUserContent =
      userMessages.length > 0
        ? userMessages[userMessages.length - 1].content
        : (messages[messages.length - 1]?.content ?? '')

    // Seed the conversation history (everything except the last user message)
    const seedMessages = messages.slice(0, -1).map((m) => ({
      role: m.role as 'user' | 'assistant',
      content: m.content,
    }))

    useExecutionStore.setState({
      messages: seedMessages,
      activeTab: 'chat',
    })

    useExecutionStore.getState().sendMessage(lastUserContent)
  }

  if (loading) {
    return (
      <div className="flex flex-col items-center justify-center py-12 text-brand-main-400">
        <Icon
          icon="lucide:loader-2"
          className="h-8 w-8 animate-spin mb-2 opacity-50"
        />
        <span className="text-sm">Loading execution...</span>
      </div>
    )
  }

  if (error) {
    return (
      <div className="p-4">
        <button
          onClick={clearSelectedExecution}
          className="flex items-center gap-1 text-xs text-brand-main-400 hover:text-white light:hover:text-brand-main-50 transition-colors mb-3"
        >
          <Icon icon="lucide:arrow-left" className="h-3 w-3" />
          Back
        </button>
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-sm text-red-400 light:text-red-600">
          {error}
        </div>
      </div>
    )
  }

  if (!execution) return null

  const status = statusConfig[execution.status] ?? statusConfig.running
  const hasBrowserRun = staticEvents.some((event) =>
    event.type.startsWith('agent.browser.'),
  )

  return (
    <div className="flex flex-col gap-3 p-3">
      {/* Back button */}
      <button
        onClick={clearSelectedExecution}
        className="flex items-center gap-1 text-xs text-brand-main-400 hover:text-white light:hover:text-brand-main-50 transition-colors self-start"
      >
        <Icon icon="lucide:arrow-left" className="h-3 w-3" />
        Back to runs
      </button>

      {/* Header: status, trigger, duration */}
      <div className="flex items-center gap-2 flex-wrap">
        <span
          className={`inline-flex items-center gap-1 rounded-full px-2 py-0.5 text-[10px] font-medium ${status.bg} ${status.color}`}
        >
          {status.label}
        </span>
        <span className="text-[10px] text-brand-main-300">
          {execution.triggerType}
        </span>
        <span className="text-[10px] text-brand-main-300">
          {formatDuration(execution.durationMs)}
        </span>
      </div>

      {/* Timestamps */}
      <div className="text-[10px] text-brand-main-300 space-y-0.5">
        <div>Started: {formatTimestamp(execution.startedAt)}</div>
        {Number(execution.completedAt) > 0 && (
          <div>Completed: {formatTimestamp(execution.completedAt)}</div>
        )}
      </div>

      {/* Model & provider */}
      {(execution.resolvedModel || execution.resolvedProvider) && (
        <div className="flex items-center gap-2 text-[10px] text-brand-main-400">
          {execution.resolvedProvider && (
            <span className="rounded bg-brand-main-800 px-1.5 py-0.5">
              {execution.resolvedProvider}
            </span>
          )}
          {execution.resolvedModel && (
            <span className="rounded bg-brand-main-800 px-1.5 py-0.5">
              {execution.resolvedModel}
            </span>
          )}
        </div>
      )}

      {/* Token usage */}
      {execution.totalTokens > 0 && (
        <div className="flex items-center gap-3 text-[10px] text-brand-main-400">
          <span>Prompt: {execution.promptTokens}</span>
          <span>Completion: {execution.completionTokens}</span>
          <span className="font-medium text-brand-main-300">
            Total: {execution.totalTokens}
          </span>
        </div>
      )}

      {/* Error message */}
      {execution.errorMessage && (
        <div className="rounded-lg border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-400 light:text-red-600">
          <div className="flex items-start gap-2">
            <Icon
              icon="lucide:alert-circle"
              className="mt-0.5 h-3 w-3 flex-shrink-0"
            />
            <pre className="whitespace-pre-wrap font-sans">
              {execution.errorMessage}
            </pre>
          </div>
        </div>
      )}

      {/* Input messages */}
      {execution.inputMessages.length > 0 && (
        <div>
          <div className="text-[10px] font-medium text-brand-main-400 mb-1.5">
            Input
          </div>
          <div className="flex flex-col gap-1.5">
            {execution.inputMessages.map((msg, idx) => (
              <div
                key={idx}
                className={`rounded-lg px-2.5 py-1.5 text-xs ${
                  msg.role === 'user'
                    ? 'bg-brand-secondary-600/20 text-brand-secondary-200'
                    : 'bg-brand-main-800 text-brand-main-200'
                }`}
              >
                <span className="text-[10px] font-medium text-brand-main-400 block mb-0.5">
                  {msg.role}
                </span>
                <pre className="whitespace-pre-wrap font-sans">
                  {msg.content}
                </pre>
              </div>
            ))}
          </div>
        </div>
      )}

      {/* Output content */}
      {execution.outputContent && (
        <div>
          <div className="text-[10px] font-medium text-brand-main-400 mb-1.5">
            Output
          </div>
          <div className="rounded-lg bg-brand-main-800 px-2.5 py-1.5 text-xs text-brand-main-200">
            <pre className="whitespace-pre-wrap font-sans">
              {execution.outputContent}
            </pre>
          </div>
        </div>
      )}

      {hasBrowserRun && (
        <div>
          <div className="text-[10px] font-medium text-brand-main-400 mb-1.5">
            Computer use
          </div>
          <div className="min-h-[460px] overflow-hidden border border-brand-main-800 rounded">
            <BrowserRunPanel events={staticEvents} tenantId={tenantId} />
          </div>
        </div>
      )}

      {/* Node timeline (static events from past execution) */}
      {staticEvents.length > 0 && (
        <div>
          <div className="text-[10px] font-medium text-brand-main-400 mb-1.5">
            Execution trace
          </div>
          <div className="rounded-lg border border-brand-main-800">
            <ExecutionTimeline staticEvents={staticEvents} />
          </div>
        </div>
      )}

      {/* Node execution details (static events from past execution) */}
      {staticEvents.length > 0 && (
        <div>
          <div className="text-[10px] font-medium text-brand-main-400 mb-1.5">
            Node details
          </div>
          <div className="rounded-lg border border-brand-main-800">
            <NodeExecutionDetails staticEvents={staticEvents} />
          </div>
        </div>
      )}

      {/* Replay button */}
      <Button
        onClick={handleReplay}
        disabled={replaying || execution.status === 'running'}
        className="flex items-center justify-center gap-1.5 rounded bg-brand-secondary-600 px-3 py-2 text-xs font-medium text-white hover:bg-brand-secondary-500 disabled:opacity-50 disabled:cursor-not-allowed transition-colors"
      >
        {replaying ? (
          <>
            <Icon icon="lucide:loader-2" className="h-3 w-3 animate-spin" />
            Replaying...
          </>
        ) : (
          <>
            <Icon icon="lucide:refresh-cw" className="h-3 w-3" />
            Replay execution
          </>
        )}
      </Button>
    </div>
  )
}
