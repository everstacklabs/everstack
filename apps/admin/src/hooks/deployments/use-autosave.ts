import { useEffect, useRef, useCallback, useState } from 'react'
import { useNavigate } from '@tanstack/react-router'
import { useStudioStore } from '@/stores/studio-store'
import { useCreateWorkflow, useSaveWorkflowDraft } from '@/hooks/deployments/use-workflows'
import type { StudioNodeData } from '@/components/deployments/studio/types'

type AutosaveStatus = 'idle' | 'saving' | 'saved' | 'error'

interface AutosaveResult {
    status: AutosaveStatus
    lastSavedAt: Date | null
    error: Error | null
    flush: () => void
}

function buildWorkflowNodes(nodes: ReturnType<typeof useStudioStore.getState>['nodes']) {
    return nodes.map((n) => {
        const data = n.data as StudioNodeData
        return {
            id: n.id,
            type: data.nodeType,
            label: data.label,
            position: n.position,
            config: data.config as Record<string, any>,
        }
    })
}

function buildWorkflowEdges(edges: ReturnType<typeof useStudioStore.getState>['edges']) {
    return edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        sourceHandle: e.sourceHandle ?? undefined,
        targetHandle: e.targetHandle ?? undefined,
    }))
}

function takeSnapshot() {
    const { nodes, edges, name, description, viewport, variables } = useStudioStore.getState()
    return JSON.stringify({ nodes, edges, name, description, viewport, variables })
}

const DEBOUNCE_MS = 1000
const RETRY_MS = 5000

export function useAutosave(): AutosaveResult {
    const navigate = useNavigate()
    const createWorkflow = useCreateWorkflow()
    const saveDraft = useSaveWorkflowDraft()

    const [status, setStatus] = useState<AutosaveStatus>('idle')
    const [lastSavedAt, setLastSavedAt] = useState<Date | null>(null)
    const [error, setError] = useState<Error | null>(null)

    // Keep latest references so `save` callback stays stable
    const createWorkflowRef = useRef(createWorkflow)
    const saveDraftRef = useRef(saveDraft)
    const navigateRef = useRef(navigate)
    createWorkflowRef.current = createWorkflow
    saveDraftRef.current = saveDraft
    navigateRef.current = navigate

    const lastSavedSnapshotRef = useRef<string | null>(null)
    const savingRef = useRef(false)
    const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
    const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null)
    const dirtyRef = useRef(false)
    const skipNextChangeRef = useRef(false)

    // Stable save function — never changes reference
    const save = useCallback(async () => {
        if (savingRef.current) return
        const currentSnapshot = takeSnapshot()

        // Skip if nothing changed
        if (lastSavedSnapshotRef.current === currentSnapshot) {
            dirtyRef.current = false
            return
        }

        savingRef.current = true
        dirtyRef.current = false
        setStatus('saving')
        setError(null)

        // Clear any pending retry
        if (retryTimerRef.current) {
            clearTimeout(retryTimerRef.current)
            retryTimerRef.current = null
        }

        try {
            const state = useStudioStore.getState()
            const workflowNodes = buildWorkflowNodes(state.nodes)
            const workflowEdges = buildWorkflowEdges(state.edges)

            if (state.workflowId) {
                const res = await saveDraftRef.current.mutateAsync({
                    id: state.workflowId,
                    name: state.name,
                    description: state.description,
                    nodes: workflowNodes,
                    edges: workflowEdges,
                    viewport: state.viewport,
                })
                if (res.workflow) {
                    // Only sync server-side metadata (version, enabled) without
                    // overwriting nodes/edges/name — the store may have changed
                    // during the async save (e.g. a version restore).
                    skipNextChangeRef.current = true
                    const current = useStudioStore.getState()
                    useStudioStore.setState({
                        // Don't let server's enabled=true override local draft state
                        enabled: current.publishedSnapshot
                            ? current.enabled
                            : (res.workflow.enabled ?? current.enabled),
                        version: res.workflow.version ?? current.version,
                    })
                }
            } else {
                const res = await createWorkflowRef.current.mutateAsync({
                    name: state.name,
                    description: state.description,
                    nodes: workflowNodes,
                    edges: workflowEdges,
                    viewport: state.viewport,
                })
                if (res.workflow?.id) {
                    const current = useStudioStore.getState()
                    skipNextChangeRef.current = true
                    current.setWorkflowData({
                        id: res.workflow.id,
                        name: current.name,
                        description: current.description,
                        nodes: current.nodes,
                        edges: current.edges,
                        viewport: current.viewport,
                        enabled: res.workflow.enabled ?? false,
                        version: res.workflow.version ?? 1,
                        variables: current.variables,
                    })
                    navigateRef.current({
                        to: '/deployments/studio/$workflowId',
                        params: { workflowId: res.workflow.id },
                        replace: true,
                    })
                }
            }

            // Track what was actually sent to the server, not the current
            // store state — the store may have changed during the await
            // (e.g. a version restore) and we need the next save to detect
            // that difference.
            lastSavedSnapshotRef.current = currentSnapshot
            setLastSavedAt(new Date())
            setStatus('saved')
        } catch (err) {
            setError(err instanceof Error ? err : new Error(String(err)))
            setStatus('error')
            dirtyRef.current = true

            // Auto-retry after delay
            retryTimerRef.current = setTimeout(() => {
                retryTimerRef.current = null
                save()
            }, RETRY_MS)
        } finally {
            savingRef.current = false

            // If the store changed while the save was in flight (e.g. a
            // version restore happened during the network round-trip),
            // schedule another save so the new state gets persisted.
            if (dirtyRef.current) {
                if (timerRef.current) clearTimeout(timerRef.current)
                timerRef.current = setTimeout(() => {
                    save()
                }, DEBOUNCE_MS)
            }
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [])

    // Flush: cancel debounce timer and save immediately
    const flush = useCallback(() => {
        if (timerRef.current) {
            clearTimeout(timerRef.current)
            timerRef.current = null
        }
        // Mark dirty so save() proceeds even if the debounce hasn't set it yet
        dirtyRef.current = true
        if (!savingRef.current) {
            save()
        }
    }, [save])

    // Subscribe to all store changes and debounce save
    useEffect(() => {
        lastSavedSnapshotRef.current = takeSnapshot()
        let initialized = false

        const unsub = useStudioStore.subscribe(() => {
            // Skip the very first notification (initial state or setWorkflowData from route loader)
            if (!initialized) {
                initialized = true
                lastSavedSnapshotRef.current = takeSnapshot()
                return
            }

            // Skip internal changes (e.g. metadata sync after save).
            // Don't update lastSavedSnapshotRef here — no actual save happened,
            // so the ref should still reflect the last persisted state.
            if (skipNextChangeRef.current) {
                skipNextChangeRef.current = false
                return
            }

            dirtyRef.current = true

            if (timerRef.current) clearTimeout(timerRef.current)
            timerRef.current = setTimeout(() => {
                save()
            }, DEBOUNCE_MS)
        })

        // If no initialization callback fires shortly after mount, mark as
        // initialized so the first real user change isn't swallowed.
        const initTimer = setTimeout(() => {
            if (!initialized) {
                initialized = true
                lastSavedSnapshotRef.current = takeSnapshot()
            }
        }, 100)

        return () => {
            unsub()
            clearTimeout(initTimer)
            if (timerRef.current) clearTimeout(timerRef.current)
            if (retryTimerRef.current) clearTimeout(retryTimerRef.current)
        }
    }, [save])

    // Flush immediately when the tab becomes hidden (user switching away)
    useEffect(() => {
        const handler = () => {
            if (document.visibilityState === 'hidden') {
                flush()
            }
        }
        document.addEventListener('visibilitychange', handler)
        return () => document.removeEventListener('visibilitychange', handler)
    }, [flush])

    // Flush on beforeunload + show warning if still dirty/saving
    useEffect(() => {
        const handler = (e: BeforeUnloadEvent) => {
            flush()
            if (dirtyRef.current || savingRef.current) {
                e.preventDefault()
            }
        }
        window.addEventListener('beforeunload', handler)
        return () => window.removeEventListener('beforeunload', handler)
    }, [flush])

    // Cmd/Ctrl+S to save immediately
    useEffect(() => {
        const handler = (e: KeyboardEvent) => {
            if ((e.metaKey || e.ctrlKey) && e.key === 's') {
                e.preventDefault()
                flush()
            }
        }
        window.addEventListener('keydown', handler)
        return () => window.removeEventListener('keydown', handler)
    }, [flush])

    return { status, lastSavedAt, error, flush }
}
