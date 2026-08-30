import { createClientFor, createConnectTransport } from '@everstack/client'
import { getApiBaseUrl } from '@/lib/api-url'
import { WorkflowsService } from '@everstack/proto/everstack/workflows/v1/workflows_service_pb'
import { create } from '@everstack/client'
import { ExecuteWorkflowRequestSchema } from '@everstack/proto/everstack/workflows/v1/workflows_pb'
import type { ExecutionEvent } from '@/stores/execution-store'

const env = (
    (typeof import.meta !== 'undefined'
        ? (import.meta as unknown as { env?: Record<string, string | undefined> }).env
        : undefined) ?? {}
) as Record<string, string | undefined>

const baseUrl = getApiBaseUrl()
const connectBase = (env.VITE_CONNECT_BASE_PATH as string | undefined) ?? ''

// Use Connect protocol (not gRPC-Web) for server-streaming.
// gRPC-Web wraps messages in binary frames that browsers can buffer/combine,
// causing lost chunks. Connect protocol uses standard HTTP streaming which
// browsers handle reliably.
const transport = createConnectTransport({
    baseUrl: `${baseUrl}${connectBase}`,
    fetch: (input, init) => fetch(input, { ...init, credentials: 'include' }),
})

const workflowsClient = createClientFor(WorkflowsService)(transport)

/**
 * Execute a workflow via server-streaming RPC.
 * Returns an AbortController that can be used to cancel the execution.
 */
export function executeWorkflow(
    workflowId: string,
    tenantId: string,
    messages: Array<{ role: string; content: string }>,
    onEvent: (event: ExecutionEvent) => void,
    onError: (error: Error) => void,
    onDone: (accumulatedContent: string) => void,
    metadata?: Record<string, string>,
): AbortController {
    const abortController = new AbortController()

    console.log('[workflow-exec] starting stream', { workflowId, tenantId, messageCount: messages.length })

    // Start the streaming call
    const run = async () => {
        try {
            const req = create(ExecuteWorkflowRequestSchema, {
                workflowId,
                tenantId,
                messages,
                metadata,
            })
            const stream = workflowsClient.executeWorkflow(req, {
                signal: abortController.signal,
            })

            // Accumulate streaming content client-side for reliable final content
            let accumulatedContent = ''
            let eventCount = 0

            for await (const event of stream) {
                eventCount++
                if (abortController.signal.aborted) break

                console.log(`[workflow-exec] event #${eventCount}`, event.type, {
                    nodeId: event.nodeId,
                    nodeType: event.nodeType,
                    chunkLen: event.chunkContent?.length ?? 0,
                    hasData: !!event.data && Object.keys(event.data).length > 0,
                })

                const executionEvent: ExecutionEvent = {
                    type: event.type as ExecutionEvent['type'],
                    nodeId: event.nodeId,
                    nodeType: event.nodeType,
                    nodeLabel: event.nodeLabel,
                    error: event.error || undefined,
                    durationMs: event.durationMs ? Number(event.durationMs) : undefined,
                    timestamp: event.timestamp ? Number(event.timestamp) : undefined,
                    chunkContent: event.chunkContent || undefined,
                    data: event.data ? Object.fromEntries(Object.entries(event.data)) : undefined,
                }

                if (event.type === 'chunk' && event.chunkContent) {
                    accumulatedContent += event.chunkContent
                }

                // Fallback: if the done event carries response_content and we
                // haven't accumulated any chunks, use it as the final content.
                if (event.type === 'done' && !accumulatedContent && event.data?.['response_content']) {
                    accumulatedContent = event.data['response_content']
                }

                onEvent(executionEvent)
            }

            console.log('[workflow-exec] stream ended', { eventCount, accumulatedLen: accumulatedContent.length })

            if (!abortController.signal.aborted) {
                onDone(accumulatedContent)
            }
        } catch (error) {
            console.error('[workflow-exec] stream error', error)
            if (!abortController.signal.aborted) {
                onError(error instanceof Error ? error : new Error(String(error)))
            }
        }
    }

    run()

    return abortController
}

/**
 * Execute a workflow via server-streaming RPC as an async generator that yields
 * ExecutionEvents. Preferred over the callback-based executeWorkflow for
 * for-await consumption (e.g. the playground run store). Pass `{ signal }` to
 * cancel; the generator stops when the signal aborts or the stream ends.
 */
export async function* streamWorkflowExecution(
    params: {
        workflowId: string
        tenantId: string
        messages: Array<{ role: string; content: string }>
        metadata?: Record<string, string>
    },
    opts?: { signal?: AbortSignal },
): AsyncGenerator<ExecutionEvent> {
    const req = create(ExecuteWorkflowRequestSchema, {
        workflowId: params.workflowId,
        tenantId: params.tenantId,
        messages: params.messages,
        metadata: params.metadata,
    })

    const stream = workflowsClient.executeWorkflow(req, { signal: opts?.signal })

    for await (const event of stream) {
        if (opts?.signal?.aborted) break
        yield {
            type: event.type as ExecutionEvent['type'],
            nodeId: event.nodeId,
            nodeType: event.nodeType,
            nodeLabel: event.nodeLabel,
            error: event.error || undefined,
            durationMs: event.durationMs ? Number(event.durationMs) : undefined,
            timestamp: event.timestamp ? Number(event.timestamp) : undefined,
            chunkContent: event.chunkContent || undefined,
            data: event.data
                ? Object.fromEntries(Object.entries(event.data))
                : undefined,
        }
    }
}
