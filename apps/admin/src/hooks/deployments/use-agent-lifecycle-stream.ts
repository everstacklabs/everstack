import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { getApiBaseUrl } from '@/lib/api-url'

export interface AgentLifecycleEvent {
    agent_id: string
    tenant_id?: string
    old_status?: string
    new_status: string
    sandbox_id?: string
    reason?: string
    detail?: string
    timestamp: string
}

interface Options {
    onEvent?: (event: AgentLifecycleEvent) => void
    enabled?: boolean
}

/**
 * Subscribes to per-agent lifecycle transitions broadcast by the backend
 * over SSE (Redis Pub/Sub fanout). Each event triggers a refetch of the
 * agent + sandbox-health queries so badges/messages update live instead
 * of waiting for the 30s poll. Pass an onEvent callback to react to
 * specific transitions (e.g. recovery_pending → show a toast).
 *
 * The stream is tenant-scoped on the backend; passing orgId is required
 * so the gateway can verify the agent belongs to the caller.
 */
export function useAgentLifecycleStream(
    agentId: string,
    orgId: string,
    { onEvent, enabled = true }: Options = {},
) {
    const queryClient = useQueryClient()

    useEffect(() => {
        if (!enabled || !agentId || !orgId) return

        const url = `${getApiBaseUrl()}/v1/agents/${encodeURIComponent(
            agentId,
        )}/lifecycle/subscribe?tenant_id=${encodeURIComponent(orgId)}`
        const source = new EventSource(url, { withCredentials: true })

        const handleLifecycle = (msg: MessageEvent<string>) => {
            try {
                const evt = JSON.parse(msg.data) as AgentLifecycleEvent
                onEvent?.(evt)
                // Any transition invalidates the agent record so the
                // detail page picks up the new lifecycle_status and
                // sandbox_id without waiting for the next poll.
                queryClient.invalidateQueries({
                    queryKey: ['agents', orgId, agentId],
                })
                if (evt.sandbox_id) {
                    queryClient.invalidateQueries({
                        queryKey: ['sandbox-health', evt.sandbox_id],
                    })
                }
            } catch (err) {
                console.warn('[agent-lifecycle-stream] parse failed', err)
            }
        }

        source.addEventListener('lifecycle', handleLifecycle as EventListener)
        // The backend opens with a "ready" event — useful for debugging.
        source.addEventListener('ready', () => {
            // no-op; presence of this listener flushes proxy buffers
        })
        source.onerror = (err) => {
            // EventSource auto-reconnects; we only log once per disconnect.
            console.debug('[agent-lifecycle-stream] reconnecting', err)
        }

        return () => {
            source.removeEventListener('lifecycle', handleLifecycle as EventListener)
            source.close()
        }
    }, [agentId, orgId, enabled, onEvent, queryClient])
}
